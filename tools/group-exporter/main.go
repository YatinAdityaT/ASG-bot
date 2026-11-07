package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mdp/qrterminal"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	_ "modernc.org/sqlite"
)

const (
	groupName   = "[Focused] Accountability subscribers"
	sessionDir  = ".group-exporter"
	sessionPath = sessionDir + "/session.db"
	outputPath  = "focused-accountability-subscribers.csv"
)

type member struct {
	name        string
	phoneNumber string
	jid         string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Export failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) (runErr error) {
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}

	database, err := sql.Open(
		"sqlite",
		"file:"+filepath.ToSlash(sessionPath)+"?_foreign_keys=on",
	)
	if err != nil {
		return fmt.Errorf("open isolated WhatsApp session: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	container := sqlstore.NewWithDB(database, "sqlite3", waLog.Noop)
	if err := container.Upgrade(ctx); err != nil {
		database.Close()
		return fmt.Errorf("initialize isolated WhatsApp session: %w", err)
	}
	defer func() {
		runErr = errors.Join(runErr, container.Close())
	}()

	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("load WhatsApp device: %w", err)
	}

	client := whatsmeow.NewClient(device, waLog.Noop)
	defer client.Disconnect()

	freshSession := device.ID == nil
	contactSync := make(chan error, 1)
	if err := connect(ctx, client, !freshSession, contactSync); err != nil {
		return err
	}
	if freshSession {
		fmt.Println("Waiting for saved contacts to finish syncing...")
		if err := waitForContactSync(ctx, contactSync); err != nil {
			return err
		}
	}

	group, err := findGroup(ctx, client)
	if err != nil {
		return err
	}

	rows := make([]member, 0, len(group.Participants))
	for _, participant := range group.Participants {
		rows = append(rows, memberFromParticipant(ctx, device, participant))
	}
	sort.Slice(rows, func(i, j int) bool {
		leftName := strings.ToLower(rows[i].name)
		rightName := strings.ToLower(rows[j].name)
		if leftName != rightName {
			return leftName < rightName
		}
		return rows[i].phoneNumber < rows[j].phoneNumber
	})

	if err := writeCSV(outputPath, rows); err != nil {
		return err
	}
	absoluteOutput, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("resolve output path: %w", err)
	}

	fmt.Printf("Group: %s\n", group.Name)
	fmt.Printf("Exported: %d members\n", len(rows))
	fmt.Printf("Saved: %s\n", absoluteOutput)
	return nil
}

func connect(
	ctx context.Context,
	client *whatsmeow.Client,
	hasSession bool,
	contactSync chan<- error,
) error {
	connected := make(chan struct{}, 1)
	failed := make(chan error, 1)
	client.AddEventHandler(func(rawEvent interface{}) {
		switch event := rawEvent.(type) {
		case *events.Connected:
			select {
			case connected <- struct{}{}:
			default:
			}
		case *events.LoggedOut:
			reportConnectionFailure(failed, errors.New("WhatsApp logged out"))
		case *events.ConnectFailure:
			reportConnectionFailure(failed, fmt.Errorf("WhatsApp connection failed: %s", event.Message))
		case *events.AppStateSyncComplete:
			if event.Name == appstate.WAPatchCriticalUnblockLow {
				reportConnectionFailure(contactSync, nil)
			}
		case *events.AppStateSyncError:
			if event.Name == appstate.WAPatchCriticalUnblockLow {
				reportConnectionFailure(contactSync, fmt.Errorf("sync saved contacts: %w", event.Error))
			}
		}
	})

	var qrChannel <-chan whatsmeow.QRChannelItem
	if !hasSession {
		var err error
		qrChannel, err = client.GetQRChannel(ctx)
		if err != nil {
			return fmt.Errorf("create QR login: %w", err)
		}
	}

	if err := client.Connect(); err != nil {
		return fmt.Errorf("connect to WhatsApp: %w", err)
	}
	if client.IsLoggedIn() {
		return nil
	}

	loginTimer := time.NewTimer(5 * time.Minute)
	defer loginTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-loginTimer.C:
			return errors.New("QR login timed out after 5 minutes")
		case err := <-failed:
			return err
		case <-connected:
			return nil
		case event, ok := <-qrChannel:
			if !ok {
				qrChannel = nil
				continue
			}
			if event.Event == "code" {
				fmt.Println("Scan this QR in WhatsApp: Linked devices → Link a device")
				qrterminal.GenerateHalfBlock(event.Code, qrterminal.L, os.Stdout)
			} else {
				fmt.Printf("QR status: %s\n", event.Event)
			}
		}
	}
}

func waitForContactSync(ctx context.Context, result <-chan error) error {
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("saved contacts did not finish syncing within 2 minutes")
	case err := <-result:
		return err
	}
}

func reportConnectionFailure(target chan<- error, err error) {
	select {
	case target <- err:
	default:
	}
}

func findGroup(ctx context.Context, client *whatsmeow.Client) (*types.GroupInfo, error) {
	groups, err := client.GetJoinedGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("list joined groups: %w", err)
	}

	var matches []*types.GroupInfo
	for _, group := range groups {
		if group != nil && group.Name == groupName {
			matches = append(matches, group)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("group %q was not found", groupName)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("found %d groups named %q; refusing to choose one", len(matches), groupName)
	}

	group, err := client.GetGroupInfo(ctx, matches[0].JID)
	if err != nil {
		return nil, fmt.Errorf("load group participants: %w", err)
	}
	return group, nil
}

func memberFromParticipant(ctx context.Context, device *store.Device, participant types.GroupParticipant) member {
	phoneJID := participant.PhoneNumber.ToNonAD()
	if phoneJID.IsEmpty() &&
		(participant.JID.Server == types.DefaultUserServer || participant.JID.Server == types.LegacyUserServer) {
		phoneJID = participant.JID.ToNonAD()
	}

	exportJID := phoneJID
	if exportJID.IsEmpty() {
		exportJID = participant.JID.ToNonAD()
	}

	return member{
		name:        resolveName(ctx, device, participant),
		phoneNumber: formatPhoneNumber(phoneJID),
		jid:         exportJID.String(),
	}
}

func resolveName(ctx context.Context, device *store.Device, participant types.GroupParticipant) string {
	candidates := []types.JID{
		participant.PhoneNumber.ToNonAD(),
		participant.JID.ToNonAD(),
		participant.LID.ToNonAD(),
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, jid := range candidates {
		if jid.IsEmpty() {
			continue
		}
		key := jid.String()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		contact, err := device.Contacts.GetContact(ctx, jid)
		if err != nil {
			continue
		}
		for _, value := range []string{
			contact.FullName,
			contact.FirstName,
			contact.PushName,
			contact.BusinessName,
		} {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return strings.TrimSpace(participant.DisplayName)
}

func formatPhoneNumber(jid types.JID) string {
	if jid.IsEmpty() || jid.User == "" {
		return ""
	}
	return "+" + jid.User
}

func writeCSV(path string, rows []member) (writeErr error) {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create CSV: %w", err)
	}
	defer func() {
		writeErr = errors.Join(writeErr, file.Close())
	}()

	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"name", "phone_number", "whatsapp_jid"}); err != nil {
		return fmt.Errorf("write CSV header: %w", err)
	}
	for _, row := range rows {
		if err := writer.Write([]string{row.name, row.phoneNumber, row.jid}); err != nil {
			return fmt.Errorf("write CSV row: %w", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush CSV: %w", err)
	}
	return nil
}
