package utils

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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

var reminderClauseBoundaryPattern = regexp.MustCompile(`(?i)\band\b`)
var relativeTimeStartPattern = regexp.MustCompile(`(?i)\bin\s+(?:an?|one|\d+)\b`)

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
	visibleMentions := visibleMentionRanges(commandText)
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
	timeScanText := commandText
	for _, location := range visibleMentions {
		timeScanText = blankReminderRange(timeScanText, location[0], location[1])
	}
	if quoteCount == 2 {
		quoteStart := strings.Index(timeScanText, `"`)
		quoteEnd := strings.LastIndex(timeScanText, `"`)
		timeScanText = blankReminderRange(timeScanText, quoteStart, quoteEnd+1)
	}
	if countReminderTimeClauses(parser, timeScanText, now) > 1 {
		return ReminderRequest{}, fmt.Errorf("reminder has multiple times")
	}
	result, err := parser.Parse(commandText, now)
	if err != nil {
		return ReminderRequest{}, fmt.Errorf("parse reminder time: %w", err)
	}
	if result == nil || result.Time.IsZero() {
		return ReminderRequest{}, fmt.Errorf("reminder is missing a time")
	}
	request.RemindAt = result.Time

	var body string
	residual := blankReminderRange(commandText, result.Index, result.Index+len(result.Text))
	for _, location := range visibleMentions {
		residual = blankReminderRange(residual, location[0], location[1])
	}
	if quoteCount == 2 {
		quoteStart := strings.Index(commandText, `"`)
		quoteEnd := strings.LastIndex(commandText, `"`)
		timeEnd := result.Index + len(result.Text)
		if result.Index < quoteEnd && timeEnd > quoteStart {
			return ReminderRequest{}, fmt.Errorf("reminder time cannot be the quoted body")
		}
		body = commandText[quoteStart+1 : quoteEnd]
		residual = blankReminderRange(residual, quoteStart, quoteEnd+1)
	} else {
		body = residual
	}

	if secondTime, parseErr := parseAdditionalReminderTime(parser, residual, now); parseErr != nil {
		return ReminderRequest{}, fmt.Errorf("parse additional reminder time: %w", parseErr)
	} else if secondTime != nil {
		return ReminderRequest{}, fmt.Errorf("reminder has multiple times")
	}
	if quoteCount == 2 && hasMeaningfulReminderText(residual) {
		return ReminderRequest{}, fmt.Errorf("reminder has unexplained text outside quoted body")
	}

	body = trimLeadingReminderFillers(body)
	if body == "" {
		return ReminderRequest{}, fmt.Errorf("reminder is missing a body")
	}
	request.Body = body
	return request, nil
}

func visibleMentionRanges(text string) [][2]int {
	var ranges [][2]int
	for index, marker := range text {
		if marker != '@' {
			continue
		}

		end := index + 1
		for end < len(text) {
			current, size := utf8.DecodeRuneInString(text[end:])
			if current == '@' || unicode.IsSpace(current) || unicode.IsPunct(current) || unicode.IsSymbol(current) {
				break
			}
			end += size
		}
		ranges = append(ranges, [2]int{index, end})
	}
	return ranges
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

func blankReminderRange(text string, start, end int) string {
	blanked := []byte(text)
	for index := start; index < end; index++ {
		blanked[index] = ' '
	}
	return string(blanked)
}

func parseAdditionalReminderTime(parser *when.Parser, text string, now time.Time) (*when.Result, error) {
	result, err := parser.Parse(text, now)
	if err != nil || result != nil {
		return result, err
	}
	for _, boundary := range reminderClauseBoundaryPattern.FindAllStringIndex(text, -1) {
		result, err = parser.Parse(strings.TrimSpace(text[boundary[1]:]), now)
		if err != nil || result != nil {
			return result, err
		}
	}
	return nil, nil
}

func countReminderTimeClauses(parser *when.Parser, text string, now time.Time) int {
	count := 0
	for _, clause := range reminderClauseBoundaryPattern.Split(text, -1) {
		result, err := parser.Parse(clause, now)
		if err == nil && result != nil {
			count++
		}
	}
	if count > 1 {
		return count
	}
	for _, location := range relativeTimeStartPattern.FindAllStringIndex(text, -1) {
		if location[0] == 0 {
			continue
		}
		before, beforeErr := parser.Parse(text[:location[0]], now)
		after, afterErr := parser.Parse(text[location[0]:], now)
		if beforeErr == nil && afterErr == nil && before != nil && after != nil {
			return 2
		}
	}
	return count
}

func hasMeaningfulReminderText(text string) bool {
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	for _, word := range words {
		switch strings.ToLower(word) {
		case "me", "to", "about", "and", "at", "on", "for":
			continue
		default:
			return true
		}
	}
	return false
}
