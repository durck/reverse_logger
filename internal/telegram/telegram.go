package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/durck/reverse_logger/internal/events"
)

type Config struct {
	Enabled   bool
	BotToken  string
	ChatIDs   []string
	ProxyURL  string
	APIBase   string
	Timeout   time.Duration
	AlertMode string
}

type Client struct {
	enabled   bool
	token     string
	chatIDs   []string
	apiBase   string
	alertMode AlertMode
	http      *http.Client
}

type SendResult struct {
	MessageID int
}

type AlertMode string

const (
	AlertModeHTML  AlertMode = "html"
	AlertModeRich  AlertMode = "rich"
	AlertModePlain AlertMode = "plain"
)

type FormattedMessage struct {
	Plain    string
	HTML     string
	RichHTML string
}

type HealthAlert struct {
	VPSName          string
	PreviousStatus   string
	Status           string
	VPSPublicIP      string
	VPSInternalIP    string
	FailedChecks     []string
	LastReportStatus string
	LastSeenAt       string
	LastOKAt         string
	CheckedAt        string
	StaleAfter       string
	IntervalSeconds  int
	MissedReports    int
	AlertID          string
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

	alertMode, err := normalizeAlertMode(config.AlertMode, config.Enabled)
	if err != nil {
		return nil, err
	}

	return &Client{
		enabled:   config.Enabled,
		token:     token,
		chatIDs:   chatIDs,
		apiBase:   apiBase,
		alertMode: alertMode,
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

	message := FormatEventAlert(event)
	errs := make([]error, len(c.chatIDs))
	var wg sync.WaitGroup
	for i, chatID := range c.chatIDs {
		i, chatID := i, chatID
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.SendFormattedMessage(ctx, chatID, message); err != nil {
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
	return c.sendWithRetry(ctx, chatID, func(ctx context.Context, chatID string) (SendResult, error) {
		return c.sendTextMessageOnce(ctx, chatID, text, "")
	})
}

func (c *Client) SendFormattedMessage(ctx context.Context, chatID string, message FormattedMessage) error {
	_, err := c.SendFormattedMessageWithResult(ctx, chatID, message)
	return err
}

func (c *Client) SendFormattedMessageWithResult(ctx context.Context, chatID string, message FormattedMessage) (SendResult, error) {
	switch c.alertMode {
	case AlertModePlain:
		return c.SendMessageWithResult(ctx, chatID, message.plainText())
	case AlertModeRich:
		if strings.TrimSpace(message.RichHTML) != "" {
			result, err := c.sendWithRetry(ctx, chatID, func(ctx context.Context, chatID string) (SendResult, error) {
				return c.sendRichMessageOnce(ctx, chatID, message.RichHTML)
			})
			if err == nil {
				return result, nil
			}
			if !isRichMessageUnsupported(err) {
				return SendResult{}, err
			}
		}
		fallthrough
	case AlertModeHTML:
		if strings.TrimSpace(message.HTML) != "" {
			return c.sendWithRetry(ctx, chatID, func(ctx context.Context, chatID string) (SendResult, error) {
				return c.sendTextMessageOnce(ctx, chatID, message.HTML, "HTML")
			})
		}
		return c.SendMessageWithResult(ctx, chatID, message.plainText())
	default:
		return c.SendMessageWithResult(ctx, chatID, message.plainText())
	}
}

func (c *Client) sendWithRetry(ctx context.Context, chatID string, send func(context.Context, string) (SendResult, error)) (SendResult, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return SendResult{}, errors.New("telegram chat ID is required")
	}

	var lastErr error
	for attempt := 0; attempt < maxSendAttempts; attempt++ {
		result, err := send(ctx, chatID)
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

func (c *Client) sendTextMessageOnce(ctx context.Context, chatID, text, parseMode string) (SendResult, error) {
	form := url.Values{}
	form.Set("chat_id", chatID)
	form.Set("text", text)
	form.Set("disable_web_page_preview", "true")
	if parseMode != "" {
		form.Set("parse_mode", parseMode)
	}

	return c.sendFormOnce(ctx, "sendMessage", form)
}

func (c *Client) sendRichMessageOnce(ctx context.Context, chatID, richHTML string) (SendResult, error) {
	payload := map[string]any{
		"chat_id": telegramChatIDValue(chatID),
		"rich_message": map[string]any{
			"html":                  richHTML,
			"skip_entity_detection": true,
		},
	}
	return c.sendJSONOnce(ctx, "sendRichMessage", payload)
}

func (c *Client) sendFormOnce(ctx context.Context, method string, form url.Values) (SendResult, error) {
	endpoint := fmt.Sprintf("%s/bot%s/%s", c.apiBase, c.token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return SendResult{}, fmt.Errorf("telegram %s request build failed: %s", method, redactToken(err.Error(), c.token))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return c.doTelegramRequest(req, method)
}

func (c *Client) sendJSONOnce(ctx context.Context, method string, payload any) (SendResult, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return SendResult{}, fmt.Errorf("telegram %s payload build failed: %s", method, redactToken(err.Error(), c.token))
	}
	endpoint := fmt.Sprintf("%s/bot%s/%s", c.apiBase, c.token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return SendResult{}, fmt.Errorf("telegram %s request build failed: %s", method, redactToken(err.Error(), c.token))
	}
	req.Header.Set("Content-Type", "application/json")

	return c.doTelegramRequest(req, method)
}

func (c *Client) doTelegramRequest(req *http.Request, method string) (SendResult, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return SendResult{}, ctxErr
		}
		return SendResult{}, sendRequestError{method: method, message: redactToken(err.Error(), c.token)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	bodyText := redactToken(strings.TrimSpace(string(body)), c.token)
	apiResponse, parseErr := parseTelegramAPIResponse(body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		statusErr := &sendStatusError{
			method: method,
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
		return SendResult{}, fmt.Errorf("telegram %s response parse failed: %s", method, redactToken(parseErr.Error(), c.token))
	}
	if !*apiResponse.OK {
		return SendResult{}, &sendStatusError{
			method:     method,
			status:     resp.StatusCode,
			apiCode:    apiResponse.ErrorCode,
			body:       bodyText,
			retryAfter: retryAfterFromResponse(apiResponse),
		}
	}
	return SendResult{MessageID: apiResponse.Result.MessageID}, nil
}

func FormatEventMessage(event events.Event) string {
	return FormatEventAlert(event).Plain
}

func FormatEventAlert(event events.Event) FormattedMessage {
	return FormatEnrichedEventAlert(events.NewEnrichedEvent(event, nil, ""))
}

func FormatEnrichedEventAlert(event events.EnrichedEvent) FormattedMessage {
	title := fmt.Sprintf("reverse_ssh %s", strings.ToUpper(valueOrDash(event.Status)))
	sourceHash := firstNonEmpty(event.SourceEventHash, event.EventHash)
	alertID := shortAlertID(sourceHash)

	sections := []alertSection{
		{
			Title: "Identity",
			Fields: []alertField{
				requiredAlertField("host", event.HostName),
				optionalAlertField("user", event.UserName),
				optionalAlertField("computer", event.ComputerName),
				requiredAlertField("id", event.ReverseSSHID),
				optionalAlertField("version", event.Version),
			},
		},
		{
			Title: "Network",
			Fields: []alertField{
				optionalAlertField("ip", endpointDisplay(event.IPAddr, event.IPPort, event.IPRaw)),
				optionalAlertField("real_client", endpointDisplay(event.RealClientIP, event.ClientPort, "")),
				optionalAlertField("transport", event.Transport),
				optionalAlertField("proxy_source_ip", event.ProxySourceIP),
				optionalAlertField("fingerprint", event.PublicKeyFingerprint),
			},
		},
		{
			Title: "Ingress",
			Fields: []alertField{
				optionalAlertField("vps", event.VPSName),
				optionalAlertField("vps_public_ip", event.VPSPublicIP),
				optionalAlertField("vps_internal_ip", event.VPSInternalIP),
				optionalAlertField("forwarder_ip", event.ForwarderIP),
				requiredAlertField("correlation", correlationDisplay(event.CorrelationStatus, event.CorrelationMethod)),
			},
		},
		{
			Title: "Timeline",
			Fields: []alertField{
				optionalAlertField("source_ts", formatTime(event.SourceTS)),
				optionalAlertField("ingress_seen_at", formatTime(event.IngressReceivedAt)),
				requiredAlertField("received_at", formatTime(event.ReceivedAt)),
			},
		},
		{
			Title: "Tracking",
			Fields: []alertField{
				optionalAlertField("alert_id", alertID),
			},
		},
	}

	return buildAlertMessage(title, sections)
}

func FormatHealthAlertMessage(alert HealthAlert) string {
	return FormatHealthAlert(alert).Plain
}

func FormatHealthAlert(alert HealthAlert) FormattedMessage {
	title := fmt.Sprintf("edge health %s", strings.ToUpper(valueOrDash(alert.Status)))
	sections := []alertSection{
		{
			Title: "State",
			Fields: []alertField{
				requiredAlertField("vps", alert.VPSName),
				requiredAlertField("previous", alert.PreviousStatus),
				requiredAlertField("current", alert.Status),
				optionalAlertField("last_report", alert.LastReportStatus),
			},
		},
		{
			Title: "Network",
			Fields: []alertField{
				optionalAlertField("public_ip", alert.VPSPublicIP),
				optionalAlertField("internal_ip", alert.VPSInternalIP),
			},
		},
		{
			Title: "Checks",
			Fields: []alertField{
				requiredAlertField("failed_checks", formatStringList(alert.FailedChecks)),
				optionalAlertField("interval_seconds", formatPositiveInt(alert.IntervalSeconds)),
				optionalAlertField("missed_reports", formatPositiveInt(alert.MissedReports)),
			},
		},
		{
			Title: "Timeline",
			Fields: []alertField{
				optionalAlertField("checked_at", alert.CheckedAt),
				optionalAlertField("last_seen_at", alert.LastSeenAt),
				optionalAlertField("last_ok_at", alert.LastOKAt),
				optionalAlertField("stale_after", alert.StaleAfter),
			},
		},
		{
			Title: "Tracking",
			Fields: []alertField{
				optionalAlertField("alert_id", strings.TrimSpace(alert.AlertID)),
			},
		},
	}
	return buildAlertMessage(title, sections)
}

func FormatReverseSSHErrorMessage(event events.ReverseSSHErrorEvent) string {
	return FormatReverseSSHErrorAlert(event).Plain
}

func FormatReverseSSHErrorAlert(event events.ReverseSSHErrorEvent) FormattedMessage {
	title := fmt.Sprintf("reverse_ssh %s", strings.ToUpper(valueOrDash(event.Severity)))
	sections := []alertSection{
		{
			Title: "Failure",
			Fields: []alertField{
				requiredAlertField("reason", event.Reason),
				requiredAlertField("message", trimTelegramLine(event.Message, 1400)),
			},
		},
		{
			Title: "Remote",
			Fields: []alertField{
				requiredAlertField("remote", firstNonEmpty(event.RemoteAddr, endpointDisplay(event.RemoteIP, event.RemotePort, ""))),
				optionalAlertField("remote_ip", event.RemoteIP),
				optionalAlertField("remote_port", formatPositiveInt(event.RemotePort)),
				optionalAlertField("transport", event.Transport),
				optionalAlertField("host", event.Host),
				optionalAlertField("fingerprint", event.Fingerprint),
			},
		},
		{
			Title: "Source",
			Fields: []alertField{
				requiredAlertField("source", event.Source),
				optionalAlertField("unit", event.Unit),
				optionalAlertField("observed_at", formatTime(event.ObservedAt)),
				requiredAlertField("received_at", formatTime(event.ReceivedAt)),
			},
		},
		{
			Title: "Tracking",
			Fields: []alertField{
				optionalAlertField("alert_id", ReverseSSHErrorAlertID(event)),
			},
		},
	}
	return buildAlertMessage(title, sections)
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

func ReverseSSHErrorAlertID(event events.ReverseSSHErrorEvent) string {
	eventHash := strings.TrimSpace(event.EventHash)
	if eventHash == "" {
		return ""
	}
	if len(eventHash) <= 12 {
		return eventHash
	}
	return eventHash[:12]
}

type alertSection struct {
	Title  string
	Fields []alertField
}

type alertField struct {
	Label     string
	Value     string
	OmitEmpty bool
}

func requiredAlertField(label, value string) alertField {
	return alertField{Label: label, Value: value}
}

func optionalAlertField(label, value string) alertField {
	return alertField{Label: label, Value: value, OmitEmpty: true}
}

func buildAlertMessage(title string, sections []alertSection) FormattedMessage {
	return FormattedMessage{
		Plain:    buildPlainAlert(title, sections),
		HTML:     buildHTMLAlert(title, sections),
		RichHTML: buildRichHTMLAlert(title, sections),
	}
}

func buildPlainAlert(title string, sections []alertSection) string {
	lines := []string{strings.TrimSpace(title)}
	for _, section := range sections {
		fields := visibleAlertFields(section.Fields)
		if len(fields) == 0 {
			continue
		}
		if strings.TrimSpace(section.Title) != "" {
			lines = append(lines, strings.TrimSpace(section.Title))
		}
		for _, field := range fields {
			lines = append(lines, fmt.Sprintf("%s: %s", field.Label, valueOrDash(cleanAlertValue(field.Value))))
		}
	}
	return strings.Join(lines, "\n")
}

func buildHTMLAlert(title string, sections []alertSection) string {
	var builder strings.Builder
	builder.WriteString("<b>")
	builder.WriteString(escapeTelegramHTML(strings.TrimSpace(title)))
	builder.WriteString("</b>")
	for _, section := range sections {
		fields := visibleAlertFields(section.Fields)
		if len(fields) == 0 {
			continue
		}
		builder.WriteString("\n<blockquote>")
		if strings.TrimSpace(section.Title) != "" {
			builder.WriteString("<b>")
			builder.WriteString(escapeTelegramHTML(strings.TrimSpace(section.Title)))
			builder.WriteString("</b>\n")
		}
		for i, field := range fields {
			if i > 0 {
				builder.WriteString("\n")
			}
			builder.WriteString(escapeTelegramHTML(field.Label))
			builder.WriteString(": ")
			builder.WriteString(escapeTelegramHTML(valueOrDash(cleanAlertValue(field.Value))))
		}
		builder.WriteString("</blockquote>")
	}
	return builder.String()
}

func buildRichHTMLAlert(title string, sections []alertSection) string {
	var builder strings.Builder
	builder.WriteString("<h3>")
	builder.WriteString(escapeTelegramHTML(strings.TrimSpace(title)))
	builder.WriteString("</h3>")
	for _, section := range sections {
		fields := visibleAlertFields(section.Fields)
		if len(fields) == 0 {
			continue
		}
		caption := strings.TrimSpace(section.Title)
		builder.WriteString("\n<table bordered striped>")
		if caption != "" {
			builder.WriteString("<caption>")
			builder.WriteString(escapeTelegramHTML(caption))
			builder.WriteString("</caption>")
		}
		builder.WriteString("<tr><th>Field</th><th>Value</th></tr>")
		for _, field := range fields {
			builder.WriteString("<tr><td>")
			builder.WriteString(escapeTelegramHTML(field.Label))
			builder.WriteString("</td><td><code>")
			builder.WriteString(escapeTelegramHTML(valueOrDash(cleanAlertValue(field.Value))))
			builder.WriteString("</code></td></tr>")
		}
		builder.WriteString("</table>")
	}
	return builder.String()
}

func visibleAlertFields(fields []alertField) []alertField {
	out := make([]alertField, 0, len(fields))
	for _, field := range fields {
		field.Label = strings.TrimSpace(field.Label)
		field.Value = cleanAlertValue(field.Value)
		if field.Label == "" {
			continue
		}
		if field.OmitEmpty && strings.TrimSpace(field.Value) == "" {
			continue
		}
		out = append(out, field)
	}
	return out
}

func (message FormattedMessage) plainText() string {
	if strings.TrimSpace(message.Plain) != "" {
		return message.Plain
	}
	if strings.TrimSpace(message.HTML) != "" {
		return stripSimpleTelegramHTML(message.HTML)
	}
	return stripSimpleTelegramHTML(message.RichHTML)
}

func stripSimpleTelegramHTML(value string) string {
	replacements := []struct {
		old string
		new string
	}{
		{"</h3>", "\n"},
		{"</caption>", "\n"},
		{"</tr>", "\n"},
		{"</p>", "\n"},
		{"</blockquote>", "\n"},
		{"<br>", "\n"},
		{"<br/>", "\n"},
	}
	for _, replacement := range replacements {
		value = strings.ReplaceAll(value, replacement.old, replacement.new)
	}
	var builder strings.Builder
	inTag := false
	for _, r := range value {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				builder.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(html.UnescapeString(builder.String()))
}

func normalizeAlertMode(raw string, enabled bool) (AlertMode, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	if mode == "" {
		return AlertModeHTML, nil
	}
	switch AlertMode(mode) {
	case AlertModeHTML, AlertModeRich, AlertModePlain:
		return AlertMode(mode), nil
	default:
		if !enabled {
			return AlertModeHTML, nil
		}
		return "", errors.New("telegram alert mode must be html, rich, or plain")
	}
}

func telegramChatIDValue(chatID string) any {
	if id, err := strconv.ParseInt(strings.TrimSpace(chatID), 10, 64); err == nil {
		return id
	}
	return strings.TrimSpace(chatID)
}

func endpointDisplay(ip string, port int, fallback string) string {
	ip = strings.TrimSpace(ip)
	fallback = strings.TrimSpace(fallback)
	if ip == "" {
		return fallback
	}
	if port > 0 {
		return net.JoinHostPort(ip, strconv.Itoa(port))
	}
	return ip
}

func correlationDisplay(status, method string) string {
	status = strings.TrimSpace(status)
	method = strings.TrimSpace(method)
	switch {
	case status == "" && method == "":
		return ""
	case method == "":
		return status
	case status == "":
		return method
	default:
		return status + "/" + method
	}
}

func formatStringList(values []string) string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = cleanAlertValue(value)
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	return strings.Join(cleaned, ",")
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func formatPositiveInt(value int) string {
	if value <= 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func shortAlertID(eventHash string) string {
	eventHash = strings.TrimSpace(eventHash)
	if eventHash == "" {
		return ""
	}
	if len(eventHash) <= 12 {
		return eventHash
	}
	return eventHash[:12]
}

func cleanAlertValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.Join(strings.Fields(value), " ")
}

func escapeTelegramHTML(value string) string {
	return html.EscapeString(value)
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

type sendRequestError struct {
	method  string
	message string
}

func (e sendRequestError) Error() string {
	return fmt.Sprintf("telegram %s request failed: %s", telegramMethodName(e.method), e.message)
}

type sendStatusError struct {
	method     string
	status     int
	apiCode    int
	body       string
	retryAfter time.Duration
}

func (e *sendStatusError) Error() string {
	if e.body == "" {
		if e.apiCode != 0 {
			return fmt.Sprintf("telegram %s failed: status=%d error_code=%d", telegramMethodName(e.method), e.status, e.apiCode)
		}
		return fmt.Sprintf("telegram %s failed: status=%d", telegramMethodName(e.method), e.status)
	}
	return fmt.Sprintf("telegram %s failed: status=%d body=%s", telegramMethodName(e.method), e.status, e.body)
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

func isRichMessageUnsupported(err error) bool {
	var statusErr *sendStatusError
	if !errors.As(err, &statusErr) || statusErr.method != "sendRichMessage" {
		return false
	}
	if statusErr.status == http.StatusNotFound || statusErr.apiCode == http.StatusNotFound {
		return true
	}
	body := strings.ToLower(statusErr.body)
	return strings.Contains(body, "method") &&
		(strings.Contains(body, "not found") ||
			strings.Contains(body, "not available") ||
			strings.Contains(body, "unsupported"))
}

func telegramMethodName(method string) string {
	method = strings.TrimSpace(method)
	if method == "" {
		return "sendMessage"
	}
	return method
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func trimTelegramLine(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}
