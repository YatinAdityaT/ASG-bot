package utils

import "testing"

func TestParseTimeRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{"", "not a reminder time"} {
		t.Run(input, func(t *testing.T) {
			if _, err := ParseTime(input); err == nil {
				t.Fatalf("ParseTime(%q) error = nil, want parse error", input)
			}
		})
	}
}

func TestExtractTimeAndMessageRejectsInvalidTime(t *testing.T) {
	if _, _, err := ExtractTimeAndMessage("remember this with no time"); err == nil {
		t.Fatal("ExtractTimeAndMessage() error = nil, want parse error")
	}
}
