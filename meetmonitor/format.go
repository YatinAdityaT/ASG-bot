package meetmonitor

import (
	"errors"
	"strings"
)

func FormatEvent(eventType, name, text string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", errors.New("blank name")
	}

	switch eventType {
	case "join":
		if strings.TrimSpace(text) != "" {
			return "", errors.New("unexpected text")
		}
		return "🎥 " + name + " joined the call.", nil
	case "leave":
		if strings.TrimSpace(text) != "" {
			return "", errors.New("unexpected text")
		}
		return "🚪 " + name + " left the call.", nil
	case "mic_muted":
		if strings.TrimSpace(text) != "" {
			return "", errors.New("unexpected text")
		}
		return "🎙️ " + name + " muted their mic.", nil
	case "mic_unmuted":
		if strings.TrimSpace(text) != "" {
			return "", errors.New("unexpected text")
		}
		return "🎙️ " + name + " unmuted their mic.", nil
	case "cam_on":
		if strings.TrimSpace(text) != "" {
			return "", errors.New("unexpected text")
		}
		return "📷 " + name + " turned their camera on.", nil
	case "cam_off":
		if strings.TrimSpace(text) != "" {
			return "", errors.New("unexpected text")
		}
		return "📷 " + name + " turned their camera off.", nil
	case "chat_message":
		if strings.TrimSpace(text) == "" {
			return "", errors.New("blank chat text")
		}
		return "💬 " + name + " said: " + text, nil
	case "rename":
		return "", errors.New("rename unsupported")
	default:
		return "", errors.New("unsupported event type")
	}
}
