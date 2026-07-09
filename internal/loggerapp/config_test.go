package loggerapp

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	t.Setenv("TELEGRAM_ALERT_MODE", "rich")

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
	if config.Telegram.AlertMode != "rich" {
		t.Fatalf("alert mode = %q", config.Telegram.AlertMode)
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

func TestDockerComposePassesLoggerConfigEnvironment(t *testing.T) {
	compose := readRepoFile(t, "docker-compose.yml")
	configSource := readRepoFile(t, "internal", "loggerapp", "config.go")

	for _, name := range loggerConfigEnvNames(configSource) {
		if !strings.Contains(compose, "      "+name+":") {
			t.Fatalf("docker-compose.yml does not pass logger config env %s", name)
		}
	}
}

func TestEnvExampleDocumentsComposeVariables(t *testing.T) {
	compose := readRepoFile(t, "docker-compose.yml") + "\n" + readRepoFile(t, "docker-compose.edge-forward.yml")
	envExample := readRepoFile(t, ".env.example")

	for _, name := range composeInterpolationNames(compose) {
		if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `=`).MatchString(envExample) {
			t.Fatalf(".env.example does not document compose variable %s", name)
		}
	}
}

func TestDockerComposeKeepsSessionPrivateKeyOutOfLoggerService(t *testing.T) {
	compose := readRepoFile(t, "docker-compose.yml")
	loggerBlock := composeServiceBlock(t, compose, "rssh-logger")
	reconcilerBlock := composeServiceBlock(t, compose, "rssh-session-reconciler")

	for _, forbidden := range []string{"RSSH_SESSION_CONSOLE_KEY_PATH", "/run/secrets/rssh_session_reconciler"} {
		if strings.Contains(loggerBlock, forbidden) {
			t.Fatalf("rssh-logger service must not receive reconciler private key reference %q", forbidden)
		}
		if !strings.Contains(reconcilerBlock, forbidden) {
			t.Fatalf("rssh-session-reconciler service missing private key reference %q", forbidden)
		}
	}
	if !strings.Contains(loggerBlock, "RSSH_SESSION_FORWARD_TOKEN:") {
		t.Fatal("rssh-logger service must receive RSSH_SESSION_FORWARD_TOKEN for snapshot endpoint auth")
	}
	if !strings.Contains(loggerBlock, "depends_on:\n      - rssh-session-reconciler") {
		t.Fatal("rssh-logger service must depend on rssh-session-reconciler for targeted compose startup")
	}
	if strings.Contains(loggerBlock, "RSSH_SESSION_CONSOLE_COMMAND_DELAY") {
		t.Fatal("rssh-logger service must not receive console command delay")
	}
	if !strings.Contains(reconcilerBlock, "RSSH_SESSION_CONSOLE_COMMAND_DELAY:") {
		t.Fatal("rssh-session-reconciler service must receive RSSH_SESSION_CONSOLE_COMMAND_DELAY")
	}
}

func setMinimalConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"WEBHOOK_TOKEN",
		"RSSH_SESSION_FORWARD_TOKEN",
		"DASHBOARD_ACTIVE_SESSION_MAX_AGE",
		"TELEGRAM_ENABLED",
		"TELEGRAM_BOT_TOKEN",
		"TELEGRAM_CHAT_IDS",
		"TELEGRAM_PROXY_URL",
		"TELEGRAM_API_BASE",
		"TELEGRAM_ALERT_MODE",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("WEBHOOK_TOKEN", "webhook-token")
}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, parts...)...)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func composeServiceBlock(t *testing.T, compose, service string) string {
	t.Helper()
	pattern := regexp.MustCompile(`(?ms)^  ` + regexp.QuoteMeta(service) + `:\n(.*?)(?:\n  [A-Za-z0-9_-]+:\n|\nnetworks:\n|\nvolumes:\n|\z)`)
	match := pattern.FindStringSubmatch(compose)
	if len(match) != 2 {
		t.Fatalf("service %s not found in compose", service)
	}
	return match[1]
}

func loggerConfigEnvNames(source string) []string {
	names := map[string]bool{}
	for _, pattern := range []string{
		`os\.Getenv\("([A-Z0-9_]+)"\)`,
		`envOrDefault\("([A-Z0-9_]+)"`,
	} {
		matches := regexp.MustCompile(pattern).FindAllStringSubmatch(source, -1)
		for _, match := range matches {
			names[match[1]] = true
		}
	}
	return sortedKeys(names)
}

func composeInterpolationNames(source string) []string {
	names := map[string]bool{}
	matches := regexp.MustCompile(`\$\{([A-Z0-9_]+)(?::[-?][^}]*)?\}`).FindAllStringSubmatch(source, -1)
	for _, match := range matches {
		names[match[1]] = true
	}
	return sortedKeys(names)
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
