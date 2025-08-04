package utils

import (
	"fmt"
	"time"

	"github.com/olebedev/when"
	"github.com/olebedev/when/rules/en"
)

func ParseTime(input string) (time.Time, error) {
	w := when.New(nil)
	w.Add(en.All...)
	result, err := w.Parse(input, time.Now())
	if err != nil || result == nil {
		return time.Time{}, fmt.Errorf("could not parse: %s", input)
	}

	return result.Time, nil
}

func ExtractTimeAndMessage(input string) (time.Time, string, error) {
	t, err := ParseTime(input)

	if err != nil {
		return time.Time{}, input, err
	}

	if t.IsZero() {
		return t, "", fmt.Errorf("invalid input")
	}

	return t, input, nil
}
