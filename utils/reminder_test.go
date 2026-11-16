package utils

import (
	"strings"
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

func TestParseReminderRecurringSchedules(t *testing.T) {
	now := time.Date(2026, time.November, 16, 13, 0, 0, 0, time.Local)
	requester := types.NewJID("456", types.DefaultUserServer)
	tests := []struct {
		input     string
		wantBody  string
		wantAt    time.Time
		wantKind  string
		wantDay   time.Weekday
		wantLabel string
	}{
		{
			input:     `/remind me to eat every day at 2pm`,
			wantBody:  "eat",
			wantAt:    time.Date(2026, time.November, 16, 14, 0, 0, 0, time.Local),
			wantKind:  RecurrenceDaily,
			wantLabel: "every day",
		},
		{
			input:     `/remind me to plan weekdays at 9am`,
			wantBody:  "plan",
			wantAt:    time.Date(2026, time.November, 17, 9, 0, 0, 0, time.Local),
			wantKind:  RecurrenceWeekdays,
			wantLabel: "weekdays",
		},
		{
			input:     `/remind me to check in every Monday at 6pm`,
			wantBody:  "check in",
			wantAt:    time.Date(2026, time.November, 16, 18, 0, 0, 0, time.Local),
			wantKind:  RecurrenceWeekly,
			wantDay:   time.Monday,
			wantLabel: "every Monday",
		},
	}

	for _, test := range tests {
		got, err := ParseReminder(test.input, nil, requester, "Yatin", now)
		if err != nil {
			t.Fatalf("ParseReminder(%q) error = %v", test.input, err)
		}
		if got.Body != test.wantBody || !got.RemindAt.Equal(test.wantAt) ||
			got.Recurrence.Kind != test.wantKind ||
			got.Recurrence.Weekday != test.wantDay ||
			got.Recurrence.Label() != test.wantLabel {
			t.Fatalf("ParseReminder(%q) = %#v, want body=%q at=%v kind=%q day=%v label=%q",
				test.input, got, test.wantBody, test.wantAt, test.wantKind, test.wantDay, test.wantLabel)
		}
	}
}

func TestRecurrenceNextAfterSkipsMissedOccurrences(t *testing.T) {
	scheduled := time.Date(2026, time.November, 13, 9, 0, 0, 0, time.Local)
	after := time.Date(2026, time.November, 16, 10, 0, 0, 0, time.Local)
	next := (Recurrence{Kind: RecurrenceWeekdays}).NextAfter(scheduled, after)
	want := time.Date(2026, time.November, 17, 9, 0, 0, 0, time.Local)
	if !next.Equal(want) {
		t.Fatalf("NextAfter() = %v, want %v", next, want)
	}
}

func TestParseFutureTimeRejectsPastAndExtraText(t *testing.T) {
	now := time.Date(2026, time.November, 16, 14, 5, 0, 0, time.Local)
	got, err := ParseFutureTime("tomorrow at 3pm", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.November, 17, 15, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, input := range []string{"yesterday at 3pm", "tomorrow at 3pm please dance"} {
		if _, err := ParseFutureTime(input, now); err == nil {
			t.Fatalf("ParseFutureTime(%q) unexpectedly succeeded", input)
		}
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
		{name: "quoted body with unexplained text", input: `/remind "submit" tomorrow at 3pm and email Sam`},
		{name: "quoted body with multiple times", input: `/remind "submit" tomorrow at 3pm and in 2 hours`},
		{name: "quoted body with adjacent multiple times", input: `/remind "submit" tomorrow at 3pm in 2 hours`},
		{name: "unquoted body with multiple times", input: `/remind me to submit tomorrow at 3pm and in 2 hours`},
		{name: "unquoted body with adjacent multiple times", input: `/remind me to submit tomorrow at 3pm in 2 hours`},
		{name: "missing body", input: `/remind tomorrow at 3pm`},
		{name: "missing time", input: `/remind me to stretch`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got, err := ParseReminder(test.input, test.mentions, requester, "Yatin", now); err == nil {
				t.Fatalf("ParseReminder() = %#v, nil error; want error", got)
			}
		})
	}
}

func TestParseReminderRejectsEveryAdditionalVisibleMentionMarker(t *testing.T) {
	now := time.Date(2026, time.October, 26, 10, 0, 0, 0, time.Local)
	requester := types.NewJID("456", types.DefaultUserServer)
	target := types.NewJID("789", types.DefaultUserServer)

	for _, test := range []struct {
		name      string
		separator string
	}{
		{name: "comma", separator: ","},
		{name: "semicolon", separator: ";"},
		{name: "period", separator: "."},
		{name: "colon", separator: ":"},
		{name: "slash", separator: "/"},
		{name: "unicode punctuation", separator: "—"},
		{name: "no separator", separator: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			visibleText := `@Alex` + test.separator + `@Fake`
			if got := len(visibleMentionRanges(visibleText)); got != 2 {
				t.Fatalf("visibleMentionRanges(%q) count = %d, want 2", visibleText, got)
			}
			input := `/remind ` + visibleText + ` "submit" tomorrow at 3pm`
			if got, err := ParseReminder(input, []types.JID{target}, requester, "Yatin", now); err == nil {
				t.Fatalf("ParseReminder(%q) = %#v, nil error; want error", input, got)
			}
		})
	}
}

func TestParseReminderAllowsSingleMentionWithTrailingPunctuation(t *testing.T) {
	now := time.Date(2026, time.October, 26, 10, 0, 0, 0, time.Local)
	requester := types.NewJID("456", types.DefaultUserServer)
	target := types.NewJID("789", types.DefaultUserServer)

	for _, punctuation := range []string{",", ";", ".", ":", "/", "!", "?", "—"} {
		input := `/remind @Alex` + punctuation + ` "submit" tomorrow at 3pm`
		got, err := ParseReminder(input, []types.JID{target}, requester, "Yatin", now)
		if err != nil {
			t.Fatalf("ParseReminder(%q) error = %v", input, err)
		}
		if got.Target != target {
			t.Errorf("ParseReminder(%q).Target = %v, want %v", input, got.Target, target)
		}
		if got.Body != "submit" {
			t.Errorf("ParseReminder(%q).Body = %q, want submit", input, got.Body)
		}
	}
}

func TestParseReminderRemovesMultiwordAndHyphenatedMentionNames(t *testing.T) {
	now := time.Date(2026, time.October, 26, 10, 0, 0, 0, time.Local)
	requester := types.NewJID("456", types.DefaultUserServer)
	target := types.NewJID("789", types.DefaultUserServer)

	for _, input := range []string{
		`/remind @Mary Jane "submit" tomorrow at 3pm`,
		`/remind @Mary-Jane "submit" tomorrow at 3pm`,
		`/remind @Mary Jane in 20 minutes to join the call`,
		`/remind @Mary Jane to submit tomorrow at 3pm`,
	} {
		got, err := ParseReminder(input, []types.JID{target}, requester, "Yatin", now)
		if err != nil {
			t.Fatalf("ParseReminder(%q) error = %v", input, err)
		}
		want := "submit"
		if strings.Contains(input, "20 minutes") {
			want = "join the call"
		}
		if got.Body != want {
			t.Errorf("ParseReminder(%q).Body = %q", input, got.Body)
		}
	}
}

func TestParseReminderAllowsQuotedBodyWithIntentionalFillers(t *testing.T) {
	now := time.Date(2026, time.October, 26, 10, 0, 0, 0, time.Local)
	requester := types.NewJID("456", types.DefaultUserServer)

	got, err := ParseReminder(`/remind me to "submit" tomorrow at 3pm`, nil, requester, "Yatin", now)
	if err != nil {
		t.Fatalf("ParseReminder() error = %v", err)
	}
	if got.Body != "submit" {
		t.Fatalf("Body = %q, want submit", got.Body)
	}
}
