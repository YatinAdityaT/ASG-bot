package cmd

import (
	"context"
	"database/sql"
	"fmt"
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
		matches := cmd.Regex.FindStringSubmatch(messageText)
		if len(matches) > 0 {
			senderIdentity := canonicalSenderIdentity(info.MessageSource)
			if cmd.IsAdminCommand && !cm.isAdmin(senderIdentity) {
				return utils.ReplyToMessage(ctx, cm.client, info.Chat, "You do not have permission to use this command.", info.ID, info.Sender)
			}

			mentions, mentionErr := extractMentions(evt)
			commandCtx := CommandContext{
				Info:           info,
				SenderIdentity: senderIdentity,
				Mentions:       mentions,
				MentionError:   mentionErr,
			}
			return cmd.Callback(db, ctx, cm.client, commandCtx, matches[1:])
		}
	}
	return nil
}

func canonicalSenderIdentity(source types.MessageSource) types.JID {
	sender := source.Sender.ToNonAD()
	if (sender.Server == types.HiddenUserServer || sender.Server == types.HostedLIDServer) &&
		!source.SenderAlt.IsEmpty() {
		return source.SenderAlt.ToNonAD()
	}
	return sender
}

func extractMentions(evt *events.Message) ([]types.JID, error) {
	if evt == nil || evt.Message == nil || evt.Message.GetExtendedTextMessage() == nil {
		return nil, nil
	}
	contextInfo := evt.Message.GetExtendedTextMessage().GetContextInfo()
	if contextInfo == nil {
		return nil, nil
	}

	mentions := make([]types.JID, 0, len(contextInfo.GetMentionedJID()))
	var mentionErr error
	for _, rawJID := range contextInfo.GetMentionedJID() {
		jid, err := types.ParseJID(rawJID)
		if err == nil && jid.User != "" {
			mentions = append(mentions, jid.ToNonAD())
		} else {
			mentionErr = fmt.Errorf("invalid mentioned JID metadata")
		}
	}
	return mentions, mentionErr
}

func (cm *CommandManager) isAdmin(jid types.JID) bool {
	_, ok := cm.admins[jid.User]
	return ok
}
