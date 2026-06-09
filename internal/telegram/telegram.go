package telegram

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/durck/reverse_logger/internal/events"
)

type Config struct {
	Enabled  bool
	BotToken string
	ChatIDs  []string
	ProxyURL string
	APIBase  string
	Timeout  time.Duration
}

type Client struct {
	enabled bool
	token   string
	chatIDs []string
	apiBase string
	http    *http.Client
}

func New(config Config) (*Client, error) {
	timeout := config.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.TrimSpace(config.ProxyURL) != "" {
		proxyURL, err := url.Parse(config.ProxyURL)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	apiBase := strings.TrimRight(config.APIBase, "/")
	if apiBase == "" {
		apiBase = "https://api.telegram.org"
	}

	return &Client{
		enabled: config.Enabled,
		token:   config.BotToken,
		chatIDs: config.ChatIDs,
		apiBase: apiBase,
		http: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
	}, nil
}

func (c *Client) Enabled() bool {
	return c != nil && c.enabled && c.token != "" && len(c.chatIDs) > 0
}

func (c *Client) NotifyEvent(ctx context.Context, event events.Event) error {
	if !c.Enabled() {
		return nil
	}
	status := strings.ToLower(event.Status)
	if status != "connected" && status != "disconnected" {
		return nil
	}

	message := FormatEventMessage(event)
	for _, chatID := range c.chatIDs {
		if err := c.SendMessage(ctx, chatID, message); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) SendMessage(ctx context.Context, chatID, text string) error {
	form := url.Values{}
	form.Set("chat_id", chatID)
	form.Set("text", text)
	form.Set("disable_web_page_preview", "true")

	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", c.apiBase, c.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("telegram sendMessage failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func FormatEventMessage(event events.Event) string {
	lines := []string{
		fmt.Sprintf("reverse_ssh %s", strings.ToUpper(event.Status)),
		fmt.Sprintf("host: %s", valueOrDash(event.HostName)),
		fmt.Sprintf("user: %s", valueOrDash(event.UserName)),
		fmt.Sprintf("computer: %s", valueOrDash(event.ComputerName)),
		fmt.Sprintf("id: %s", valueOrDash(event.ReverseSSHID)),
		fmt.Sprintf("ip: %s", valueOrDash(event.IPRaw)),
		fmt.Sprintf("version: %s", valueOrDash(event.Version)),
	}
	if !event.SourceTS.IsZero() {
		lines = append(lines, "source_ts: "+event.SourceTS.UTC().Format(time.RFC3339))
	}
	lines = append(lines, "received_at: "+event.ReceivedAt.UTC().Format(time.RFC3339))
	return strings.Join(lines, "\n")
}

func SplitChatIDs(raw string) []string {
	parts := strings.Split(raw, ",")
	var ids []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			ids = append(ids, part)
		}
	}
	return ids
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
