package tone

import (
	"regexp"
	"sort"
	"strings"

	"whatsappBot/utils"
)

const defaultToneCommandsPath = "tone/tone_commands.json"

type toneIndicator struct {
	Name        string
	Description string
	Example     string
	Regex       *regexp.Regexp
}

type ToneIManager struct {
	toneIndicators map[string]toneIndicator
	client         utils.MessageSender
}

type ti struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Example     string `json:"example"`
	RegexStr    string `json:"regex"`
}

func NewToneIManager(client utils.MessageSender) (*ToneIManager, error) {
	return newToneIManager(client, defaultToneCommandsPath)
}

func newToneIManager(client utils.MessageSender, path string) (*ToneIManager, error) {
	tim := &ToneIManager{
		toneIndicators: make(map[string]toneIndicator),
		client:         client,
	}

	indicators, err := loadToneCommandsFromJSON(path)
	if err != nil {
		return nil, err
	}

	for _, ti := range indicators {
		tim.RegisterToneI(ti)
	}

	return tim, nil
}

func (tim *ToneIManager) RegisterToneI(ti toneIndicator) {
	tim.toneIndicators[ti.Name] = ti
}

func (tim *ToneIManager) ToneList() []string {
	lines := make([]string, 0, len(tim.toneIndicators))
	for _, indicator := range tim.toneIndicators {
		lines = append(lines, indicator.Name+" — "+toneDescription(indicator))
	}
	sort.Strings(lines)
	return lines
}

func toneDescription(indicator toneIndicator) string {
	description := strings.TrimPrefix(
		indicator.Description,
		"`"+indicator.Name+"` - ",
	)
	if index := strings.IndexByte(description, '\n'); index >= 0 {
		description = description[:index]
	}
	return description
}
