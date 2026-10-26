package tone

import (
	"regexp"

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
