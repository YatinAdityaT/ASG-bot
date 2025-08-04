package tone

import (
	"fmt"
	"regexp"

	"go.mau.fi/whatsmeow"
)

type toneIndicator struct {
	Name        string
	Description string
	Example     string
	Regex       *regexp.Regexp
}

type ToneIManager struct {
	toneIndicators map[string]toneIndicator
	//logger *log.Logger
	client *whatsmeow.Client
}

type ti struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Example     string `json:"example"`
	RegexStr    string `json:"regex"`
}

func NewToneIManager(client *whatsmeow.Client) *ToneIManager {
	tim := &ToneIManager{
		toneIndicators: make(map[string]toneIndicator),
		client:         client,
	}

	indicators, err := loadToneCommandsFromJSON("tone/tone_commands.json")
	if err != nil {
		fmt.Printf("Failed to load tone indicators: %v\n", err)
		return tim
	}

	for _, ti := range indicators {
		tim.RegisterToneI(ti)
	}

	return tim
}

func (tim *ToneIManager) RegisterToneI(ti toneIndicator) {
	tim.toneIndicators[ti.Name] = ti
}
