package meetmonitor

import "testing"

func TestFormatEventFormatsSupportedEvents(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		person    string
		text      string
		want      string
	}{
		{
			name:      "join",
			eventType: "join",
			person:    "Alex",
			want:      "🎥 Alex joined the call.",
		},
		{
			name:      "leave",
			eventType: "leave",
			person:    "Alex",
			want:      "🚪 Alex left the call.",
		},
		{
			name:      "mic muted",
			eventType: "mic_muted",
			person:    "Alex",
			want:      "🎙️ Alex muted their mic.",
		},
		{
			name:      "mic unmuted",
			eventType: "mic_unmuted",
			person:    "Alex",
			want:      "🎙️ Alex unmuted their mic.",
		},
		{
			name:      "camera on",
			eventType: "cam_on",
			person:    "Alex",
			want:      "📷 Alex turned their camera on.",
		},
		{
			name:      "camera off",
			eventType: "cam_off",
			person:    "Alex",
			want:      "📷 Alex turned their camera off.",
		},
		{
			name:      "chat message",
			eventType: "chat_message",
			person:    "Alex",
			text:      "Hello",
			want:      "💬 Alex said: Hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FormatEvent(tt.eventType, tt.person, tt.text)
			if err != nil {
				t.Fatalf("FormatEvent() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("FormatEvent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatEventRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		person    string
		text      string
	}{
		{name: "blank name", eventType: "join", person: " ", text: ""},
		{name: "non-chat text", eventType: "join", person: "Alex", text: "unexpected"},
		{name: "blank chat text", eventType: "chat_message", person: "Alex", text: " "},
		{name: "rename", eventType: "rename", person: "Alex", text: ""},
		{name: "unsupported type", eventType: "screen_shared", person: "Alex", text: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := FormatEvent(tt.eventType, tt.person, tt.text); err == nil {
				t.Fatal("FormatEvent() error = nil, want validation error")
			}
		})
	}
}
