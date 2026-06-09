package loggerapp

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/durck/reverse_logger/internal/telegram"
)

type Config struct {
	ListenAddr       string
	DataDir          string
	WebhookToken     string
	EdgeForwardToken string
	Telegram         telegram.Config
}

func LoadConfig() (Config, error) {
	config := Config{
		ListenAddr:       envOrDefault("LISTEN_ADDR", ":8080"),
		DataDir:          envOrDefault("DATA_DIR", "/data"),
		WebhookToken:     strings.TrimSpace(os.Getenv("WEBHOOK_TOKEN")),
		EdgeForwardToken: strings.TrimSpace(os.Getenv("EDGE_FORWARD_TOKEN")),
		Telegram: telegram.Config{
			Enabled:  parseBool(os.Getenv("TELEGRAM_ENABLED")),
			BotToken: strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
			ChatIDs:  telegram.SplitChatIDs(os.Getenv("TELEGRAM_CHAT_IDS")),
			ProxyURL: strings.TrimSpace(os.Getenv("TELEGRAM_PROXY_URL")),
			APIBase:  strings.TrimSpace(os.Getenv("TELEGRAM_API_BASE")),
			Timeout:  5 * time.Second,
		},
	}

	if config.WebhookToken == "" {
		return Config{}, errors.New("WEBHOOK_TOKEN is required")
	}
	return config, nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on", "enabled":
		return true
	default:
		return false
	}
}
