package loggerapp

import (
	"errors"
	"os"
	"strconv"
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
	EdgeHealthToken  string
	DashboardToken   string
	IngressWSPath    string
	IngressPushPath  string
	Correlation      store.CorrelationConfig
	EdgeHealth       EdgeHealthConfig
	Telegram         telegram.Config
}

type EdgeHealthConfig struct {
	Token           string
	DefaultInterval time.Duration
	MissedReports   int
	BootstrapGrace  time.Duration
	MonitorInterval time.Duration
}

func LoadConfig() (Config, error) {
	telegramEnabled, err := parseBoolStrict("TELEGRAM_ENABLED", os.Getenv("TELEGRAM_ENABLED"))
	if err != nil {
		return Config{}, err
	}

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
		EdgeHealthToken:  strings.TrimSpace(os.Getenv("EDGE_HEALTH_TOKEN")),
		DashboardToken:   strings.TrimSpace(os.Getenv("DASHBOARD_TOKEN")),
		IngressWSPath:    events.NormalizeIngressPath(os.Getenv("INGRESS_WS_PATH"), events.DefaultWSPath),
		IngressPushPath:  events.NormalizeIngressPath(os.Getenv("INGRESS_PUSH_PATH"), events.DefaultPushPath),
		Correlation:      correlation,
		EdgeHealth: EdgeHealthConfig{
			Token:           strings.TrimSpace(os.Getenv("EDGE_HEALTH_TOKEN")),
			DefaultInterval: parseDurationOrDefault(os.Getenv("EDGE_HEALTH_DEFAULT_INTERVAL"), 30*time.Second),
			MissedReports:   parsePositiveIntOrDefault(os.Getenv("EDGE_HEALTH_MISSED_REPORTS"), 3),
			BootstrapGrace:  parseDurationOrDefault(os.Getenv("EDGE_HEALTH_BOOTSTRAP_GRACE"), store.DefaultEdgeHealthBootstrapGrace),
			MonitorInterval: parseDurationOrDefault(os.Getenv("EDGE_HEALTH_MONITOR_INTERVAL"), 30*time.Second),
		},
		Telegram: telegram.Config{
			Enabled:  telegramEnabled,
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
	if config.Telegram.Enabled {
		if config.Telegram.BotToken == "" {
			return Config{}, errors.New("TELEGRAM_BOT_TOKEN is required when TELEGRAM_ENABLED=true")
		}
		if len(config.Telegram.ChatIDs) == 0 {
			return Config{}, errors.New("TELEGRAM_CHAT_IDS is required when TELEGRAM_ENABLED=true")
		}
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

func parseBoolStrict(name, value string) (bool, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "":
		return false, nil
	case "1", "true", "yes", "y", "on", "enabled":
		return true, nil
	case "0", "false", "no", "n", "off", "disabled":
		return false, nil
	default:
		return false, errors.New(name + " must be a boolean value")
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

func parsePositiveIntOrDefault(value string, fallback int) int {
	num, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || num <= 0 {
		return fallback
	}
	return num
}
