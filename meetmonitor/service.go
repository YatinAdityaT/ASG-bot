package meetmonitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"whatsappBot/outbound"
)

const (
	monitorOfflineAfter = 90 * time.Second
	captureTimeWindow   = 5 * time.Minute
)

var canonicalSessionID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type Service struct {
	mu              sync.Mutex
	store           StateStore
	jobs            JobCreator
	meeting         string
	connected       func() bool
	now             func() time.Time
	baselinePending bool
}

func NewService(store StateStore, jobs JobCreator, meeting string, connected func() bool, now func() time.Time) *Service {
	if connected == nil {
		connected = func() bool { return false }
	}
	if now == nil {
		now = time.Now
	}
	return &Service{
		store:           store,
		jobs:            jobs,
		meeting:         meeting,
		connected:       connected,
		now:             now,
		baselinePending: true,
	}
}

func (s *Service) Process(ctx context.Context, snapshot Snapshot) (Result, error) {
	now := s.now().UTC()
	normalized, snapshotHash, err := normalizeSnapshot(snapshot, s.meeting, now)
	if err != nil {
		return Result{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	previous, err := s.store.Load(ctx, s.meeting)
	if errors.Is(err, ErrNotSeen) {
		return s.accept(ctx, normalized, snapshotHash, now, "baseline", nil)
	}
	if err != nil {
		return Result{}, err
	}

	if normalized.SessionID != previous.SessionID {
		return s.accept(ctx, normalized, snapshotHash, now, "baseline", nil)
	}
	if normalized.Sequence < previous.Sequence {
		return s.result("stale", previous.Sequence, now, isOnline(now, previous.LastHeartbeatAt), 0), nil
	}
	if normalized.Sequence == previous.Sequence {
		if snapshotHash != previous.SnapshotHash {
			return Result{}, ErrSequenceConflict
		}
		if s.baselinePending {
			return s.accept(ctx, normalized, snapshotHash, now, "baseline", nil)
		}
		if !isOnline(now, previous.LastHeartbeatAt) {
			return s.accept(ctx, normalized, snapshotHash, now, "baseline", nil)
		}
		return s.accept(ctx, normalized, snapshotHash, now, "replay", nil)
	}

	if s.baselinePending ||
		!isOnline(now, previous.LastHeartbeatAt) ||
		(!previous.ForwardingEnabled && normalized.ForwardingEnabled) {
		return s.accept(ctx, normalized, snapshotHash, now, "baseline", nil)
	}
	if !normalized.ForwardingEnabled {
		return s.accept(ctx, normalized, snapshotHash, now, "no_change", nil)
	}

	changes := diffParticipants(previous.Participants, normalized.Participants)
	if len(changes) == 0 {
		return s.accept(ctx, normalized, snapshotHash, now, "no_change", nil)
	}
	return s.accept(ctx, normalized, snapshotHash, now, "changed", changes)
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	now := s.now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.store.Load(ctx, s.meeting)
	if errors.Is(err, ErrNotSeen) {
		return Status{
			BotOnline:         true,
			WhatsAppConnected: s.connected(),
			MonitorState:      "never_seen",
			Meeting:           s.meeting,
		}, nil
	}
	if err != nil {
		return Status{}, err
	}

	monitorState := "offline"
	if isOnline(now, state.LastHeartbeatAt) {
		monitorState = "online"
	}
	lastHeartbeatAt := state.LastHeartbeatAt
	return Status{
		BotOnline:         true,
		WhatsAppConnected: s.connected(),
		MonitorState:      monitorState,
		LastHeartbeatAt:   &lastHeartbeatAt,
		Meeting:           state.Meeting,
		Participants:      len(state.Participants),
		ForwardingEnabled: state.ForwardingEnabled,
		ExtensionVersion:  state.ExtensionVersion,
	}, nil
}

func (s *Service) accept(
	ctx context.Context,
	snapshot Snapshot,
	snapshotHash string,
	now time.Time,
	status string,
	changes []participantChange,
) (Result, error) {
	for _, change := range changes {
		text, err := FormatEvent(change.changeType, change.name, "")
		if err != nil {
			return Result{}, fmt.Errorf("format Meet monitor event: %w", err)
		}
		_, err = s.jobs.Create(ctx, outbound.CreateRequest{
			IdempotencyKey: jobIdempotencyKey(
				snapshot.Meeting,
				snapshot.SessionID,
				snapshot.Sequence,
				change.participantID,
				change.changeType,
			),
			Text: text,
		})
		if err != nil {
			return Result{}, err
		}
	}

	state := PersistedState{
		Meeting:           snapshot.Meeting,
		SessionID:         snapshot.SessionID,
		Sequence:          snapshot.Sequence,
		Participants:      snapshot.Participants,
		SnapshotHash:      snapshotHash,
		ExtensionVersion:  snapshot.ExtensionVersion,
		ForwardingEnabled: snapshot.ForwardingEnabled,
		LastHeartbeatAt:   now,
		UpdatedAt:         now,
	}
	if err := s.store.Save(ctx, state); err != nil {
		return Result{}, err
	}
	s.baselinePending = false
	return s.result(status, snapshot.Sequence, now, true, len(changes)), nil
}

func (s *Service) result(status string, sequence int64, now time.Time, online bool, queued int) Result {
	return Result{
		Status:            status,
		AcceptedSequence:  sequence,
		ServerTime:        now,
		MonitorOnline:     online,
		WhatsAppConnected: s.connected(),
		QueuedChanges:     queued,
	}
}

func normalizeSnapshot(snapshot Snapshot, meeting string, now time.Time) (Snapshot, string, error) {
	if snapshot.ProtocolVersion != 1 ||
		snapshot.Meeting != meeting ||
		!canonicalSessionID.MatchString(snapshot.SessionID) ||
		snapshot.Sequence < 1 ||
		snapshot.CapturedAt.Before(now.Add(-captureTimeWindow)) ||
		snapshot.CapturedAt.After(now.Add(captureTimeWindow)) ||
		strings.TrimSpace(snapshot.ExtensionVersion) == "" ||
		utf8.RuneCountInString(snapshot.ExtensionVersion) > 32 ||
		len(snapshot.Participants) > 500 {
		return Snapshot{}, "", ErrInvalidSnapshot
	}

	normalized := snapshot
	normalized.CapturedAt = snapshot.CapturedAt.UTC()
	normalized.Participants = append([]Participant(nil), snapshot.Participants...)
	seenIDs := make(map[string]struct{}, len(normalized.Participants))
	for i := range normalized.Participants {
		participant := &normalized.Participants[i]
		if strings.TrimSpace(participant.ID) == "" ||
			utf8.RuneCountInString(participant.ID) > 255 ||
			hasControlOrFormat(participant.ID) {
			return Snapshot{}, "", ErrInvalidSnapshot
		}
		if _, exists := seenIDs[participant.ID]; exists {
			return Snapshot{}, "", ErrInvalidSnapshot
		}
		seenIDs[participant.ID] = struct{}{}

		participant.Name = sanitizeMeetName(participant.Name)
		if participant.Name == "" || utf8.RuneCountInString(participant.Name) > 200 {
			return Snapshot{}, "", ErrInvalidSnapshot
		}
		if participant.Mic != "muted" && participant.Mic != "unmuted" {
			return Snapshot{}, "", ErrInvalidSnapshot
		}
	}
	sort.Slice(normalized.Participants, func(i, j int) bool {
		return normalized.Participants[i].ID < normalized.Participants[j].ID
	})

	encoded, err := json.Marshal(normalized)
	if err != nil {
		return Snapshot{}, "", fmt.Errorf("encode normalized Meet snapshot identity: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return normalized, hex.EncodeToString(sum[:]), nil
}

func hasControlOrFormat(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return true
		}
	}
	return false
}

func sanitizeMeetName(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case unicode.IsControl(r):
			builder.WriteByte(' ')
		case unicode.Is(unicode.Cf, r):
			continue
		default:
			builder.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

func isOnline(now, heartbeat time.Time) bool {
	return !now.After(heartbeat.Add(monitorOfflineAfter))
}

type participantChange struct {
	participantID string
	changeType    string
	name          string
}

var changeTypeOrder = map[string]int{
	"join":        0,
	"leave":       1,
	"cam_on":      2,
	"cam_off":     3,
	"mic_muted":   4,
	"mic_unmuted": 5,
}

func diffParticipants(previous, current []Participant) []participantChange {
	oldByID := make(map[string]Participant, len(previous))
	newByID := make(map[string]Participant, len(current))
	for _, participant := range previous {
		oldByID[participant.ID] = participant
	}
	for _, participant := range current {
		newByID[participant.ID] = participant
	}

	changes := make([]participantChange, 0)
	for id, oldParticipant := range oldByID {
		newParticipant, exists := newByID[id]
		if !exists {
			changes = append(changes, participantChange{id, "leave", oldParticipant.Name})
			continue
		}
		if oldParticipant.Cam != newParticipant.Cam {
			changeType := "cam_off"
			if newParticipant.Cam {
				changeType = "cam_on"
			}
			changes = append(changes, participantChange{id, changeType, newParticipant.Name})
		}
		if oldParticipant.Mic != newParticipant.Mic {
			changeType := "mic_unmuted"
			if newParticipant.Mic == "muted" {
				changeType = "mic_muted"
			}
			changes = append(changes, participantChange{id, changeType, newParticipant.Name})
		}
	}
	for id, participant := range newByID {
		if _, exists := oldByID[id]; !exists {
			changes = append(changes, participantChange{id, "join", participant.Name})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].participantID != changes[j].participantID {
			return changes[i].participantID < changes[j].participantID
		}
		return changeTypeOrder[changes[i].changeType] < changeTypeOrder[changes[j].changeType]
	})
	return changes
}

func jobIdempotencyKey(meeting, session string, sequence int64, participantID, changeType string) string {
	encoded, _ := json.Marshal([]string{
		meeting,
		session,
		strconv.FormatInt(sequence, 10),
		participantID,
		changeType,
	})
	sum := sha256.Sum256(encoded)
	return "meet-state-" + hex.EncodeToString(sum[:])
}
