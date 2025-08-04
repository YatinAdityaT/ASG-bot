package tone

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
)

// Load tone indicators from JSON file and return []toneIndicator
func loadToneCommandsFromJSON(path string) ([]toneIndicator, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("error opening tone commands file: %w", err)
	}
	defer file.Close()

	var raw []ti
	if err := json.NewDecoder(file).Decode(&raw); err != nil {
		return nil, fmt.Errorf("error decoding JSON: %w", err)
	}

	var indicators []toneIndicator
	for _, entry := range raw {
		re, err := regexp.Compile(entry.RegexStr)
		if err != nil {
			return nil, fmt.Errorf("invalid regex '%s': %w", entry.RegexStr, err)
		}
		info := fmt.Sprintf("`%s` - %s\nExample: %s", entry.Name, entry.Description, entry.Example)
		indicators = append(indicators, toneIndicator{
			Name:        entry.Name,
			Description: info,
			Example:     entry.Example,
			Regex:       re,
		})
	}
	return indicators, nil
}
