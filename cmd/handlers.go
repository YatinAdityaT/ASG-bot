package cmd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"whatsappBot/identity"
	"whatsappBot/meetmonitor"
	"whatsappBot/utils"

	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

const reminderUsage = `Sorry, I couldn't understand that reminder. Try /remind @Alex "submit the form" tomorrow at 3pm or /remind me to stretch in 30 minutes.`
const identityMapUsage = `Identity mapping commands:
/map add @person username | Canonical Name | Meet Name
/map alias username | Another Meet Name
/map show username
/map list
/map remove username`

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

func (cm *CommandManager) handleStatus(ctx context.Context, client utils.MessageSender, info types.MessageInfo, args []string) error {
	status, err := cm.statusProvider.Status(ctx)
	if err != nil {
		return fmt.Errorf("get monitor status: %w", err)
	}

	lines := []string{
		"Bot: " + boolString(status.BotOnline, "online", "offline"),
		"WhatsApp: " + boolString(status.WhatsAppConnected, "connected", "disconnected"),
		"Monitor: " + monitorStateString(status.MonitorState),
		"Last heartbeat: " + heartbeatAgeString(cm.now(), status.MonitorState, status.LastHeartbeatAt),
		"Meeting: " + status.Meeting,
		fmt.Sprintf("Participants: %d", status.Participants),
		"Forwarding: " + boolString(status.ForwardingEnabled, "ON", "OFF"),
		"Extension: " + extensionVersionString(status.ExtensionVersion),
	}
	return utils.ReplyToMessage(ctx, client, info.Chat, strings.Join(lines, "\n"), info.ID, info.Sender)
}

func (cm *CommandManager) handleMeetMessage(
	_ *sql.DB,
	ctx context.Context,
	client utils.MessageSender,
	commandCtx CommandContext,
	args []string,
) error {
	info := commandCtx.Info
	name := strings.TrimSpace(info.PushName)
	if name == "" {
		name = commandCtx.SenderIdentity.User
	}
	message := fmt.Sprintf("%q said %q", name, args[0])
	if _, err := cm.meetMessenger.QueueMessage(ctx, message); err != nil {
		return fmt.Errorf("queue Meet message: %w", err)
	}
	return utils.ReplyToMessage(ctx, client, info.Chat, "Queued for Google Meet.", info.ID, info.Sender)
}

func (cm *CommandManager) handleIdentityMap(
	db *sql.DB,
	ctx context.Context,
	client utils.MessageSender,
	commandCtx CommandContext,
	args []string,
) error {
	info := commandCtx.Info
	raw := ""
	if len(args) > 0 {
		raw = strings.TrimSpace(args[0])
	}
	store := identity.NewStore(db)
	reply := func(text string) error {
		return utils.ReplyToMessage(ctx, client, info.Chat, text, info.ID, info.Sender)
	}

	switch {
	case raw == "":
		return reply(identityMapUsage)
	case strings.EqualFold(raw, "list"):
		people, err := store.List(ctx)
		if err != nil {
			return fmt.Errorf("list identity mappings: %w", err)
		}
		if len(people) == 0 {
			return reply("No identity mappings yet.")
		}
		lines := []string{fmt.Sprintf("Identity mappings (%d):", len(people))}
		for _, person := range people {
			lines = append(lines, fmt.Sprintf(
				"• @%s — %s — Meet: %s",
				person.Username,
				person.CanonicalName,
				strings.Join(person.MeetAliases, ", "),
			))
		}
		return reply(strings.Join(lines, "\n"))
	case strings.HasPrefix(strings.ToLower(raw), "add "):
		if commandCtx.MentionError != nil || len(commandCtx.Mentions) != 1 {
			return reply("Mention exactly one WhatsApp member.\n" + identityMapUsage)
		}
		parts := splitMapFields(raw[len("add "):], 3)
		if parts == nil {
			return reply(identityMapUsage)
		}
		firstFields := strings.Fields(parts[0])
		if len(firstFields) == 0 {
			return reply(identityMapUsage)
		}
		username := firstFields[len(firstFields)-1]
		person, err := store.Add(
			ctx,
			username,
			parts[1],
			commandCtx.Mentions[0].ToNonAD().String(),
			parts[2],
		)
		if err != nil {
			return reply(identityMapError(err))
		}
		return reply(fmt.Sprintf(
			"Mapped @%s to %s with Meet name %q.",
			person.Username,
			person.CanonicalName,
			person.MeetAliases[0],
		))
	case strings.HasPrefix(strings.ToLower(raw), "alias "):
		parts := splitMapFields(raw[len("alias "):], 2)
		if parts == nil {
			return reply(identityMapUsage)
		}
		if err := store.AddAlias(ctx, parts[0], parts[1]); err != nil {
			return reply(identityMapError(err))
		}
		return reply(fmt.Sprintf("Added Meet name %q to @%s.", parts[1], parts[0]))
	case strings.HasPrefix(strings.ToLower(raw), "show "):
		username := strings.TrimSpace(raw[len("show "):])
		person, err := store.Get(ctx, username)
		if err != nil {
			return reply(identityMapError(err))
		}
		return reply(fmt.Sprintf(
			"@%s\nName: %s\nWhatsApp: %s\nMeet: %s",
			person.Username,
			person.CanonicalName,
			person.WhatsAppJID,
			strings.Join(person.MeetAliases, ", "),
		))
	case strings.HasPrefix(strings.ToLower(raw), "remove "):
		username := strings.TrimSpace(raw[len("remove "):])
		if err := store.Delete(ctx, username); err != nil {
			return reply(identityMapError(err))
		}
		return reply("Removed @" + username + ".")
	default:
		return reply(identityMapUsage)
	}
}

func splitMapFields(value string, count int) []string {
	raw := strings.Split(value, "|")
	if len(raw) != count {
		return nil
	}
	result := make([]string, count)
	for index := range raw {
		result[index] = strings.TrimSpace(raw[index])
		if result[index] == "" {
			return nil
		}
	}
	return result
}

func identityMapError(err error) string {
	switch {
	case errors.Is(err, identity.ErrInvalidUsername):
		return "Username must be 2–32 lowercase letters, numbers, or underscores."
	case errors.Is(err, identity.ErrInvalidName):
		return "Canonical name is invalid."
	case errors.Is(err, identity.ErrInvalidJID):
		return "WhatsApp identity is invalid."
	case errors.Is(err, identity.ErrInvalidAlias):
		return "Meet name is invalid."
	case errors.Is(err, identity.ErrUsernameTaken):
		return "That username is already assigned."
	case errors.Is(err, identity.ErrWhatsAppTaken):
		return "That WhatsApp member already has a mapping."
	case errors.Is(err, identity.ErrAliasTaken):
		return "That Meet name is already assigned."
	case errors.Is(err, identity.ErrNotFound):
		return "No mapping exists for that username."
	default:
		return "The identity mapping could not be updated."
	}
}

func (cm *CommandManager) handleCall(ctx context.Context, client utils.MessageSender, info types.MessageInfo, args []string) error {
	roster, err := cm.callProvider.CallRoster(ctx)
	if err != nil {
		return fmt.Errorf("get Meet call roster: %w", err)
	}
	if roster.MonitorState != "online" {
		return utils.ReplyToMessage(
			ctx,
			client,
			info.Chat,
			"Meet monitor is offline, so I can't verify who is in the call.",
			info.ID,
			info.Sender,
		)
	}

	type mediaState struct {
		cam     bool
		unmuted bool
	}
	unique := make(map[string]mediaState, len(roster.Participants))
	for _, participant := range roster.Participants {
		if isMonitorParticipant(participant) {
			continue
		}
		name := strings.TrimSpace(participant.Name)
		if name != "" {
			current := unique[name]
			current.cam = current.cam || participant.Cam
			current.unmuted = current.unmuted || participant.Mic == "unmuted"
			unique[name] = current
		}
	}
	names := make([]string, 0, len(unique))
	for name := range unique {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return utils.ReplyToMessage(ctx, client, info.Chat, "The call is currently empty.", info.ID, info.Sender)
	}

	var reply strings.Builder
	fmt.Fprintf(&reply, "In the call (%d) (cam on, mic off):", len(names))
	for _, name := range names {
		media := unique[name]
		fmt.Fprintf(&reply, "\n• %s", name)
		var exceptions []string
		if !media.cam {
			exceptions = append(exceptions, "cam off")
		}
		if media.unmuted {
			exceptions = append(exceptions, "mic on")
		}
		if len(exceptions) > 0 {
			fmt.Fprintf(&reply, " — %s", strings.Join(exceptions, " · "))
		}
	}
	return utils.ReplyToMessage(ctx, client, info.Chat, reply.String(), info.ID, info.Sender)
}

type groupInfoProvider interface {
	GetGroupInfo(context.Context, types.JID) (*types.GroupInfo, error)
}

func (cm *CommandManager) handlePingCall(
	ctx context.Context,
	client utils.MessageSender,
	info types.MessageInfo,
	_ []string,
) error {
	roster, err := cm.callProvider.CallRoster(ctx)
	if err != nil {
		return fmt.Errorf("get Meet call roster: %w", err)
	}
	if roster.MonitorState != "online" {
		return utils.ReplyToMessage(
			ctx, client, info.Chat,
			"Meet monitor is offline, so I can't verify who is in the call.",
			info.ID, info.Sender,
		)
	}

	nameSet := make(map[string]struct{}, len(roster.Participants))
	for _, participant := range roster.Participants {
		if isMonitorParticipant(participant) {
			continue
		}
		if name := strings.TrimSpace(participant.Name); name != "" {
			nameSet[name] = struct{}{}
		}
	}
	names := make([]string, 0, len(nameSet))
	for name := range nameSet {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return utils.ReplyToMessage(
			ctx, client, info.Chat, "The call is currently empty.", info.ID, info.Sender,
		)
	}

	groups, ok := client.(groupInfoProvider)
	if !ok {
		return utils.ReplyToMessage(
			ctx, client, info.Chat, "Group mentions are unavailable.", info.ID, info.Sender,
		)
	}
	group, err := groups.GetGroupInfo(ctx, info.Chat)
	if err != nil {
		return fmt.Errorf("get WhatsApp group info: %w", err)
	}

	mentioned := make([]string, 0, len(group.Participants))
	labels := make([]string, 0, len(group.Participants))
	seen := make(map[string]struct{}, len(group.Participants))
	for _, participant := range group.Participants {
		mentionJID := participant.JID
		if mentionJID.User == "" {
			mentionJID = participant.PhoneNumber
		}
		mentionJID = mentionJID.ToNonAD()
		if mentionJID.User == "" {
			continue
		}
		key := mentionJID.String()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		mentioned = append(mentioned, key)
		labelJID := participant.PhoneNumber
		if labelJID.User == "" {
			labelJID = mentionJID
		}
		labels = append(labels, "@"+labelJID.User)
	}
	if len(mentioned) == 0 {
		return utils.ReplyToMessage(
			ctx, client, info.Chat, "No group members were available to mention.", info.ID, info.Sender,
		)
	}

	text := fmt.Sprintf(
		"📣 %s\nThese people are in the call (%d): %s",
		strings.Join(labels, " "),
		len(names),
		strings.Join(names, ", "),
	)
	_, err = client.SendMessage(ctx, info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: &text,
			ContextInfo: &waProto.ContextInfo{
				MentionedJID: mentioned,
			},
		},
	})
	return err
}

func isMonitorParticipant(participant meetmonitor.Participant) bool {
	return participant.Self ||
		strings.EqualFold(strings.TrimSpace(participant.Name), "ADHD SUPPORT GROUP")
}

func boolString(value bool, whenTrue, whenFalse string) string {
	if value {
		return whenTrue
	}
	return whenFalse
}

func monitorStateString(state string) string {
	if state == "never_seen" {
		return "never seen"
	}
	return state
}

func heartbeatAgeString(now time.Time, state string, lastHeartbeatAt *time.Time) string {
	if state == "never_seen" {
		return "never"
	}
	if lastHeartbeatAt == nil {
		return "unknown"
	}

	age := now.Sub(lastHeartbeatAt.UTC())
	if age < 0 {
		age = 0
	}
	if age < time.Minute {
		return fmt.Sprintf("%ds ago", int(age/time.Second))
	}
	if age < time.Hour {
		return fmt.Sprintf("%dm ago", int(age/time.Minute))
	}
	return fmt.Sprintf("%dh ago", int(age/time.Hour))
}

func extensionVersionString(version string) string {
	if strings.TrimSpace(version) == "" {
		return "unknown"
	}
	return version
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

	rawReminder := strings.TrimSpace(args[0])
	mentions := commandCtx.Mentions
	fields := strings.Fields(rawReminder)
	if len(mentions) == 0 && len(fields) >= 3 &&
		!strings.EqualFold(fields[0], "me") &&
		(strings.EqualFold(fields[1], "to") || strings.EqualFold(fields[1], "about")) {
		person, err := identity.NewStore(db).Get(ctx, strings.ToLower(fields[0]))
		if errors.Is(err, identity.ErrNotFound) {
			return utils.ReplyToMessage(
				ctx, client, info.Chat,
				"No mapping exists for @"+strings.ToLower(fields[0])+".",
				info.ID, info.Sender,
			)
		}
		if err != nil {
			return fmt.Errorf("resolve reminder username: %w", err)
		}
		target, err := types.ParseJID(person.WhatsAppJID)
		if err != nil {
			return fmt.Errorf("parse mapped reminder target: %w", err)
		}
		mentions = []types.JID{target.ToNonAD()}
		rawReminder = "@" + person.Username + rawReminder[len(fields[0]):]
	}

	request, err := utils.ParseReminder(
		"/remind "+rawReminder,
		mentions,
		commandCtx.SenderIdentity,
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
		info.Chat.String(), commandCtx.SenderIdentity.String(),
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
		id, info.Chat.String(), commandCtx.SenderIdentity.String(),
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
		commandCtx.SenderIdentity,
		info.PushName,
		time.Now(),
	)
	if err != nil {
		return utils.ReplyToMessage(ctx, client, info.Chat, "Failed to parse time or message.", info.ID, info.Sender)
	}

	var targetRaw string
	err = db.QueryRowContext(ctx,
		"SELECT to_id FROM reminders WHERE id = ? AND chat_id = ? AND requested_by = ?",
		id, info.Chat.String(), commandCtx.SenderIdentity.String(),
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
		reminderDeliveryMessage(request), request.RemindAt, id, info.Chat.String(), commandCtx.SenderIdentity.String(),
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
