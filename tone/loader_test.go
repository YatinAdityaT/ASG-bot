package tone

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

type loaderTestSender struct{}

func (loaderTestSender) SendMessage(
	context.Context,
	types.JID,
	*waProto.Message,
	...whatsmeow.SendRequestExtra,
) (whatsmeow.SendResponse, error) {
	return whatsmeow.SendResponse{}, errors.New("not used")
}

func TestLoadToneCommandsFromJSON(t *testing.T) {
	path := writeToneFile(t, `[
		{
			"name": "/j",
			"description": "joking",
			"example": "sure /j",
			"regex": "(?i)/j"
		}
	]`)

	indicators, err := loadToneCommandsFromJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(indicators) != 1 {
		t.Fatalf("got %d indicators, want 1", len(indicators))
	}
	indicator := indicators[0]
	if indicator.Name != "/j" || indicator.Example != "sure /j" {
		t.Fatalf("unexpected indicator: %#v", indicator)
	}
	if !indicator.Regex.MatchString("SURE /J") {
		t.Fatal("compiled regex did not match")
	}
}

func TestLoadToneCommandsRejectsMalformedJSON(t *testing.T) {
	path := writeToneFile(t, `[{"name":`)

	if _, err := loadToneCommandsFromJSON(path); err == nil {
		t.Fatal("expected malformed JSON error")
	}
}

func TestLoadToneCommandsRejectsInvalidRegex(t *testing.T) {
	path := writeToneFile(t, `[{"name":"/bad","regex":"["}]`)

	if _, err := loadToneCommandsFromJSON(path); err == nil {
		t.Fatal("expected invalid regex error")
	}
}

func TestNewToneIManagerReturnsLoadError(t *testing.T) {
	path := writeToneFile(t, `[{"name":`)

	if _, err := newToneIManager(loaderTestSender{}, path); err == nil {
		t.Fatal("expected tone manager load error")
	}
}

func writeToneFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tones.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
