package loggerapp

import (
	"strings"
	"testing"
)

func TestLoadConfigRequiresTelegramTokenWhenEnabled(t *testing.T) {
	setMinimalConfigEnv(t)
	t.Setenv("TELEGRAM_ENABLED", "true")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_IDS", "123")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected telegram token error")
	}
	if !strings.Contains(err.Error(), "TELEGRAM_BOT_TOKEN") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestLoadConfigRequiresTelegramChatIDsWhenEnabled(t *testing.T) {
	setMinimalConfigEnv(t)
	t.Setenv("TELEGRAM_ENABLED", "true")
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("TELEGRAM_CHAT_IDS", " ")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected telegram chat IDs error")
	}
	if !strings.Contains(err.Error(), "TELEGRAM_CHAT_IDS") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestLoadConfigRejectsInvalidTelegramEnabled(t *testing.T) {
	setMinimalConfigEnv(t)
	t.Setenv("TELEGRAM_ENABLED", "treu")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected invalid TELEGRAM_ENABLED error")
	}
	if !strings.Contains(err.Error(), "TELEGRAM_ENABLED") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestLoadConfigAcceptsCompleteTelegramConfig(t *testing.T) {
	setMinimalConfigEnv(t)
	t.Setenv("TELEGRAM_ENABLED", "true")
	t.Setenv("TELEGRAM_BOT_TOKEN", " token ")
	t.Setenv("TELEGRAM_CHAT_IDS", " 123, 456 ")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.Telegram.Enabled {
		t.Fatal("telegram should be enabled")
	}
	if config.Telegram.BotToken != "token" {
		t.Fatalf("bot token = %q", config.Telegram.BotToken)
	}
	if len(config.Telegram.ChatIDs) != 2 || config.Telegram.ChatIDs[0] != "123" || config.Telegram.ChatIDs[1] != "456" {
		t.Fatalf("chat IDs = %#v", config.Telegram.ChatIDs)
	}
}

func TestLoadConfigParsesDashboardActiveSessionMaxAge(t *testing.T) {
	setMinimalConfigEnv(t)
	t.Setenv("DASHBOARD_ACTIVE_SESSION_MAX_AGE", "45m")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Dashboard.ActiveSessionMaxAge.String() != "45m0s" {
		t.Fatalf("active max age = %s", config.Dashboard.ActiveSessionMaxAge)
	}
}

func TestLoadConfigAllowsDisablingDashboardActiveSessionMaxAge(t *testing.T) {
	setMinimalConfigEnv(t)
	t.Setenv("DASHBOARD_ACTIVE_SESSION_MAX_AGE", "0s")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Dashboard.ActiveSessionMaxAge != 0 {
		t.Fatalf("active max age = %s", config.Dashboard.ActiveSessionMaxAge)
	}
}

func setMinimalConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"WEBHOOK_TOKEN",
		"DASHBOARD_ACTIVE_SESSION_MAX_AGE",
		"TELEGRAM_ENABLED",
		"TELEGRAM_BOT_TOKEN",
		"TELEGRAM_CHAT_IDS",
		"TELEGRAM_PROXY_URL",
		"TELEGRAM_API_BASE",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("WEBHOOK_TOKEN", "webhook-token")
}
