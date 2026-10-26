package cmd

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"whatsappBot/utils"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func (cm *CommandManager) RouteMessage(db *sql.DB, ctx context.Context, evt *events.Message) error {
	info := evt.Info
	messageText := utils.ExtractText(evt)

	if messageText == "" || !strings.HasPrefix(messageText, "/") {
		return nil
	}

	for _, cmd := range cm.commands {
		matches := caseInsensitive(cmd.Regex).FindStringSubmatch(messageText)
		if len(matches) > 0 {
			if cmd.IsAdminCommand && !cm.isAdmin(info.Sender) {
				return utils.ReplyToMessage(ctx, cm.client, info.Chat, "You do not have permission to use this command.", info.ID, info.Sender)
			}

			commandCtx := CommandContext{
				Info:     info,
				Mentions: extractMentions(evt),
			}
			return cmd.Callback(db, ctx, cm.client, commandCtx, matches[1:])
		}
	}
	return nil
}

func caseInsensitive(expression *regexp.Regexp) *regexp.Regexp {
	return regexp.MustCompile("(?i:" + expression.String() + ")")
}

func extractMentions(evt *events.Message) []types.JID {
	if evt == nil || evt.Message == nil || evt.Message.GetExtendedTextMessage() == nil {
		return nil
	}
	contextInfo := evt.Message.GetExtendedTextMessage().GetContextInfo()
	if contextInfo == nil {
		return nil
	}

	mentions := make([]types.JID, 0, len(contextInfo.GetMentionedJID()))
	for _, rawJID := range contextInfo.GetMentionedJID() {
		jid, err := types.ParseJID(rawJID)
		if err == nil && jid.User != "" {
			mentions = append(mentions, jid.ToNonAD())
		}
	}
	return mentions
}

func (cm *CommandManager) isAdmin(jid types.JID) bool {
	_, ok := cm.admins[jid.User]
	return ok
}
