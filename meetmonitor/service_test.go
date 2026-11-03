package meetmonitor_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	appdb "whatsappBot/db"
	"whatsappBot/meetmonitor"
	"whatsappBot/outbound"
)

const (
	monitoredMeeting = "jpy-ymgz-tdo"
	testSession      = "01234567-89ab-cdef-0123-456789abcdef"
	secondSession    = "fedcba98-7654-3210-fedc-ba9876543210"
	targetChat       = "120363418911552290@g.us"
)

func TestServiceRejectsInvalidSnapshots(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	base := validSnapshot(now)
	manyParticipants := make([]meetmonitor.Participant, 501)
	for i := range manyParticipants {
		manyParticipants[i] = participant(fmt.Sprintf("p-%03d", i), "Person")
	}

	tests := []struct {
		name   string
		mutate func(*meetmonitor.Snapshot)
	}{
		{"protocol", func(s *meetmonitor.Snapshot) { s.ProtocolVersion = 2 }},
		{"meeting", func(s *meetmonitor.Snapshot) { s.Meeting = "other" }},
		{"blank session", func(s *meetmonitor.Snapshot) { s.SessionID = "" }},
		{"uppercase session", func(s *meetmonitor.Snapshot) { s.SessionID = strings.ToUpper(testSession) }},
		{"malformed session", func(s *meetmonitor.Snapshot) { s.SessionID = "0123456789ab-cdef-0123-456789abcdef" }},
		{"sequence", func(s *meetmonitor.Snapshot) { s.Sequence = 0 }},
		{"captured too old", func(s *meetmonitor.Snapshot) { s.CapturedAt = now.Add(-5*time.Minute - time.Nanosecond) }},
		{"captured too new", func(s *meetmonitor.Snapshot) { s.CapturedAt = now.Add(5*time.Minute + time.Nanosecond) }},
		{"blank extension", func(s *meetmonitor.Snapshot) { s.ExtensionVersion = " \t " }},
		{"long extension", func(s *meetmonitor.Snapshot) { s.ExtensionVersion = strings.Repeat("界", 33) }},
		{"too many participants", func(s *meetmonitor.Snapshot) { s.Participants = manyParticipants }},
		{"duplicate participant ID", func(s *meetmonitor.Snapshot) {
			s.Participants = []meetmonitor.Participant{participant("a", "Alice"), participant("a", "Another")}
		}},
		{"blank participant ID", func(s *meetmonitor.Snapshot) { s.Participants[0].ID = " \t " }},
		{"long participant ID", func(s *meetmonitor.Snapshot) { s.Participants[0].ID = strings.Repeat("界", 256) }},
		{"control in participant ID", func(s *meetmonitor.Snapshot) { s.Participants[0].ID = "a\nb" }},
		{"format in participant ID", func(s *meetmonitor.Snapshot) { s.Participants[0].ID = "a\u200db" }},
		{"blank sanitized name", func(s *meetmonitor.Snapshot) { s.Participants[0].Name = "\n\u200d\t" }},
		{"long name", func(s *meetmonitor.Snapshot) { s.Participants[0].Name = strings.Repeat("界", 201) }},
		{"invalid microphone", func(s *meetmonitor.Snapshot) { s.Participants[0].Mic = "unknown" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newServiceEnvironment(t, now)
			snapshot := cloneSnapshot(base)
			tt.mutate(&snapshot)

			_, err := env.service.Process(context.Background(), snapshot)
			if !errors.Is(err, meetmonitor.ErrInvalidSnapshot) {
				t.Fatalf("Process() error = %v, want ErrInvalidSnapshot", err)
			}
			if _, err := env.state.Load(context.Background(), monitoredMeeting); !errors.Is(err, meetmonitor.ErrNotSeen) {
				t.Fatalf("invalid snapshot changed state: %v", err)
			}
		})
	}
}

func TestServiceCanonicalizesBeforeHashAndPersistence(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.FixedZone("offset", 2*60*60))
	env := newServiceEnvironment(t, now)
	snapshot := validSnapshot(now)
	snapshot.Participants = []meetmonitor.Participant{
		{ID: "b", Name: " Bob ", Mic: "muted"},
		{ID: "a", Name: "  Alice\t \u200dSmith\n", Mic: "unmuted"},
	}

	result, err := env.service.Process(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "baseline" {
		t.Fatalf("status = %q, want baseline", result.Status)
	}
	state, err := env.state.Load(context.Background(), monitoredMeeting)
	if err != nil {
		t.Fatal(err)
	}
	wantParticipants := []meetmonitor.Participant{
		{ID: "a", Name: "Alice Smith", Mic: "unmuted"},
		{ID: "b", Name: "Bob", Mic: "muted"},
	}
	if !reflect.DeepEqual(state.Participants, wantParticipants) {
		t.Fatalf("participants = %#v, want %#v", state.Participants, wantParticipants)
	}
	normalized := snapshot
	normalized.CapturedAt = snapshot.CapturedAt.UTC()
	normalized.Participants = wantParticipants
	if state.SnapshotHash != expectedSnapshotHash(t, normalized) {
		t.Fatalf("snapshot hash = %q, want canonical complete request hash", state.SnapshotHash)
	}
}

func TestServiceFirstBaselineAndUnchangedHeartbeat(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	env := newServiceEnvironment(t, now)
	snapshot := validSnapshot(now)
	connectedCalls := 0
	env.service = meetmonitor.NewService(env.state, env.jobs, monitoredMeeting, func() bool {
		connectedCalls++
		return true
	}, func() time.Time { return env.now })

	first, err := env.service.Process(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	assertResult(t, first, "baseline", 1, now, true, true, 0)
	if countJobs(t, env.database) != 0 {
		t.Fatal("first baseline created a job")
	}

	env.now = now.Add(30 * time.Second)
	snapshot.Sequence = 2
	snapshot.CapturedAt = env.now
	second, err := env.service.Process(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	assertResult(t, second, "no_change", 2, env.now, true, true, 0)
	state, err := env.state.Load(context.Background(), monitoredMeeting)
	if err != nil {
		t.Fatal(err)
	}
	if !state.LastHeartbeatAt.Equal(env.now) || connectedCalls != 2 {
		t.Fatalf("heartbeat = %v, connected calls = %d", state.LastHeartbeatAt, connectedCalls)
	}
}

func TestServiceQueuesDeterministicOrderedChanges(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	env := newServiceEnvironment(t, now)
	baseline := validSnapshot(now)
	baseline.Participants = []meetmonitor.Participant{
		{ID: "a", Name: "Alice", Mic: "muted"},
		{ID: "b", Name: "Bob", Mic: "muted"},
		{ID: "d", Name: "David", Cam: false, Mic: "unmuted"},
		{ID: "e", Name: "Eve", Cam: true, Mic: "muted"},
	}
	if _, err := env.service.Process(context.Background(), baseline); err != nil {
		t.Fatal(err)
	}

	env.now = now.Add(10 * time.Second)
	changed := validSnapshot(env.now)
	changed.Sequence = 2
	changed.Participants = []meetmonitor.Participant{
		{ID: "e", Name: "Eve", Cam: false, Mic: "unmuted"},
		{ID: "d", Name: "David", Cam: true, Mic: "muted"},
		{ID: "c", Name: "Carol", Cam: true, Mic: "unmuted"},
		{ID: "b", Name: "Bob", Mic: "muted"},
	}
	result, err := env.service.Process(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	assertResult(t, result, "changed", 2, env.now, true, false, 6)

	got := loadJobIdentityAndText(t, env.database)
	changes := []struct {
		participantID string
		changeType    string
		text          string
	}{
		{"a", "leave", "🚪 Alice left the call."},
		{"c", "join", "🎥 Carol joined the call."},
		{"d", "cam_on", "📷 David turned their camera on."},
		{"d", "mic_muted", "🎙️ David muted their mic."},
		{"e", "cam_off", "📷 Eve turned their camera off."},
		{"e", "mic_unmuted", "🎙️ Eve unmuted their mic."},
	}
	if len(got) != len(changes) {
		t.Fatalf("jobs = %#v, want %d", got, len(changes))
	}
	for i, change := range changes {
		wantKey := expectedJobKey(t, monitoredMeeting, testSession, 2, change.participantID, change.changeType)
		if got[i].key != wantKey || got[i].text != change.text {
			t.Fatalf("job %d = %#v, want key %q text %q", i, got[i], wantKey, change.text)
		}
	}
}

func TestServiceMetadataOnlyUpdatePersistsWithoutJob(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	env := newServiceEnvironment(t, now)
	if _, err := env.service.Process(context.Background(), validSnapshot(now)); err != nil {
		t.Fatal(err)
	}

	env.now = now.Add(time.Second)
	update := validSnapshot(env.now)
	update.Sequence = 2
	update.Participants[0].Name = "Alicia"
	update.Participants[0].Self = true
	update.Participants[0].Host = true
	result, err := env.service.Process(context.Background(), update)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "no_change" || result.QueuedChanges != 0 || countJobs(t, env.database) != 0 {
		t.Fatalf("metadata result = %#v, jobs = %d", result, countJobs(t, env.database))
	}
	state, err := env.state.Load(context.Background(), monitoredMeeting)
	if err != nil {
		t.Fatal(err)
	}
	if state.Participants[0].Name != "Alicia" || !state.Participants[0].Self || !state.Participants[0].Host {
		t.Fatalf("metadata not persisted: %#v", state.Participants[0])
	}
}

func TestServiceBaselineTransitions(t *testing.T) {
	tests := []struct {
		name       string
		prepare    func(*serviceEnvironment, meetmonitor.Snapshot)
		next       func(*serviceEnvironment, meetmonitor.Snapshot) meetmonitor.Snapshot
		wantStatus string
	}{
		{
			name:    "new session",
			prepare: processBaseline,
			next: func(env *serviceEnvironment, snapshot meetmonitor.Snapshot) meetmonitor.Snapshot {
				env.now = env.now.Add(time.Second)
				snapshot.SessionID = secondSession
				snapshot.CapturedAt = env.now
				snapshot.Participants = append(snapshot.Participants, participant("b", "Bob"))
				return snapshot
			},
			wantStatus: "baseline",
		},
		{
			name:    "offline recovery",
			prepare: processBaseline,
			next: func(env *serviceEnvironment, snapshot meetmonitor.Snapshot) meetmonitor.Snapshot {
				env.now = env.now.Add(90*time.Second + time.Nanosecond)
				snapshot.Sequence = 2
				snapshot.CapturedAt = env.now
				snapshot.Participants = append(snapshot.Participants, participant("b", "Bob"))
				return snapshot
			},
			wantStatus: "baseline",
		},
		{
			name: "forwarding off",
			prepare: func(env *serviceEnvironment, snapshot meetmonitor.Snapshot) {
				processBaseline(env, snapshot)
			},
			next: func(env *serviceEnvironment, snapshot meetmonitor.Snapshot) meetmonitor.Snapshot {
				env.now = env.now.Add(time.Second)
				snapshot.Sequence = 2
				snapshot.CapturedAt = env.now
				snapshot.ForwardingEnabled = false
				snapshot.Participants = append(snapshot.Participants, participant("b", "Bob"))
				return snapshot
			},
			wantStatus: "no_change",
		},
		{
			name: "off to on",
			prepare: func(env *serviceEnvironment, snapshot meetmonitor.Snapshot) {
				snapshot.ForwardingEnabled = false
				processBaseline(env, snapshot)
			},
			next: func(env *serviceEnvironment, snapshot meetmonitor.Snapshot) meetmonitor.Snapshot {
				env.now = env.now.Add(time.Second)
				snapshot.Sequence = 2
				snapshot.CapturedAt = env.now
				snapshot.ForwardingEnabled = true
				snapshot.Participants = append(snapshot.Participants, participant("b", "Bob"))
				return snapshot
			},
			wantStatus: "baseline",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
			env := newServiceEnvironment(t, now)
			snapshot := validSnapshot(now)
			tt.prepare(env, snapshot)
			next := tt.next(env, snapshot)

			result, err := env.service.Process(context.Background(), next)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tt.wantStatus || result.QueuedChanges != 0 {
				t.Fatalf("result = %#v, want %q with no jobs", result, tt.wantStatus)
			}
			if countJobs(t, env.database) != 0 {
				t.Fatal("silent transition created jobs")
			}
			state, err := env.state.Load(context.Background(), monitoredMeeting)
			if err != nil {
				t.Fatal(err)
			}
			if state.Sequence != next.Sequence || !reflect.DeepEqual(state.Participants, canonicalParticipants(next.Participants)) {
				t.Fatalf("state = %#v, want accepted transition", state)
			}
		})
	}
}

func TestServiceRestartReconcilesPersistedOnlineState(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	env := newServiceEnvironment(t, now)
	processBaseline(env, validSnapshot(now))

	env.now = now.Add(time.Second)
	env.service = meetmonitor.NewService(env.state, env.jobs, monitoredMeeting, nil, func() time.Time { return env.now })
	next := validSnapshot(env.now)
	next.Sequence = 2
	next.Participants = append(next.Participants, participant("b", "Bob"))

	result, err := env.service.Process(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "changed" || result.QueuedChanges != 1 {
		t.Fatalf("post-restart result = %#v, want one changed event", result)
	}
	jobs := loadJobIdentityAndText(t, env.database)
	wantKey := expectedJobKey(t, monitoredMeeting, testSession, 2, "b", "join")
	if len(jobs) != 1 || jobs[0].key != wantKey || jobs[0].text != "🎥 Bob joined the call." {
		t.Fatalf("post-restart jobs = %#v, want deterministic Bob join job %q", jobs, wantKey)
	}
	state, err := env.state.Load(context.Background(), monitoredMeeting)
	if err != nil {
		t.Fatal(err)
	}
	if state.Sequence != 2 || len(state.Participants) != 2 {
		t.Fatalf("post-restart state = %#v, want accepted snapshot", state)
	}
}

func TestServiceSequenceClassification(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	t.Run("stale does not refresh heartbeat", func(t *testing.T) {
		env := newServiceEnvironment(t, now)
		accepted := validSnapshot(now)
		accepted.Sequence = 2
		processBaseline(env, accepted)
		env.now = now.Add(91 * time.Second)
		stale := validSnapshot(env.now)

		result, err := env.service.Process(context.Background(), stale)
		if err != nil {
			t.Fatal(err)
		}
		assertResult(t, result, "stale", 2, env.now, false, false, 0)
		state, err := env.state.Load(context.Background(), monitoredMeeting)
		if err != nil {
			t.Fatal(err)
		}
		if !state.LastHeartbeatAt.Equal(now) || state.Sequence != 2 {
			t.Fatalf("stale changed state: %#v", state)
		}
	})

	t.Run("replay refreshes heartbeat", func(t *testing.T) {
		env := newServiceEnvironment(t, now)
		snapshot := validSnapshot(now)
		processBaseline(env, snapshot)
		env.now = now.Add(30 * time.Second)

		result, err := env.service.Process(context.Background(), snapshot)
		if err != nil {
			t.Fatal(err)
		}
		assertResult(t, result, "replay", 1, env.now, true, false, 0)
		state, err := env.state.Load(context.Background(), monitoredMeeting)
		if err != nil {
			t.Fatal(err)
		}
		if !state.LastHeartbeatAt.Equal(env.now) {
			t.Fatalf("replay heartbeat = %v, want %v", state.LastHeartbeatAt, env.now)
		}
	})

	t.Run("same sequence different hash conflicts", func(t *testing.T) {
		env := newServiceEnvironment(t, now)
		snapshot := validSnapshot(now)
		processBaseline(env, snapshot)
		conflict := snapshot
		conflict.ExtensionVersion = "different"

		_, err := env.service.Process(context.Background(), conflict)
		if !errors.Is(err, meetmonitor.ErrSequenceConflict) {
			t.Fatalf("Process() error = %v, want ErrSequenceConflict", err)
		}
		state, err := env.state.Load(context.Background(), monitoredMeeting)
		if err != nil {
			t.Fatal(err)
		}
		if state.ExtensionVersion != snapshot.ExtensionVersion || !state.LastHeartbeatAt.Equal(now) {
			t.Fatalf("conflict changed state: %#v", state)
		}
	})
}

func TestServiceOnlineBoundary(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		age        time.Duration
		wantStatus string
		wantJobs   int
	}{
		{"exactly 90 seconds remains online", 90 * time.Second, "changed", 1},
		{"greater than 90 seconds baselines", 90*time.Second + time.Nanosecond, "baseline", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newServiceEnvironment(t, now)
			processBaseline(env, validSnapshot(now))
			env.now = now.Add(tt.age)
			next := validSnapshot(env.now)
			next.Sequence = 2
			next.Participants = append(next.Participants, participant("b", "Bob"))

			result, err := env.service.Process(context.Background(), next)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tt.wantStatus || result.QueuedChanges != tt.wantJobs {
				t.Fatalf("result = %#v, want status %q jobs %d", result, tt.wantStatus, tt.wantJobs)
			}
		})
	}
}

func TestServicePartialFailureKeepsStateAndRetryIsIdempotent(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	env := newServiceEnvironment(t, now)
	failing := &failOnceCreator{base: env.jobs, failAt: 2}
	env.service = meetmonitor.NewService(env.state, failing, monitoredMeeting, nil, func() time.Time { return env.now })
	processBaseline(env, validSnapshot(now))

	env.now = now.Add(time.Second)
	changed := validSnapshot(env.now)
	changed.Sequence = 2
	changed.Participants = append(changed.Participants,
		participant("b", "Bob"),
		participant("c", "Carol"),
	)
	_, err := env.service.Process(context.Background(), changed)
	if err == nil || err.Error() != "forced create failure" {
		t.Fatalf("Process() error = %v, want forced create failure", err)
	}
	state, err := env.state.Load(context.Background(), monitoredMeeting)
	if err != nil {
		t.Fatal(err)
	}
	if state.Sequence != 1 || len(state.Participants) != 1 || countJobs(t, env.database) != 1 {
		t.Fatalf("partial failure state = %#v, jobs = %d", state, countJobs(t, env.database))
	}

	result, err := env.service.Process(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "changed" || result.QueuedChanges != 2 {
		t.Fatalf("retry result = %#v", result)
	}
	if countJobs(t, env.database) != 2 {
		t.Fatalf("retry jobs = %d, want 2 unique jobs", countJobs(t, env.database))
	}
	state, err = env.state.Load(context.Background(), monitoredMeeting)
	if err != nil {
		t.Fatal(err)
	}
	if state.Sequence != 2 {
		t.Fatalf("retry state sequence = %d, want 2", state.Sequence)
	}
}

func TestServiceConcurrentSubmissionsAcceptHighestSequence(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	env := newServiceEnvironment(t, now)
	processBaseline(env, validSnapshot(now))
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, sequence := range []int64{2, 3} {
		snapshot := validSnapshot(now.Add(time.Second))
		snapshot.Sequence = sequence
		snapshot.ExtensionVersion = strconv.FormatInt(sequence, 10)
		go func() {
			<-start
			_, err := env.service.Process(context.Background(), snapshot)
			results <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	state, err := env.state.Load(context.Background(), monitoredMeeting)
	if err != nil {
		t.Fatal(err)
	}
	if state.Sequence != 3 || state.ExtensionVersion != "3" {
		t.Fatalf("concurrent final state = %#v, want sequence 3", state)
	}
}

func TestStatusNeverSeenOnlineAndOffline(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	env := newServiceEnvironment(t, now)
	env.service = meetmonitor.NewService(env.state, env.jobs, monitoredMeeting, func() bool { return true }, func() time.Time { return env.now })

	neverSeen, err := env.service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !neverSeen.BotOnline || !neverSeen.WhatsAppConnected || neverSeen.MonitorState != "never_seen" ||
		neverSeen.Meeting != monitoredMeeting || neverSeen.LastHeartbeatAt != nil {
		t.Fatalf("never-seen status = %#v", neverSeen)
	}

	snapshot := validSnapshot(now)
	snapshot.ExtensionVersion = "2.4.0"
	snapshot.ForwardingEnabled = false
	snapshot.Participants = append(snapshot.Participants, participant("b", "Bob"))
	processBaseline(env, snapshot)
	online, err := env.service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if online.MonitorState != "online" || online.LastHeartbeatAt == nil || !online.LastHeartbeatAt.Equal(now) ||
		online.Participants != 2 || online.ForwardingEnabled || online.ExtensionVersion != "2.4.0" {
		t.Fatalf("online status = %#v", online)
	}

	env.now = now.Add(90*time.Second + time.Nanosecond)
	offline, err := env.service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if offline.MonitorState != "offline" || offline.LastHeartbeatAt == nil || !offline.LastHeartbeatAt.Equal(now) {
		t.Fatalf("offline status = %#v", offline)
	}
}

type serviceEnvironment struct {
	database *sql.DB
	state    *meetmonitor.Store
	jobs     *outbound.Store
	service  *meetmonitor.Service
	now      time.Time
}

func newServiceEnvironment(t *testing.T, now time.Time) *serviceEnvironment {
	t.Helper()
	_, database, err := appdb.SetupDB(context.Background(), t.TempDir()+"/service.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	env := &serviceEnvironment{database: database, state: meetmonitor.NewStore(database), now: now}
	var messageID int
	env.jobs = outbound.NewStore(database, targetChat,
		outbound.WithClock(func() time.Time { return env.now }),
		outbound.WithMessageIDGenerator(func() (string, error) {
			messageID++
			return fmt.Sprintf("%020X", messageID), nil
		}),
	)
	env.service = meetmonitor.NewService(env.state, env.jobs, monitoredMeeting, nil, func() time.Time { return env.now })
	return env
}

func validSnapshot(now time.Time) meetmonitor.Snapshot {
	return meetmonitor.Snapshot{
		ProtocolVersion:   1,
		Meeting:           monitoredMeeting,
		SessionID:         testSession,
		Sequence:          1,
		CapturedAt:        now,
		ExtensionVersion:  "1.0.0",
		ForwardingEnabled: true,
		Participants:      []meetmonitor.Participant{participant("a", "Alice")},
	}
}

func participant(id, name string) meetmonitor.Participant {
	return meetmonitor.Participant{ID: id, Name: name, Mic: "muted"}
}

func cloneSnapshot(snapshot meetmonitor.Snapshot) meetmonitor.Snapshot {
	clone := snapshot
	clone.Participants = append([]meetmonitor.Participant(nil), snapshot.Participants...)
	return clone
}

func canonicalParticipants(participants []meetmonitor.Participant) []meetmonitor.Participant {
	copy := append([]meetmonitor.Participant(nil), participants...)
	for i := range copy {
		copy[i].Name = strings.Join(strings.Fields(copy[i].Name), " ")
	}
	return copy
}

func processBaseline(env *serviceEnvironment, snapshot meetmonitor.Snapshot) {
	if _, err := env.service.Process(context.Background(), snapshot); err != nil {
		panic(err)
	}
}

func assertResult(t *testing.T, got meetmonitor.Result, status string, sequence int64, serverTime time.Time, online, connected bool, queued int) {
	t.Helper()
	if got.Status != status || got.AcceptedSequence != sequence || !got.ServerTime.Equal(serverTime.UTC()) ||
		got.MonitorOnline != online || got.WhatsAppConnected != connected || got.QueuedChanges != queued {
		t.Fatalf("result = %#v, want status=%q sequence=%d server=%v online=%v connected=%v queued=%d",
			got, status, sequence, serverTime.UTC(), online, connected, queued)
	}
}

func expectedSnapshotHash(t *testing.T, snapshot meetmonitor.Snapshot) string {
	t.Helper()
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func expectedJobKey(t *testing.T, meeting, session string, sequence int64, participantID, changeType string) string {
	t.Helper()
	encoded, err := json.Marshal([]string{meeting, session, strconv.FormatInt(sequence, 10), participantID, changeType})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	return "meet-state-" + hex.EncodeToString(sum[:])
}

type jobIdentityAndText struct {
	key  string
	text string
}

func loadJobIdentityAndText(t *testing.T, database *sql.DB) []jobIdentityAndText {
	t.Helper()
	rows, err := database.Query("SELECT idempotency_key, message FROM outbound_jobs ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var jobs []jobIdentityAndText
	for rows.Next() {
		var job jobIdentityAndText
		if err := rows.Scan(&job.key, &job.text); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return jobs
}

func countJobs(t *testing.T, database *sql.DB) int {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM outbound_jobs").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

type failOnceCreator struct {
	mu     sync.Mutex
	base   meetmonitor.JobCreator
	calls  int
	failAt int
	failed bool
}

func (f *failOnceCreator) Create(ctx context.Context, request outbound.CreateRequest) (*outbound.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls == f.failAt && !f.failed {
		f.failed = true
		return nil, errors.New("forced create failure")
	}
	return f.base.Create(ctx, request)
}
