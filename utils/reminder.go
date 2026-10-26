package utils

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/olebedev/when"
	"github.com/olebedev/when/rules/en"
	"go.mau.fi/whatsmeow/types"
)

// ReminderRequest is the trusted, parsed form of a reminder command.
type ReminderRequest struct {
	Target        types.JID
	Requester     types.JID
	RequesterName string
	Body          string
	RemindAt      time.Time
}

var visibleMentionPattern = regexp.MustCompile(`(?:^|\s)@\S+`)

// ParseReminder parses reminder text using trusted WhatsApp mention metadata.
func ParseReminder(
	input string,
	mentions []types.JID,
	requester types.JID,
	requesterName string,
	now time.Time,
) (ReminderRequest, error) {
	var request ReminderRequest

	commandText := strings.TrimSpace(input)
	fields := strings.Fields(commandText)
	if len(fields) == 0 || !strings.EqualFold(fields[0], "/remind") {
		return request, fmt.Errorf("invalid reminder command")
	}
	commandText = strings.TrimSpace(commandText[len(fields[0]):])
	if commandText == "" {
		return request, fmt.Errorf("reminder is missing time and body")
	}

	if len(mentions) > 1 {
		return request, fmt.Errorf("reminders support one mentioned participant")
	}
	visibleMentions := visibleMentionPattern.FindAllString(commandText, -1)
	if len(visibleMentions) > 1 || (len(visibleMentions) == 1) != (len(mentions) == 1) {
		return request, fmt.Errorf("visible mentions must match WhatsApp mention metadata")
	}

	request.Requester = requester.ToNonAD()
	request.Target = request.Requester
	if len(mentions) == 1 {
		request.Target = mentions[0].ToNonAD()
	}
	request.RequesterName = strings.TrimSpace(requesterName)
	if request.RequesterName == "" {
		request.RequesterName = request.Requester.User
	}

	quoteCount := strings.Count(commandText, `"`)
	if quoteCount%2 != 0 {
		return ReminderRequest{}, fmt.Errorf("reminder body has malformed quotes")
	}
	if quoteCount > 2 {
		return ReminderRequest{}, fmt.Errorf("reminder has multiple quoted bodies")
	}

	parser := when.New(nil)
	parser.Add(en.All...)
	result, err := parser.Parse(commandText, now)
	if err != nil {
		return ReminderRequest{}, fmt.Errorf("parse reminder time: %w", err)
	}
	if result == nil || result.Time.IsZero() {
		return ReminderRequest{}, fmt.Errorf("reminder is missing a time")
	}
	request.RemindAt = result.Time

	var body string
	if quoteCount == 2 {
		quoteStart := strings.Index(commandText, `"`)
		quoteEnd := strings.LastIndex(commandText, `"`)
		timeEnd := result.Index + len(result.Text)
		if result.Index < quoteEnd && timeEnd > quoteStart {
			return ReminderRequest{}, fmt.Errorf("reminder time cannot be the quoted body")
		}
		body = commandText[quoteStart+1 : quoteEnd]
	} else {
		timeEnd := result.Index + len(result.Text)
		body = commandText[:result.Index] + " " + commandText[timeEnd:]
		body = visibleMentionPattern.ReplaceAllString(body, " ")
	}

	body = trimLeadingReminderFillers(body)
	if body == "" {
		return ReminderRequest{}, fmt.Errorf("reminder is missing a body")
	}
	request.Body = body
	return request, nil
}

func trimLeadingReminderFillers(body string) string {
	body = strings.TrimSpace(body)
	for {
		fields := strings.Fields(body)
		if len(fields) == 0 {
			return ""
		}
		switch strings.ToLower(fields[0]) {
		case "me", "to", "about":
			body = strings.TrimSpace(strings.TrimPrefix(body, fields[0]))
		default:
			return body
		}
	}
}
