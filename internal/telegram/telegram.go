package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
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

type SendResult struct {
	MessageID int
}

const (
	defaultTimeout      = 5 * time.Second
	maxSendAttempts     = 3
	baseSendRetryDelay  = 250 * time.Millisecond
	redactedTokenMarker = "<redacted>"
)

func New(config Config) (*Client, error) {
	timeout := config.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	token := strings.TrimSpace(config.BotToken)
	chatIDs := normalizeChatIDs(config.ChatIDs)
	if config.Enabled {
		if token == "" {
			return nil, errors.New("telegram bot token is required when telegram is enabled")
		}
		if len(chatIDs) == 0 {
			return nil, errors.New("telegram chat IDs are required when telegram is enabled")
		}
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	proxyRaw := strings.TrimSpace(config.ProxyURL)
	if config.Enabled && proxyRaw != "" {
		proxyURL, err := url.Parse(proxyRaw)
		if err != nil || proxyURL.Scheme == "" || proxyURL.Host == "" {
			return nil, errors.New("telegram proxy URL is invalid")
		}
		if !isSupportedProxyScheme(proxyURL.Scheme) {
			return nil, errors.New("telegram proxy URL scheme is unsupported")
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	apiBase, err := normalizeAPIBase(config.APIBase, config.Enabled)
	if err != nil {
		return nil, err
	}

	return &Client{
		enabled: config.Enabled,
		token:   token,
		chatIDs: chatIDs,
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

func (c *Client) ChatIDs() []string {
	if c == nil {
		return nil
	}
	return append([]string(nil), c.chatIDs...)
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
	errs := make([]error, len(c.chatIDs))
	var wg sync.WaitGroup
	for i, chatID := range c.chatIDs {
		i, chatID := i, chatID
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.SendMessage(ctx, chatID, message); err != nil {
				errs[i] = fmt.Errorf("recipient %d: %w", i+1, err)
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

func (c *Client) SendMessage(ctx context.Context, chatID, text string) error {
	_, err := c.SendMessageWithResult(ctx, chatID, text)
	return err
}

func (c *Client) SendMessageWithResult(ctx context.Context, chatID, text string) (SendResult, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return SendResult{}, errors.New("telegram chat ID is required")
	}

	var lastErr error
	for attempt := 0; attempt < maxSendAttempts; attempt++ {
		result, err := c.sendMessageOnce(ctx, chatID, text)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if attempt == maxSendAttempts-1 || !isRetryableSendError(err) {
			return SendResult{}, err
		}
		if err := waitForRetry(ctx, sendRetryDelay(attempt, err)); err != nil {
			return SendResult{}, err
		}
	}
	return SendResult{}, lastErr
}

func (c *Client) sendMessageOnce(ctx context.Context, chatID, text string) (SendResult, error) {
	form := url.Values{}
	form.Set("chat_id", chatID)
	form.Set("text", text)
	form.Set("disable_web_page_preview", "true")

	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", c.apiBase, c.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return SendResult{}, fmt.Errorf("telegram sendMessage request build failed: %s", redactToken(err.Error(), c.token))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return SendResult{}, ctxErr
		}
		return SendResult{}, sendRequestError(redactToken(err.Error(), c.token))
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	bodyText := redactToken(strings.TrimSpace(string(body)), c.token)
	apiResponse, parseErr := parseTelegramAPIResponse(body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		statusErr := &sendStatusError{
			status: resp.StatusCode,
			body:   bodyText,
		}
		if parseErr == nil {
			statusErr.apiCode = apiResponse.ErrorCode
			statusErr.retryAfter = retryAfterFromResponse(apiResponse)
		}
		return SendResult{}, statusErr
	}
	if parseErr != nil {
		return SendResult{}, fmt.Errorf("telegram sendMessage response parse failed: %s", redactToken(parseErr.Error(), c.token))
	}
	if !*apiResponse.OK {
		return SendResult{}, &sendStatusError{
			status:     resp.StatusCode,
			apiCode:    apiResponse.ErrorCode,
			body:       bodyText,
			retryAfter: retryAfterFromResponse(apiResponse),
		}
	}
	return SendResult{MessageID: apiResponse.Result.MessageID}, nil
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
	if alertID := AlertID(event); alertID != "" {
		lines = append(lines, "alert_id: "+alertID)
	}
	if !event.SourceTS.IsZero() {
		lines = append(lines, "source_ts: "+event.SourceTS.UTC().Format(time.RFC3339))
	}
	lines = append(lines, "received_at: "+event.ReceivedAt.UTC().Format(time.RFC3339))
	return strings.Join(lines, "\n")
}

func AlertID(event events.Event) string {
	eventHash := strings.TrimSpace(event.EventHash)
	if eventHash == "" {
		return ""
	}
	if len(eventHash) <= 12 {
		return eventHash
	}
	return eventHash[:12]
}

func SplitChatIDs(raw string) []string {
	return normalizeChatIDs(strings.Split(raw, ","))
}

func normalizeChatIDs(parts []string) []string {
	var ids []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			ids = append(ids, part)
		}
	}
	return ids
}

func normalizeAPIBase(raw string, enabled bool) (string, error) {
	apiBase := strings.TrimRight(strings.TrimSpace(raw), "/")
	if apiBase == "" {
		return "https://api.telegram.org", nil
	}
	if !enabled {
		return apiBase, nil
	}

	parsed, err := url.Parse(apiBase)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("telegram API base URL is invalid")
	}
	switch parsed.Scheme {
	case "https":
		return apiBase, nil
	case "http":
		if isLoopbackHost(parsed.Hostname()) {
			return apiBase, nil
		}
	}
	return "", errors.New("telegram API base URL must use https unless it targets localhost")
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isSupportedProxyScheme(scheme string) bool {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "http", "https", "socks5", "socks5h":
		return true
	default:
		return false
	}
}

type sendRequestError string

func (e sendRequestError) Error() string {
	return fmt.Sprintf("telegram sendMessage request failed: %s", string(e))
}

type sendStatusError struct {
	status     int
	apiCode    int
	body       string
	retryAfter time.Duration
}

func (e *sendStatusError) Error() string {
	if e.body == "" {
		if e.apiCode != 0 {
			return fmt.Sprintf("telegram sendMessage failed: status=%d error_code=%d", e.status, e.apiCode)
		}
		return fmt.Sprintf("telegram sendMessage failed: status=%d", e.status)
	}
	return fmt.Sprintf("telegram sendMessage failed: status=%d body=%s", e.status, e.body)
}

type telegramAPIResponse struct {
	OK          *bool  `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
	Result      struct {
		MessageID int `json:"message_id"`
	} `json:"result"`
	Parameters struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func parseTelegramAPIResponse(body []byte) (telegramAPIResponse, error) {
	var response telegramAPIResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return telegramAPIResponse{}, err
	}
	if response.OK == nil {
		return telegramAPIResponse{}, errors.New("telegram response missing ok field")
	}
	return response, nil
}

func retryAfterFromResponse(response telegramAPIResponse) time.Duration {
	if response.Parameters.RetryAfter <= 0 {
		return 0
	}
	return time.Duration(response.Parameters.RetryAfter) * time.Second
}

func isRetryableSendError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var statusErr *sendStatusError
	if errors.As(err, &statusErr) {
		return statusErr.retryAfter > 0 ||
			statusErr.status == http.StatusTooManyRequests ||
			statusErr.status >= http.StatusInternalServerError ||
			statusErr.apiCode == http.StatusTooManyRequests ||
			statusErr.apiCode >= http.StatusInternalServerError
	}
	var requestErr sendRequestError
	return errors.As(err, &requestErr)
}

func sendRetryDelay(attempt int, err error) time.Duration {
	var statusErr *sendStatusError
	if errors.As(err, &statusErr) && statusErr.retryAfter > 0 {
		return statusErr.retryAfter
	}
	return time.Duration(attempt+1) * baseSendRetryDelay
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func redactToken(value, token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return value
	}
	return strings.ReplaceAll(value, token, redactedTokenMarker)
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
