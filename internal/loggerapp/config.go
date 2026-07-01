package loggerapp

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/durck/reverse_logger/internal/events"
	"github.com/durck/reverse_logger/internal/store"
	"github.com/durck/reverse_logger/internal/telegram"
)

type Config struct {
	ListenAddr       string
	DataDir          string
	WebhookToken     string
	EdgeForwardToken string
	DashboardToken   string
	IngressWSPath    string
	IngressPushPath  string
	Correlation      store.CorrelationConfig
	Telegram         telegram.Config
}

func LoadConfig() (Config, error) {
	correlation := store.DefaultCorrelationConfig()
	correlation.WebhookMatchBefore = parseDurationOrDefault(os.Getenv("CORRELATION_WEBHOOK_MATCH_BEFORE"), correlation.WebhookMatchBefore)
	correlation.WebhookMatchAfter = parseDurationOrDefault(os.Getenv("CORRELATION_WEBHOOK_MATCH_AFTER"), correlation.WebhookMatchAfter)
	correlation.IngressReconcileBefore = parseDurationOrDefault(os.Getenv("CORRELATION_INGRESS_RECONCILE_BEFORE"), correlation.IngressReconcileBefore)
	correlation.IngressReconcileAfter = parseDurationOrDefault(os.Getenv("CORRELATION_INGRESS_RECONCILE_AFTER"), correlation.IngressReconcileAfter)
	correlation.EnableClientIPFallback = parseBoolOrDefault(os.Getenv("CORRELATION_CLIENT_IP_FALLBACK_ENABLED"), correlation.EnableClientIPFallback)
	correlation.EnableUniqueTimeFallback = parseBoolOrDefault(os.Getenv("CORRELATION_UNIQUE_TIME_FALLBACK_ENABLED"), correlation.EnableUniqueTimeFallback)

	config := Config{
		ListenAddr:       envOrDefault("LISTEN_ADDR", ":8080"),
		DataDir:          envOrDefault("DATA_DIR", "/data"),
		WebhookToken:     strings.TrimSpace(os.Getenv("WEBHOOK_TOKEN")),
		EdgeForwardToken: strings.TrimSpace(os.Getenv("EDGE_FORWARD_TOKEN")),
		DashboardToken:   strings.TrimSpace(os.Getenv("DASHBOARD_TOKEN")),
		IngressWSPath:    events.NormalizeIngressPath(os.Getenv("INGRESS_WS_PATH"), events.DefaultWSPath),
		IngressPushPath:  events.NormalizeIngressPath(os.Getenv("INGRESS_PUSH_PATH"), events.DefaultPushPath),
		Correlation:      correlation,
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

func parseBoolOrDefault(value string, fallback bool) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return parseBool(value)
}

func parseDurationOrDefault(value string, fallback time.Duration) time.Duration {
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}
