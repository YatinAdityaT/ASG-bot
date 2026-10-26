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

func (cm *CommandManager) handlePing(ctx context.Context, client utils.MessageSender, info types.MessageInfo, args []string) {
	utils.ReplyToMessage(ctx, client, info.Chat, "Pong!", info.ID, info.Sender)
}

func (cm *CommandManager) handleEcho(ctx context.Context, client utils.MessageSender, info types.MessageInfo, args []string) {
	response := args[0]
	utils.ReplyToMessage(ctx, client, info.Chat, response, info.ID, info.Sender)
}

func (cm *CommandManager) handleHelp(ctx context.Context, client utils.MessageSender, info types.MessageInfo, args []string) {
	var b strings.Builder
	b.WriteString("Hello! I am a bot. Here are my commands:\n\n")

	for _, cmd := range cm.orderedList {
		// Show only non-admin commands to regular users
		if !cmd.IsAdminCommand {
			fmt.Fprintf(&b, "`%s` - %s\n", cmd.Name, cmd.Description)
		}
	}

	utils.ReplyToMessage(ctx, client, info.Chat, b.String(), info.ID, info.Sender)
}

func (cm *CommandManager) handleDie(ctx context.Context, client utils.MessageSender, info types.MessageInfo, args []string) {
	response := "Goodbye cruel world!"
	if err := utils.ReplyToMessage(ctx, client, info.Chat, response, info.ID, info.Sender); err == nil {
		cm.shutdown()
	}
}

func (cm *CommandManager) handleReminder(db *sql.DB, ctx context.Context, client utils.MessageSender, info types.MessageInfo, args []string) {
	input := args[0]
	t, msg, err := utils.ExtractTimeAndMessage(input)
	if err != nil {
		utils.ReplyToMessage(ctx, client, info.Chat, "Sorry, I couldn't parse that time. Please try again.", info.ID, info.Sender)
		return
	}

	_, err = db.ExecContext(ctx,
		"INSERT INTO reminders (chat_id, to_id, message, remind_at) VALUES (?, ?, ?, ?)",
		info.Chat.String(), info.Sender.ToNonAD().String(), "@"+info.Sender.User+" you had asked me to \""+msg+"\"", t,
	)
	if err != nil {
		utils.ReplyToMessage(ctx, client, info.Chat, "Failed to save reminder: "+err.Error(), info.ID, info.Sender)
		return
	}

	resp := fmt.Sprintf("✅ Reminder set for %s: \"%s\"", t.Format("3:04PM Jan 2"), msg)
	utils.ReplyToMessage(ctx, client, info.Chat, resp, info.ID, info.Sender)
}

func (cm *CommandManager) handleRemindList(db *sql.DB, ctx context.Context, client utils.MessageSender, info types.MessageInfo, args []string) {
	rows, err := db.QueryContext(ctx,
		"SELECT id, message, remind_at FROM reminders WHERE chat_id = ? AND to_id = ? ORDER BY remind_at ASC",
		info.Chat.String(), info.Sender.ToNonAD().String(),
	)
	if err != nil {
		utils.ReplyToMessage(ctx, client, info.Chat, "Failed to retrieve reminders", info.ID, info.Sender)
		return
	}
	defer rows.Close()

	var response strings.Builder
	for rows.Next() {
		var id int
		var msg string
		var remindAt time.Time
		rows.Scan(&id, &msg, &remindAt)
		response.WriteString(fmt.Sprintf("[ID: %d]: %s – %s\n", id, remindAt.Format("3:04PM Jan 2"), msg))
	}

	if response.Len() == 0 {
		response.WriteString("📭 No reminders found.")
	}

	utils.ReplyToMessage(ctx, client, info.Chat, response.String(), info.ID, info.Sender)
}

func (cm *CommandManager) handleRemindDelete(db *sql.DB, ctx context.Context, client utils.MessageSender, info types.MessageInfo, args []string) {
	if len(args) < 1 {
		utils.ReplyToMessage(ctx, client, info.Chat, "Please specify the ID to delete.", info.ID, info.Sender)
		return
	}

	id, err := strconv.Atoi(args[0])
	if err != nil {
		utils.ReplyToMessage(ctx, client, info.Chat, "Invalid ID format.", info.ID, info.Sender)
		return
	}

	res, err := db.ExecContext(ctx,
		"DELETE FROM reminders WHERE id = ? AND chat_id = ?",
		id, info.Chat.String(),
	)
	if err != nil {
		utils.ReplyToMessage(ctx, client, info.Chat, "Failed to delete reminder.", info.ID, info.Sender)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		utils.ReplyToMessage(ctx, client, info.Chat, "Reminder not found or doesn't belong to this chat.", info.ID, info.Sender)
	} else {
		utils.ReplyToMessage(ctx, client, info.Chat, "✅ Reminder deleted.", info.ID, info.Sender)
	}
}

func (cm *CommandManager) handleRemindUpdate(db *sql.DB, ctx context.Context, client utils.MessageSender, info types.MessageInfo, args []string) {
	if len(args) < 2 {
		utils.ReplyToMessage(ctx, client, info.Chat, "Usage: /remind update <id> <new time> <message>", info.ID, info.Sender)
		return
	}

	id, err := strconv.Atoi(args[0])
	if err != nil {
		utils.ReplyToMessage(ctx, client, info.Chat, "Invalid ID format.", info.ID, info.Sender)
		return
	}

	t, msg, err := utils.ExtractTimeAndMessage(args[1])
	if err != nil {
		utils.ReplyToMessage(ctx, client, info.Chat, "Failed to parse time or message.", info.ID, info.Sender)
		return
	}

	_, err = db.ExecContext(ctx,
		"INSERT INTO reminders (chat_id, to_id, message, remind_at) VALUES (?, ?, ?, ?)",
	)

	res, err := db.ExecContext(ctx,
		"UPDATE reminders SET message = ?, remind_at = ? WHERE id = ? AND chat_id = ?",
		"@"+info.Sender.User+" you had asked me to \""+msg+"\"", t, id, info.Chat.String(),
	)
	if err != nil {
		utils.ReplyToMessage(ctx, client, info.Chat, "Failed to update reminder.", info.ID, info.Sender)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		utils.ReplyToMessage(ctx, client, info.Chat, "Reminder not found or doesn't belong to this chat.", info.ID, info.Sender)
	} else {
		utils.ReplyToMessage(ctx, client, info.Chat, "✅ Reminder updated.", info.ID, info.Sender)
	}
}

// func (cm *CommandManager) handleToneList(ctx context.Context, client *whatsmeow.Client, info types.MessageInfo, args []string) {

// }

// func (cm *CommandManager) handleMute(ctx context.Context, client *whatsmeow.Client, info types.MessageInfo, args []string) {

// }

// func (cm *CommandManager) handleUnmute(ctx context.Context, client *whatsmeow.Client, info types.MessageInfo, args []string) {

// }
