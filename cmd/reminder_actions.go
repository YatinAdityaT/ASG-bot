package cmd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"

	"whatsappBot/reminderdelivery"
	"whatsappBot/utils"
)

const (
	snoozeUsage     = "Reply to a fired reminder with /snooze 30 minutes."
	rescheduleUsage = "Reply to a fired reminder with /reschedule tomorrow at 3pm."
)

var errWeekdayReminderWeekend = errors.New(
	"weekday reminders must be moved to a weekday",
)

func (cm *CommandManager) handleSnooze(
	db *sql.DB,
	ctx context.Context,
	client utils.MessageSender,
	commandCtx CommandContext,
	args []string,
) error {
	now := cm.now()
	next, err := utils.ParseFutureTime(strings.TrimSpace(args[0]), now)
	if err != nil {
		next, err = utils.ParseFutureTime("in "+strings.TrimSpace(args[0]), now)
	}
	if err != nil {
		return cm.replyReminderAction(ctx, client, commandCtx, snoozeUsage)
	}
	return cm.moveQuotedReminder(db, ctx, client, commandCtx, next, "snoozed")
}

func (cm *CommandManager) handleReschedule(
	db *sql.DB,
	ctx context.Context,
	client utils.MessageSender,
	commandCtx CommandContext,
	args []string,
) error {
	next, err := utils.ParseFutureTime(strings.TrimSpace(args[0]), cm.now())
	if err != nil {
		return cm.replyReminderAction(ctx, client, commandCtx, rescheduleUsage)
	}
	return cm.moveQuotedReminder(db, ctx, client, commandCtx, next, "rescheduled")
}

func (cm *CommandManager) moveQuotedReminder(
	db *sql.DB,
	ctx context.Context,
	client utils.MessageSender,
	commandCtx CommandContext,
	next time.Time,
	action string,
) error {
	if commandCtx.QuotedMessageID == "" {
		if action == "snoozed" {
			return cm.replyReminderAction(ctx, client, commandCtx, snoozeUsage)
		}
		return cm.replyReminderAction(ctx, client, commandCtx, rescheduleUsage)
	}
	reference, err := reminderdelivery.Find(
		ctx,
		db,
		commandCtx.QuotedMessageID,
		commandCtx.Info.Chat.String(),
	)
	if errors.Is(err, reminderdelivery.ErrNotFound) {
		return cm.replyReminderAction(
			ctx, client, commandCtx,
			"That quoted message is not a reminder I can change.",
		)
	}
	if err != nil {
		return fmt.Errorf("resolve quoted reminder: %w", err)
	}
	if !authorizedReminderActor(commandCtx, reference) {
		return cm.replyReminderAction(
			ctx, client, commandCtx,
			"Only the reminder requester or recipient can change it.",
		)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reminder update: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE reminder_deliveries
		SET acted_at = ?
		WHERE message_id = ? AND chat_id = ? AND acted_at IS NULL
	`, cm.now().UTC().UnixNano(), reference.MessageID, reference.ChatID)
	if err != nil {
		return fmt.Errorf("claim reminder delivery: %w", err)
	}
	claimed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read reminder claim: %w", err)
	}
	if claimed == 0 {
		return cm.replyReminderAction(
			ctx, client, commandCtx,
			"That reminder reply has already been used. Reply to its latest delivery.",
		)
	}

	recurrence, err := moveReminderSeries(ctx, tx, reference, next)
	if errors.Is(err, sql.ErrNoRows) {
		return cm.replyReminderAction(
			ctx, client, commandCtx,
			"That reminder no longer exists.",
		)
	}
	if errors.Is(err, errWeekdayReminderWeekend) {
		return cm.replyReminderAction(
			ctx, client, commandCtx,
			"Choose a Monday–Friday time for a weekdays reminder.",
		)
	}
	if err != nil {
		return fmt.Errorf("move reminder series: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reminder update: %w", err)
	}

	text := fmt.Sprintf(
		"✅ Reminder %s for %s.",
		action,
		next.In(time.Local).Format("3:04 PM on Jan 2"),
	)
	if recurrence.IsRecurring() {
		text = fmt.Sprintf(
			"✅ Repeating reminder %s for %s (%s).",
			action,
			next.In(time.Local).Format("3:04 PM on Jan 2"),
			recurrence.Label(),
		)
	}
	return cm.replyReminderAction(ctx, client, commandCtx, text)
}

func moveReminderSeries(
	ctx context.Context,
	tx *sql.Tx,
	reference reminderdelivery.Reference,
	next time.Time,
) (utils.Recurrence, error) {
	switch reference.SourceType {
	case reminderdelivery.SourceWhatsApp:
		return moveWhatsAppReminder(ctx, tx, reference, next)
	case reminderdelivery.SourceMeet:
		return moveMeetReminder(ctx, tx, reference, next)
	default:
		return utils.Recurrence{}, errors.New("unsupported reminder source")
	}
}

func moveWhatsAppReminder(
	ctx context.Context,
	tx *sql.Tx,
	reference reminderdelivery.Reference,
	next time.Time,
) (utils.Recurrence, error) {
	var (
		recurrenceKind    string
		recurrenceWeekday sql.NullInt64
		body              sql.NullString
		requesterName     sql.NullString
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT recurrence_kind, recurrence_weekday, body, requester_name
		FROM reminders
		WHERE id = ? AND chat_id = ?
	`, reference.ReminderID, reference.ChatID).Scan(
		&recurrenceKind,
		&recurrenceWeekday,
		&body,
		&requesterName,
	); err != nil {
		return utils.Recurrence{}, err
	}
	recurrence := recurrenceFromColumns(recurrenceKind, recurrenceWeekday)
	if recurrence.Kind == utils.RecurrenceWeekdays &&
		(next.Weekday() == time.Saturday || next.Weekday() == time.Sunday) {
		return recurrence, errWeekdayReminderWeekend
	}
	weekday := recurrenceWeekdayValueForUpdate(recurrence, next)
	message := ""
	if body.Valid && strings.TrimSpace(body.String) != "" {
		target, err := types.ParseJID(reference.TargetJID)
		if err != nil {
			return recurrence, err
		}
		requester, err := types.ParseJID(reference.RequesterJID)
		if err != nil {
			return recurrence, err
		}
		message = utils.FormatReminderDelivery(
			target.ToNonAD(),
			requester.ToNonAD(),
			requesterName.String,
			body.String,
			next,
		)
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE reminders
		SET remind_at = ?,
			message = CASE WHEN ? = '' THEN message ELSE ? END,
			recurrence_weekday = ?, active = 1, updated_at = ?
		WHERE id = ? AND chat_id = ?
	`, next, message, message, weekday, time.Now().UTC().UnixNano(),
		reference.ReminderID, reference.ChatID)
	if err != nil {
		return recurrence, err
	}
	return recurrenceWithShiftedWeekday(recurrence, next), nil
}

func moveMeetReminder(
	ctx context.Context,
	tx *sql.Tx,
	reference reminderdelivery.Reference,
	next time.Time,
) (utils.Recurrence, error) {
	var recurrenceKind string
	var recurrenceWeekday sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT recurrence_kind, recurrence_weekday
		FROM meet_reminders
		WHERE id = ? AND whatsapp_chat_id = ?
	`, reference.ReminderID, reference.ChatID).Scan(
		&recurrenceKind,
		&recurrenceWeekday,
	); err != nil {
		return utils.Recurrence{}, err
	}
	recurrence := recurrenceFromColumns(recurrenceKind, recurrenceWeekday)
	if recurrence.Kind == utils.RecurrenceWeekdays &&
		(next.Weekday() == time.Saturday || next.Weekday() == time.Sunday) {
		return recurrence, errWeekdayReminderWeekend
	}
	shifted := recurrenceWithShiftedWeekday(recurrence, next)
	_, err := tx.ExecContext(ctx, `
		UPDATE meet_reminders
		SET remind_at = ?, recurrence_weekday = ?,
			whatsapp_delivered_at = NULL, meet_status = 'pending',
			completed_at = NULL, updated_at = ?
		WHERE id = ? AND whatsapp_chat_id = ?
	`, next.UTC().UnixNano(), recurrenceWeekdayValueForUpdate(recurrence, next),
		time.Now().UTC().UnixNano(), reference.ReminderID, reference.ChatID)
	if err != nil {
		return recurrence, err
	}
	return shifted, nil
}

func recurrenceFromColumns(kind string, weekday sql.NullInt64) utils.Recurrence {
	recurrence := utils.Recurrence{Kind: kind}
	if weekday.Valid {
		recurrence.Weekday = time.Weekday(weekday.Int64)
	}
	return recurrence
}

func recurrenceWithShiftedWeekday(
	recurrence utils.Recurrence,
	next time.Time,
) utils.Recurrence {
	if recurrence.Kind == utils.RecurrenceWeekly {
		recurrence.Weekday = next.Weekday()
	}
	return recurrence
}

func recurrenceWeekdayValueForUpdate(
	recurrence utils.Recurrence,
	next time.Time,
) any {
	if recurrence.Kind == utils.RecurrenceWeekly {
		return int(next.Weekday())
	}
	return nil
}

func sameReminderIdentity(sender types.JID, stored string) bool {
	candidate, err := types.ParseJID(stored)
	if err != nil {
		return false
	}
	return sender.ToNonAD() == candidate.ToNonAD()
}

func authorizedReminderActor(
	commandCtx CommandContext,
	reference reminderdelivery.Reference,
) bool {
	candidates := []types.JID{
		commandCtx.SenderIdentity,
		commandCtx.Info.Sender,
		commandCtx.Info.SenderAlt,
	}
	for _, candidate := range candidates {
		if candidate.User == "" {
			continue
		}
		if sameReminderIdentity(candidate, reference.RequesterJID) ||
			sameReminderIdentity(candidate, reference.TargetJID) {
			return true
		}
	}
	return false
}

func (cm *CommandManager) replyReminderAction(
	ctx context.Context,
	client utils.MessageSender,
	commandCtx CommandContext,
	text string,
) error {
	return utils.ReplyToMessage(
		ctx,
		client,
		commandCtx.Info.Chat,
		text,
		commandCtx.Info.ID,
		commandCtx.Info.Sender,
	)
}
