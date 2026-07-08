package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/durck/reverse_logger/internal/events"
)

type config struct {
	ForwardURL string
	Token      string
	Unit       string
	Source     string
	Command    string
	Timeout    time.Duration
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	if err := run(context.Background(), cfg); err != nil {
		log.Fatal(err)
	}
}

func loadConfig() (config, error) {
	unit := envOrDefault("RSSH_JOURNAL_UNIT", "reverse_ssh")
	cfg := config{
		ForwardURL: strings.TrimSpace(os.Getenv("RSSH_ERROR_FORWARD_URL")),
		Token:      firstNonEmpty(os.Getenv("RSSH_ERROR_FORWARD_TOKEN"), os.Getenv("EDGE_FORWARD_TOKEN")),
		Unit:       unit,
		Source:     envOrDefault("RSSH_ERROR_SOURCE", "journalctl"),
		Command:    envOrDefault("RSSH_JOURNAL_COMMAND", "journalctl -u "+shellQuote(unit)+" -f -n 0 -o cat"),
		Timeout:    parseDurationOrDefault(os.Getenv("RSSH_ERROR_FORWARD_TIMEOUT"), 5*time.Second),
	}
	if cfg.ForwardURL == "" {
		return config{}, errors.New("RSSH_ERROR_FORWARD_URL is required")
	}
	if cfg.Token == "" {
		return config{}, errors.New("RSSH_ERROR_FORWARD_TOKEN or EDGE_FORWARD_TOKEN is required")
	}
	return cfg, nil
}

func run(ctx context.Context, cfg config) error {
	cmd := shellCommand(ctx, cfg.Command)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	errCh := make(chan error, 2)
	go func() {
		errCh <- scanLines(ctx, cfg, stdout)
	}()
	go func() {
		errCh <- scanLines(ctx, cfg, stderr)
	}()

	waitErr := cmd.Wait()
	stdoutErr := <-errCh
	stderrErr := <-errCh
	return errors.Join(stdoutErr, stderrErr, waitErr)
}

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}

func scanLines(ctx context.Context, cfg config, reader io.Reader) error {
	client := &http.Client{Timeout: cfg.Timeout}
	endpoint := forwardEndpoint(cfg.ForwardURL, cfg.Token)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		event, ok := events.NewReverseSSHErrorEventFromLogLine(cfg.Source, cfg.Unit, line, time.Now(), time.Now())
		if !ok {
			continue
		}
		if err := postEvent(ctx, client, endpoint, event); err != nil {
			log.Printf("forward reverse_ssh error event failed: %v", err)
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		return err
	}
	return nil
}

func postEvent(ctx context.Context, client *http.Client, endpoint string, event events.ReverseSSHErrorEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("logger returned status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func forwardEndpoint(baseURL, token string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/" + url.PathEscape(strings.TrimSpace(token))
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseDurationOrDefault(value string, fallback time.Duration) time.Duration {
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
