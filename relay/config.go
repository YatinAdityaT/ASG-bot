package relay

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

const (
	RoleWindows = "windows"
	RoleTermux  = "termux"
)

//go:embed config.json
var embeddedConfig embed.FS

type sealedSecret struct {
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type fileConfig struct {
	Version int          `json:"version"`
	Broker  string       `json:"broker"`
	Windows sealedSecret `json:"windows"`
	Termux  sealedSecret `json:"termux"`
}

type Settings struct {
	broker string
	secret []byte
}

type Announcement struct {
	URL       string    `json:"url"`
	Published time.Time `json:"published"`
}

func Load(role, apiToken string) (Settings, error) {
	if role != RoleWindows && role != RoleTermux {
		return Settings{}, fmt.Errorf("unsupported relay role %q", role)
	}
	if strings.TrimSpace(apiToken) == "" {
		return Settings{}, errors.New("relay API token is empty")
	}
	raw, err := embeddedConfig.ReadFile("config.json")
	if err != nil {
		return Settings{}, fmt.Errorf("read relay config: %w", err)
	}
	var cfg fileConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Settings{}, fmt.Errorf("decode relay config: %w", err)
	}
	broker, err := url.Parse(cfg.Broker)
	if err != nil || broker.Scheme != "https" || broker.Host != "ntfy.sh" ||
		broker.User != nil || broker.RawQuery != "" || broker.Fragment != "" {
		return Settings{}, errors.New("relay broker must be https://ntfy.sh")
	}
	sealed := cfg.Windows
	if role == RoleTermux {
		sealed = cfg.Termux
	}
	secret, err := openSecret(role, apiToken, sealed)
	if err != nil {
		return Settings{}, fmt.Errorf("decrypt relay config for %s: %w", role, err)
	}
	if len(secret) != 32 {
		return Settings{}, errors.New("relay secret has invalid length")
	}
	return Settings{
		broker: strings.TrimRight(cfg.Broker, "/"),
		secret: secret,
	}, nil
}

func openSecret(role, apiToken string, sealed sealedSecret) ([]byte, error) {
	nonce, err := base64.StdEncoding.DecodeString(sealed.Nonce)
	if err != nil {
		return nil, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(sealed.Ciphertext)
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256(append(
		[]byte("asg-remote-relay-key-v1\x00"),
		[]byte(apiToken)...,
	))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, []byte(role))
}

func (settings Settings) DiscoveryTopic() string {
	mac := hmac.New(sha256.New, settings.secret)
	_, _ = mac.Write([]byte("asg-remote-relay-discovery-v1"))
	return "asg-" + hex.EncodeToString(mac.Sum(nil))[:40]
}

func (settings Settings) GatewayStatusTopic() string {
	mac := hmac.New(sha256.New, settings.secret)
	_, _ = mac.Write([]byte("asg-remote-relay-gateway-status-v1"))
	return "asg-" + hex.EncodeToString(mac.Sum(nil))[:40]
}

func (settings Settings) BearerToken() string {
	mac := hmac.New(sha256.New, settings.secret)
	_, _ = mac.Write([]byte("asg-remote-relay-bearer-v1"))
	return hex.EncodeToString(mac.Sum(nil))
}

func (settings Settings) PublishURL() string {
	return settings.broker + "/" + settings.DiscoveryTopic()
}

func (settings Settings) GatewayStatusURL() string {
	return settings.broker + "/" + settings.GatewayStatusTopic()
}

func (settings Settings) SubscribeURL() string {
	return settings.PublishURL() + "/json?since=10m"
}

func (settings Settings) SealAnnouncement(value Announcement) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(settings.secret)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, raw, []byte("discovery"))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (settings Settings) OpenAnnouncement(value string) (Announcement, error) {
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Announcement{}, err
	}
	block, err := aes.NewCipher(settings.secret)
	if err != nil {
		return Announcement{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Announcement{}, err
	}
	if len(sealed) < gcm.NonceSize() {
		return Announcement{}, errors.New("encrypted announcement is too short")
	}
	raw, err := gcm.Open(
		nil,
		sealed[:gcm.NonceSize()],
		sealed[gcm.NonceSize():],
		[]byte("discovery"),
	)
	if err != nil {
		return Announcement{}, err
	}
	var result Announcement
	if err := json.Unmarshal(raw, &result); err != nil {
		return Announcement{}, err
	}
	return result, nil
}
