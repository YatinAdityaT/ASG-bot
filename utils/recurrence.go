package utils

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	RecurrenceNone     = "none"
	RecurrenceDaily    = "daily"
	RecurrenceWeekdays = "weekdays"
	RecurrenceWeekly   = "weekly"
)

type Recurrence struct {
	Kind    string
	Weekday time.Weekday
}

func (recurrence Recurrence) IsRecurring() bool {
	return recurrence.Kind != "" && recurrence.Kind != RecurrenceNone
}

func (recurrence Recurrence) Label() string {
	switch recurrence.Kind {
	case RecurrenceDaily:
		return "every day"
	case RecurrenceWeekdays:
		return "weekdays"
	case RecurrenceWeekly:
		return "every " + recurrence.Weekday.String()
	default:
		return ""
	}
}

func (recurrence Recurrence) NextAfter(anchor, after time.Time) time.Time {
	if !recurrence.IsRecurring() {
		return time.Time{}
	}
	location := anchor.Location()
	localAfter := after.In(location)
	candidate := time.Date(
		localAfter.Year(),
		localAfter.Month(),
		localAfter.Day(),
		anchor.Hour(),
		anchor.Minute(),
		anchor.Second(),
		anchor.Nanosecond(),
		location,
	)

	switch recurrence.Kind {
	case RecurrenceDaily:
		if !candidate.After(localAfter) {
			candidate = candidate.AddDate(0, 0, 1)
		}
	case RecurrenceWeekdays:
		for !candidate.After(localAfter) ||
			candidate.Weekday() == time.Saturday ||
			candidate.Weekday() == time.Sunday {
			candidate = candidate.AddDate(0, 0, 1)
		}
	case RecurrenceWeekly:
		for candidate.Weekday() != recurrence.Weekday || !candidate.After(localAfter) {
			candidate = candidate.AddDate(0, 0, 1)
		}
	default:
		return time.Time{}
	}
	return candidate
}

var recurrenceClock = `(?:[01]?\d|2[0-3])(?::[0-5]\d)?(?:\s*(?:a\.?m\.?|p\.?m\.?))?`
var recurrenceName = `(?:every\s+day|daily|weekdays|every\s+weekday|every\s+(?:monday|tuesday|wednesday|thursday|friday|saturday|sunday))`
var recurrenceBeforeClockPattern = regexp.MustCompile(
	`(?i)\b(` + recurrenceName + `)\s+at\s+(` + recurrenceClock + `)\s*$`,
)
var recurrenceAfterClockPattern = regexp.MustCompile(
	`(?i)\bat\s+(` + recurrenceClock + `)\s+(` + recurrenceName + `)\s*$`,
)

func extractRecurrenceClause(text string) (string, Recurrence, error) {
	var (
		location []int
		name     string
		clock    string
	)
	if match := recurrenceBeforeClockPattern.FindStringSubmatchIndex(text); match != nil {
		location = match[:2]
		name = text[match[2]:match[3]]
		clock = text[match[4]:match[5]]
	} else if match := recurrenceAfterClockPattern.FindStringSubmatchIndex(text); match != nil {
		location = match[:2]
		clock = text[match[2]:match[3]]
		name = text[match[4]:match[5]]
	} else {
		return text, Recurrence{Kind: RecurrenceNone}, nil
	}

	recurrence, err := parseRecurrenceName(name)
	if err != nil {
		return "", Recurrence{}, err
	}
	rewritten := strings.TrimSpace(text[:location[0]] + " at " + clock)
	return rewritten, recurrence, nil
}

func parseRecurrenceName(value string) (Recurrence, error) {
	normalized := strings.ToLower(strings.Join(strings.Fields(value), " "))
	switch normalized {
	case "every day", "daily":
		return Recurrence{Kind: RecurrenceDaily}, nil
	case "weekday", "weekdays", "every weekday":
		return Recurrence{Kind: RecurrenceWeekdays}, nil
	}
	if strings.HasPrefix(normalized, "every ") {
		weekdayName := strings.TrimSpace(strings.TrimPrefix(normalized, "every "))
		for weekday := time.Sunday; weekday <= time.Saturday; weekday++ {
			if strings.EqualFold(weekday.String(), weekdayName) {
				return Recurrence{Kind: RecurrenceWeekly, Weekday: weekday}, nil
			}
		}
	}
	return Recurrence{}, fmt.Errorf("invalid recurrence")
}
