package cmd

import (
	"context"
	"database/sql"
	"strings"
	"whatsappBot/utils"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func (cm *CommandManager) RouteMessage(db *sql.DB, ctx context.Context, evt *events.Message) {
	info := evt.Info
	messageText := utils.ExtractCleanText(evt)

	if messageText == "" || !strings.HasPrefix(messageText, "/") {
		return
	}

	for _, cmd := range cm.commands {
		matches := cmd.Regex.FindStringSubmatch(messageText)
		if len(matches) > 0 {
			if cmd.IsAdminCommand && !cm.isAdmin(info.Sender) {
				_ = utils.ReplyToMessage(ctx, cm.client, info.Chat, "You do not have permission to use this command.", info.ID, info.Sender)
				return
			}

			cmd.Callback(db, ctx, cm.client, info, matches[1:])
			return
		}
	}
}

func (cm *CommandManager) isAdmin(jid types.JID) bool {
	_, ok := cm.admins[jid.User]
	return ok
}
