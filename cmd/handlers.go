package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
	"whatsappBot/utils"

	"go.mau.fi/whatsmeow/types"
)

const reminderUsage = `Sorry, I couldn't understand that reminder. Try /remind @Alex "submit the form" tomorrow at 3pm or /remind me to stretch in 30 minutes.`

func (cm *CommandManager) handlePing(ctx context.Context, client utils.MessageSender, info types.MessageInfo, args []string) error {
	return utils.ReplyToMessage(ctx, client, info.Chat, "Pong!", info.ID, info.Sender)
}

func (cm *CommandManager) handleEcho(ctx context.Context, client utils.MessageSender, info types.MessageInfo, args []string) error {
	response := args[0]
	return utils.ReplyToMessage(ctx, client, info.Chat, response, info.ID, info.Sender)
}

func (cm *CommandManager) handleHelp(ctx context.Context, client utils.MessageSender, info types.MessageInfo, args []string) error {
	var b strings.Builder
	b.WriteString("Hello! I am a bot. Here are my commands:\n\n")

	for _, cmd := range cm.orderedList {
		// Show only non-admin commands to regular users
		if !cmd.IsAdminCommand {
			fmt.Fprintf(&b, "`%s` - %s\n", cmd.Name, cmd.Description)
		}
	}

	return utils.ReplyToMessage(ctx, client, info.Chat, b.String(), info.ID, info.Sender)
}

func (cm *CommandManager) handleDie(ctx context.Context, client utils.MessageSender, info types.MessageInfo, args []string) error {
	response := "Goodbye cruel world!"
	if err := utils.ReplyToMessage(ctx, client, info.Chat, response, info.ID, info.Sender); err != nil {
		return err
	}
	if cm.shutdown != nil {
		cm.shutdown()
	}
	return nil
}

func (cm *CommandManager) handleReminder(db *sql.DB, ctx context.Context, client utils.MessageSender, commandCtx CommandContext, args []string) error {
	info := commandCtx.Info
	if commandCtx.MentionError != nil {
		return utils.ReplyToMessage(ctx, client, info.Chat, reminderUsage, info.ID, info.Sender)
	}

	request, err := utils.ParseReminder(
		"/remind "+args[0],
		commandCtx.Mentions,
		info.Sender,
		info.PushName,
		time.Now(),
	)
	if err != nil {
		return utils.ReplyToMessage(ctx, client, info.Chat, reminderUsage, info.ID, info.Sender)
	}

	_, err = db.ExecContext(ctx,
		"INSERT INTO reminders (chat_id, to_id, requested_by, message, remind_at) VALUES (?, ?, ?, ?, ?)",
		info.Chat.String(),
		request.Target.String(),
		request.Requester.String(),
		reminderDeliveryMessage(request),
		request.RemindAt,
	)
	if err != nil {
		return utils.ReplyToMessage(ctx, client, info.Chat, "Failed to save reminder: "+err.Error(), info.ID, info.Sender)
	}

	resp := fmt.Sprintf("✅ Reminder set for %s: \"%s\"", request.RemindAt.Format("3:04PM Jan 2"), request.Body)
	return utils.ReplyToMessage(ctx, client, info.Chat, resp, info.ID, info.Sender)
}

func reminderDeliveryMessage(request utils.ReminderRequest) string {
	if request.Target == request.Requester {
		return fmt.Sprintf(`@%s you asked me to remind you to %q`, request.Target.User, request.Body)
	}
	return fmt.Sprintf(
		`@%s %s asked me to remind you to %q`,
		request.Target.User,
		request.RequesterName,
		request.Body,
	)
}

func (cm *CommandManager) handleRemindList(db *sql.DB, ctx context.Context, client utils.MessageSender, commandCtx CommandContext, args []string) error {
	info := commandCtx.Info
	rows, err := db.QueryContext(ctx,
		"SELECT id, message, remind_at FROM reminders WHERE chat_id = ? AND requested_by = ? ORDER BY remind_at ASC",
		info.Chat.String(), info.Sender.ToNonAD().String(),
	)
	if err != nil {
		return utils.ReplyToMessage(ctx, client, info.Chat, "Failed to retrieve reminders", info.ID, info.Sender)
	}
	defer rows.Close()

	var response strings.Builder
	for rows.Next() {
		var id int
		var msg string
		var remindAt time.Time
		if err := rows.Scan(&id, &msg, &remindAt); err != nil {
			return utils.ReplyToMessage(ctx, client, info.Chat, "Failed to retrieve reminders", info.ID, info.Sender)
		}
		response.WriteString(fmt.Sprintf("[ID: %d]: %s – %s\n", id, remindAt.Format("3:04PM Jan 2"), msg))
	}
	if err := rows.Err(); err != nil {
		return utils.ReplyToMessage(ctx, client, info.Chat, "Failed to retrieve reminders", info.ID, info.Sender)
	}

	if response.Len() == 0 {
		response.WriteString("📭 No reminders found.")
	}

	return utils.ReplyToMessage(ctx, client, info.Chat, response.String(), info.ID, info.Sender)
}

func (cm *CommandManager) handleRemindDelete(db *sql.DB, ctx context.Context, client utils.MessageSender, commandCtx CommandContext, args []string) error {
	info := commandCtx.Info
	if len(args) < 1 {
		return utils.ReplyToMessage(ctx, client, info.Chat, "Please specify the ID to delete.", info.ID, info.Sender)
	}

	id, err := strconv.Atoi(args[0])
	if err != nil {
		return utils.ReplyToMessage(ctx, client, info.Chat, "Invalid ID format.", info.ID, info.Sender)
	}

	res, err := db.ExecContext(ctx,
		"DELETE FROM reminders WHERE id = ? AND chat_id = ? AND requested_by = ?",
		id, info.Chat.String(), info.Sender.ToNonAD().String(),
	)
	if err != nil {
		return utils.ReplyToMessage(ctx, client, info.Chat, "Failed to delete reminder.", info.ID, info.Sender)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return utils.ReplyToMessage(ctx, client, info.Chat, "Failed to delete reminder.", info.ID, info.Sender)
	}
	if n == 0 {
		return utils.ReplyToMessage(ctx, client, info.Chat, "Reminder not found or doesn't belong to this chat.", info.ID, info.Sender)
	}
	return utils.ReplyToMessage(ctx, client, info.Chat, "✅ Reminder deleted.", info.ID, info.Sender)
}

func (cm *CommandManager) handleRemindUpdate(db *sql.DB, ctx context.Context, client utils.MessageSender, commandCtx CommandContext, args []string) error {
	info := commandCtx.Info
	if commandCtx.MentionError != nil {
		return utils.ReplyToMessage(ctx, client, info.Chat, reminderUsage, info.ID, info.Sender)
	}
	if len(args) < 2 {
		return utils.ReplyToMessage(ctx, client, info.Chat, "Usage: /remind update <id> <new time> <message>", info.ID, info.Sender)
	}

	id, err := strconv.Atoi(args[0])
	if err != nil {
		return utils.ReplyToMessage(ctx, client, info.Chat, "Invalid ID format.", info.ID, info.Sender)
	}

	request, err := utils.ParseReminder(
		"/remind "+args[1],
		nil,
		info.Sender,
		info.PushName,
		time.Now(),
	)
	if err != nil {
		return utils.ReplyToMessage(ctx, client, info.Chat, "Failed to parse time or message.", info.ID, info.Sender)
	}

	var targetRaw string
	err = db.QueryRowContext(ctx,
		"SELECT to_id FROM reminders WHERE id = ? AND chat_id = ? AND requested_by = ?",
		id, info.Chat.String(), info.Sender.ToNonAD().String(),
	).Scan(&targetRaw)
	if err == sql.ErrNoRows {
		return utils.ReplyToMessage(ctx, client, info.Chat, "Reminder not found or doesn't belong to this chat.", info.ID, info.Sender)
	}
	if err != nil {
		return utils.ReplyToMessage(ctx, client, info.Chat, "Failed to update reminder.", info.ID, info.Sender)
	}
	target, err := types.ParseJID(targetRaw)
	if err != nil {
		return utils.ReplyToMessage(ctx, client, info.Chat, "Failed to update reminder.", info.ID, info.Sender)
	}
	request.Target = target.ToNonAD()

	res, err := db.ExecContext(ctx,
		"UPDATE reminders SET message = ?, remind_at = ? WHERE id = ? AND chat_id = ? AND requested_by = ?",
		reminderDeliveryMessage(request), request.RemindAt, id, info.Chat.String(), info.Sender.ToNonAD().String(),
	)
	if err != nil {
		return utils.ReplyToMessage(ctx, client, info.Chat, "Failed to update reminder.", info.ID, info.Sender)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return utils.ReplyToMessage(ctx, client, info.Chat, "Failed to update reminder.", info.ID, info.Sender)
	}
	if n == 0 {
		return utils.ReplyToMessage(ctx, client, info.Chat, "Reminder not found or doesn't belong to this chat.", info.ID, info.Sender)
	}
	return utils.ReplyToMessage(ctx, client, info.Chat, "✅ Reminder updated.", info.ID, info.Sender)
}

// func (cm *CommandManager) handleToneList(ctx context.Context, client *whatsmeow.Client, info types.MessageInfo, args []string) {

// }

// func (cm *CommandManager) handleMute(ctx context.Context, client *whatsmeow.Client, info types.MessageInfo, args []string) {

// }

// func (cm *CommandManager) handleUnmute(ctx context.Context, client *whatsmeow.Client, info types.MessageInfo, args []string) {

// }
