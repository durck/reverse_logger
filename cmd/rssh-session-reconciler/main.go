package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/durck/reverse_logger/internal/rsshsession"
)

type config struct {
	Console    rsshsession.ConsoleConfig
	ForwardURL string
	Token      string
	Interval   time.Duration
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	httpClient := &http.Client{Timeout: cfg.Console.Timeout}
	log.Printf("rssh-session-reconciler polling %s:%d every %s", cfg.Console.Host, cfg.Console.Port, cfg.Interval)

	for {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Console.Timeout)
		if err := rsshsession.WaitForLogger(ctx, cfg.ForwardURL, httpClient); err != nil {
			cancel()
			log.Printf("logger health wait failed: %v", err)
			time.Sleep(cfg.Interval)
			continue
		}
		cancel()

		checkedAt := time.Now().UTC()
		ctx, cancel = context.WithTimeout(context.Background(), cfg.Console.Timeout)
		sessions, err := rsshsession.FetchLiveSessions(ctx, cfg.Console)
		cancel()
		if err != nil {
			log.Printf("reverse_ssh session snapshot failed: %v", err)
			time.Sleep(cfg.Interval)
			continue
		}

		payload := rsshsession.SnapshotPayload{
			CheckedAt:         checkedAt,
			LiveReverseSSHIDs: rsshsession.LiveSessionIDs(sessions),
		}
		ctx, cancel = context.WithTimeout(context.Background(), cfg.Console.Timeout)
		err = (rsshsession.SnapshotClient{
			ForwardURL: cfg.ForwardURL,
			Token:      cfg.Token,
			HTTPClient: httpClient,
		}).PostSnapshot(ctx, payload)
		cancel()
		if err != nil {
			log.Printf("post session snapshot failed: %v", err)
		} else {
			log.Printf("posted session snapshot: live=%d", len(payload.LiveReverseSSHIDs))
		}
		time.Sleep(cfg.Interval)
	}
}

func loadConfig() (config, error) {
	timeout := parseDurationEnv("RSSH_SESSION_TIMEOUT", 10*time.Second)
	interval := parseDurationEnv("RSSH_SESSION_INTERVAL", 30*time.Second)
	port := parseIntEnv("RSSH_SESSION_CONSOLE_PORT", 2222)
	cfg := config{
		Console: rsshsession.ConsoleConfig{
			Host:           envOrDefault("RSSH_SESSION_CONSOLE_HOST", "reverse_ssh"),
			Port:           port,
			User:           envOrDefault("RSSH_SESSION_CONSOLE_USER", "root"),
			PrivateKeyPath: strings.TrimSpace(os.Getenv("RSSH_SESSION_CONSOLE_KEY_PATH")),
			KnownHostsPath: envOrDefault("RSSH_SESSION_KNOWN_HOSTS_PATH", "/state/known_hosts"),
			Timeout:        timeout,
			CommandDelay:   parseDurationEnv("RSSH_SESSION_CONSOLE_COMMAND_DELAY", time.Second),
		},
		ForwardURL: envOrDefault("RSSH_SESSION_FORWARD_URL", "http://rssh-logger:8080/session-snapshots"),
		Token:      strings.TrimSpace(os.Getenv("RSSH_SESSION_FORWARD_TOKEN")),
		Interval:   interval,
	}
	if cfg.Token == "" {
		return config{}, errConfig("RSSH_SESSION_FORWARD_TOKEN is required")
	}
	if cfg.Console.PrivateKeyPath == "" {
		return config{}, errConfig("RSSH_SESSION_CONSOLE_KEY_PATH is required")
	}
	return cfg, nil
}

type errConfig string

func (e errConfig) Error() string {
	return string(e)
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func parseDurationEnv(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func parseIntEnv(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
