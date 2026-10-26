package utils

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

func TestParseReminderSupportedSyntaxes(t *testing.T) {
	now := time.Date(2026, time.October, 26, 10, 0, 0, 0, time.Local)
	requester := types.NewJID("456", types.DefaultUserServer)
	target := types.NewJID("789", types.DefaultUserServer)

	tests := []struct {
		name       string
		input      string
		mentions   []types.JID
		wantTarget types.JID
		wantBody   string
		wantAt     time.Time
	}{
		{
			name:       "mention before quoted body",
			input:      `/remind @Alex "submit the form" tomorrow at 3pm`,
			mentions:   []types.JID{target},
			wantTarget: target,
			wantBody:   "submit the form",
			wantAt:     time.Date(2026, time.October, 27, 15, 0, 0, 0, time.Local),
		},
		{
			name:       "time before mention",
			input:      `/remind tomorrow at 3pm @Alex "submit the form"`,
			mentions:   []types.JID{target},
			wantTarget: target,
			wantBody:   "submit the form",
			wantAt:     time.Date(2026, time.October, 27, 15, 0, 0, 0, time.Local),
		},
		{
			name:       "unquoted mentioned body",
			input:      `/remind @Alex in 20 minutes to join the call`,
			mentions:   []types.JID{target},
			wantTarget: target,
			wantBody:   "join the call",
			wantAt:     time.Date(2026, time.October, 26, 10, 20, 0, 0, time.Local),
		},
		{
			name:       "explicit self",
			input:      `/remind me to stretch in 30 minutes`,
			wantTarget: requester,
			wantBody:   "stretch",
			wantAt:     time.Date(2026, time.October, 26, 10, 30, 0, 0, time.Local),
		},
		{
			name:       "implicit self quoted",
			input:      `/remind in 30 minutes "stretch"`,
			wantTarget: requester,
			wantBody:   "stretch",
			wantAt:     time.Date(2026, time.October, 26, 10, 30, 0, 0, time.Local),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseReminder(test.input, test.mentions, requester, "Yatin", now)
			if err != nil {
				t.Fatalf("ParseReminder() error = %v", err)
			}
			if got.Target != test.wantTarget.ToNonAD() {
				t.Errorf("Target = %v, want %v", got.Target, test.wantTarget.ToNonAD())
			}
			if got.Requester != requester.ToNonAD() {
				t.Errorf("Requester = %v, want %v", got.Requester, requester.ToNonAD())
			}
			if got.RequesterName != "Yatin" {
				t.Errorf("RequesterName = %q, want Yatin", got.RequesterName)
			}
			if got.Body != test.wantBody {
				t.Errorf("Body = %q, want %q", got.Body, test.wantBody)
			}
			if !got.RemindAt.Equal(test.wantAt) {
				t.Errorf("RemindAt = %v, want %v", got.RemindAt, test.wantAt)
			}
		})
	}
}

func TestParseReminderPreservesCapitalizationAndNormalizesJIDs(t *testing.T) {
	now := time.Date(2026, time.October, 26, 10, 0, 0, 0, time.Local)
	requester := types.NewADJID("456", 1, 2)
	target := types.NewADJID("789", 3, 4)

	got, err := ParseReminder(
		`/REMIND @Alex "Review CUDA Docs" tomorrow at 3pm`,
		[]types.JID{target},
		requester,
		"",
		now,
	)
	if err != nil {
		t.Fatalf("ParseReminder() error = %v", err)
	}
	if got.Body != "Review CUDA Docs" {
		t.Errorf("Body = %q, want preserved capitalization", got.Body)
	}
	if got.Target != target.ToNonAD() || got.Requester != requester.ToNonAD() {
		t.Errorf("JIDs = (%v, %v), want normalized (%v, %v)", got.Target, got.Requester, target.ToNonAD(), requester.ToNonAD())
	}
	if got.RequesterName != requester.User {
		t.Errorf("RequesterName = %q, want fallback %q", got.RequesterName, requester.User)
	}
}

func TestParseReminderRemovesLeadingFiller(t *testing.T) {
	now := time.Date(2026, time.October, 26, 10, 0, 0, 0, time.Local)
	requester := types.NewJID("456", types.DefaultUserServer)

	for _, test := range []struct {
		input string
		want  string
	}{
		{input: `/remind tomorrow at 3pm me about Submit Expenses`, want: "Submit Expenses"},
		{input: `/remind tomorrow at 3pm to Submit Expenses`, want: "Submit Expenses"},
		{input: `/remind tomorrow at 3pm about Submit Expenses`, want: "Submit Expenses"},
		{input: `/remind tomorrow at 3pm "to Submit Expenses"`, want: "Submit Expenses"},
		{input: `/remind tomorrow at 3pm "about Submit Expenses"`, want: "Submit Expenses"},
	} {
		got, err := ParseReminder(test.input, nil, requester, "Yatin", now)
		if err != nil {
			t.Fatalf("ParseReminder(%q) error = %v", test.input, err)
		}
		if got.Body != test.want {
			t.Errorf("ParseReminder(%q).Body = %q, want %q", test.input, got.Body, test.want)
		}
	}
}

func TestParseReminderRejectsInvalidInput(t *testing.T) {
	now := time.Date(2026, time.October, 26, 10, 0, 0, 0, time.Local)
	requester := types.NewJID("456", types.DefaultUserServer)
	target := types.NewJID("789", types.DefaultUserServer)

	tests := []struct {
		name     string
		input    string
		mentions []types.JID
	}{
		{name: "fake visible mention", input: `/remind @Alex "submit the form" tomorrow at 3pm`},
		{name: "multiple mentions", input: `/remind @Alex @Sam "submit the form" tomorrow at 3pm`, mentions: []types.JID{target, types.NewJID("999", types.DefaultUserServer)}},
		{name: "malformed quote", input: `/remind "submit the form tomorrow at 3pm`},
		{name: "multiple quoted bodies", input: `/remind "submit" "the form" tomorrow at 3pm`},
		{name: "missing body", input: `/remind tomorrow at 3pm`},
		{name: "missing time", input: `/remind me to stretch`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseReminder(test.input, test.mentions, requester, "Yatin", now); err == nil {
				t.Fatal("ParseReminder() error = nil, want error")
			}
		})
	}
}
