package cmd

import (
	"context"
	"database/sql"
	"regexp"
	"time"
	"whatsappBot/meetmonitor"
	"whatsappBot/utils"

	"go.mau.fi/whatsmeow/types"
)

type CommandContext struct {
	Info           types.MessageInfo
	SenderIdentity types.JID
	Mentions       []types.JID
	MentionError   error
}

type CommandHandlerFunc func(db *sql.DB, ctx context.Context, client utils.MessageSender, commandCtx CommandContext, args []string) error
type Command struct {
	Name           string
	Description    string
	Regex          *regexp.Regexp
	Callback       CommandHandlerFunc
	IsAdminCommand bool
}

type StatusProvider interface {
	Status(context.Context) (meetmonitor.Status, error)
}

type MeetMessenger interface {
	QueueMessage(context.Context, string) (meetmonitor.Message, error)
}

type CallProvider interface {
	CallRoster(context.Context) (meetmonitor.CallRoster, error)
}

type ManagerOption func(*CommandManager)

type CommandManager struct {
	commands       map[string]Command // for lookup
	orderedList    []Command          // for /help order
	admins         map[string]struct{}
	statusProvider StatusProvider
	meetMessenger  MeetMessenger
	callProvider   CallProvider
	//logger *log.Logger
	client   utils.MessageSender
	shutdown context.CancelFunc
	now      func() time.Time
}

func WithStatusProvider(provider StatusProvider) ManagerOption {
	return func(manager *CommandManager) {
		manager.statusProvider = provider
	}
}

func WithMeetMessenger(messenger MeetMessenger) ManagerOption {
	return func(manager *CommandManager) {
		manager.meetMessenger = messenger
	}
}

func WithCallProvider(provider CallProvider) ManagerOption {
	return func(manager *CommandManager) {
		manager.callProvider = provider
	}
}

func WithClock(now func() time.Time) ManagerOption {
	return func(manager *CommandManager) {
		if now != nil {
			manager.now = now
		}
	}
}

func NewCommandManager(client utils.MessageSender, admins []string, shutdown context.CancelFunc, options ...ManagerOption) *CommandManager {
	adminMap := make(map[string]struct{})
	for _, adminNumber := range admins {
		adminMap[adminNumber] = struct{}{}
	}

	cm := &CommandManager{
		commands:    make(map[string]Command),
		orderedList: []Command{},
		admins:      adminMap,
		client:      client,
		shutdown:    shutdown,
		now:         time.Now,
	}
	for _, option := range options {
		option(cm)
	}

	// Regular user commands
	cm.RegisterCommand(Command{
		Name:           "/ping",
		Description:    "Replies with 'Pong!' to check if the bot is alive.",
		Regex:          regexp.MustCompile(`^/ping$`),
		Callback:       NoDB(cm.handlePing),
		IsAdminCommand: false,
	})

	cm.RegisterCommand(Command{
		Name:           "/echo",
		Description:    "Repeats the message you send after the command.",
		Regex:          regexp.MustCompile(`^/echo (.*)$`),
		Callback:       NoDB(cm.handleEcho),
		IsAdminCommand: false,
	})

	cm.RegisterCommand(Command{
		Name:           "/help",
		Description:    "Displays a list of all available commands.",
		Regex:          regexp.MustCompile(`^/help$`),
		Callback:       NoDB(cm.handleHelp),
		IsAdminCommand: false,
	})

	if cm.statusProvider != nil {
		cm.RegisterCommand(Command{
			Name:           "/status",
			Description:    "Shows bot, WhatsApp, and Meet monitor health.",
			Regex:          regexp.MustCompile(`^/status$`),
			Callback:       NoDB(cm.handleStatus),
			IsAdminCommand: false,
		})
	}

	if cm.callProvider != nil {
		cm.RegisterCommand(Command{
			Name:           "/call",
			Description:    "Lists everyone currently in the monitored Google Meet call.",
			Regex:          regexp.MustCompile(`^/call$`),
			Callback:       NoDB(cm.handleCall),
			IsAdminCommand: false,
		})
	}

	if cm.meetMessenger != nil {
		cm.RegisterCommand(Command{
			Name:           "/meet <message>",
			Description:    "Posts a message to the monitored Google Meet chat.",
			Regex:          regexp.MustCompile(`^/meet\s+(.+)$`),
			Callback:       cm.handleMeetMessage,
			IsAdminCommand: false,
		})
	}

	cm.RegisterCommand(Command{
		Name: "/remind",
		Description: `Creates a reminder for you or one mentioned recipient. Examples:
		- /remind @Alex "submit the form" tomorrow at 3pm
		- /remind tomorrow at 3pm @Alex "submit the form"
		- /remind @Alex in 20 minutes to join the call
		- /remind me to stretch in 30 minutes
		- /remind in 30 minutes "stretch"
		Use at most one mentioned recipient. Without a mention, the reminder is for you.
		Only the requester can list, update, or delete it. Ambiguous or malformed reminders are rejected.`,
		Regex:          regexp.MustCompile(`^/remind (.+)`),
		Callback:       cm.handleReminder,
		IsAdminCommand: false,
	})

	cm.RegisterCommand(Command{
		Name:           "/rlist",
		Description:    `Shows you your reminder list`,
		Regex:          regexp.MustCompile(`^/rlist$`),
		Callback:       cm.handleRemindList,
		IsAdminCommand: false,
	})

	cm.RegisterCommand(Command{
		Name:           "/rupdate <key>",
		Description:    `Updates a reminder given the id (find the id by listing your reminders with /remind list)`,
		Regex:          regexp.MustCompile(`^/rupdate (\d+) (.+)$`),
		Callback:       cm.handleRemindUpdate,
		IsAdminCommand: false,
	})

	cm.RegisterCommand(Command{
		Name:           "/rdelete <key>",
		Description:    `Deletes a reminder given the id (find the id by listing your reminders with /remind list)`,
		Regex:          regexp.MustCompile(`^/rdelete (\d+)$`),
		Callback:       cm.handleRemindDelete,
		IsAdminCommand: false,
	})

	// cm.RegisterCommand(Command{
	// 	Name:           "/tone",
	// 	Description:    `Lists all the supported tone identifiers. (Big list, avoid listing it a lot)`,
	// 	Regex:          regexp.MustCompile(`^/tone$`),
	// 	Callback:       NoDB(cm.handleToneList),
	// 	IsAdminCommand: false,
	// })

	// cm.RegisterCommand(Command{
	// 	Name:           "/mute",
	// 	Description:    `Bot doesn't reply (Note: auto replies will also be disabled)`,
	// 	Regex:          regexp.MustCompile(`^/mute$`),
	// 	Callback:       NoDB(cm.handleMute),
	// 	IsAdminCommand: true,
	// })

	// cm.RegisterCommand(Command{
	// 	Name:           "/unmute",
	// 	Description:    `Bot unmutted`,
	// 	Regex:          regexp.MustCompile(`^/tone$`),
	// 	Callback:       NoDB(cm.handleUnmute),
	// 	IsAdminCommand: true,
	// })

	// Admin-only commands
	cm.RegisterCommand(Command{
		Name:           "/die",
		Description:    "[Admin only command] Bot shuts down",
		Regex:          regexp.MustCompile(`^/die$`),
		Callback:       NoDB(cm.handleDie),
		IsAdminCommand: true,
	})

	return cm
}

func (cm *CommandManager) RegisterCommand(cmd Command) {
	cmd.Regex = regexp.MustCompile("(?i:" + cmd.Regex.String() + ")")
	cm.commands[cmd.Name] = cmd
	cm.orderedList = append(cm.orderedList, cmd)
}
