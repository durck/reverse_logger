package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/durck/reverse_logger/internal/events"
)

func TestNotifyEventSendsMessage(t *testing.T) {
	called := make(chan string, 1)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottoken/sendMessage" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		called <- r.Form.Get("text")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer api.Close()

	client, err := New(Config{
		Enabled:  true,
		BotToken: "token",
		ChatIDs:  []string{"123"},
		APIBase:  api.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	event, err := events.ParseWebhookPayload([]byte(`{"Status":"connected","ID":"abc","HostName":"u.c","Timestamp":"2026-06-09T12:00:00Z"}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.NotifyEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}

	select {
	case text := <-called:
		if !strings.Contains(text, "CONNECTED") || !strings.Contains(text, "host: u.c") {
			t.Fatalf("unexpected message %q", text)
		}
	case <-time.After(time.Second):
		t.Fatal("telegram API was not called")
	}
}

func TestNotifyEventContinuesAfterRecipientFailure(t *testing.T) {
	seen := make(chan string, 2)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		chatID := r.Form.Get("chat_id")
		seen <- chatID
		if chatID == "bad" {
			writeTelegramError(w, http.StatusBadRequest, "chat not found")
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer api.Close()

	client, err := New(Config{
		Enabled:  true,
		BotToken: "token",
		ChatIDs:  []string{"bad", "good"},
		APIBase:  api.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	event, err := events.ParseWebhookPayload([]byte(`{"Status":"connected","ID":"abc","HostName":"u.c","Timestamp":"2026-06-09T12:00:00Z"}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	err = client.NotifyEvent(context.Background(), event)
	if err == nil {
		t.Fatal("expected partial delivery error")
	}

	got := make(map[string]int)
	for len(got) < 2 {
		select {
		case chatID := <-seen:
			got[chatID]++
		case <-time.After(time.Second):
			t.Fatalf("not all recipients were attempted, seen=%v", got)
		}
	}
	if got["bad"] != 1 || got["good"] != 1 {
		t.Fatalf("expected both recipients to be attempted once, seen=%v", got)
	}
}

func TestNotifyEventSendsRecipientsConcurrently(t *testing.T) {
	seen := make(chan string, 2)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		chatID := r.Form.Get("chat_id")
		seen <- chatID
		if chatID == "slow" {
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer api.Close()

	client, err := New(Config{
		Enabled:  true,
		BotToken: "token",
		ChatIDs:  []string{"slow", "good"},
		APIBase:  api.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	event, err := events.ParseWebhookPayload([]byte(`{"Status":"connected","ID":"abc","HostName":"u.c","Timestamp":"2026-06-09T12:00:00Z"}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := client.NotifyEvent(ctx, event); err == nil {
		t.Fatal("expected slow recipient to fail")
	}

	got := make(map[string]bool)
	deadline := time.After(time.Second)
	for len(got) < 2 {
		select {
		case chatID := <-seen:
			got[chatID] = true
		case <-deadline:
			t.Fatalf("not all recipients were attempted, seen=%v", got)
		}
	}
	if !got["slow"] || !got["good"] {
		t.Fatalf("expected both recipients to be attempted, seen=%v", got)
	}
}

func TestSendMessageRedactsTokenFromRequestError(t *testing.T) {
	token := "123456:secret-token"
	client, err := New(Config{
		Enabled:  true,
		BotToken: token,
		ChatIDs:  []string{"123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	client.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("failed posting to %s", req.URL.String())
	})

	err = client.SendMessage(context.Background(), "123", "hello")
	if err == nil {
		t.Fatal("expected request error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error leaked bot token: %v", err)
	}
	if !strings.Contains(err.Error(), redactedTokenMarker) {
		t.Fatalf("error did not include redaction marker: %v", err)
	}
}

func TestSendMessageRejectsTelegramOKFalse(t *testing.T) {
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
	}))
	defer api.Close()

	client, err := New(Config{
		Enabled:  true,
		BotToken: "token",
		ChatIDs:  []string{"123"},
		APIBase:  api.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = client.SendMessage(context.Background(), "123", "hello")
	if err == nil {
		t.Fatal("expected Telegram ok=false response to fail")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("error missing Telegram description: %v", err)
	}
}

func TestSendMessageRetriesTransientStatus(t *testing.T) {
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			writeTelegramError(w, http.StatusInternalServerError, "temporary failure")
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer api.Close()

	client, err := New(Config{
		Enabled:  true,
		BotToken: "token",
		ChatIDs:  []string{"123"},
		APIBase:  api.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := client.SendMessage(context.Background(), "123", "hello"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestSendMessageWithResultReturnsMessageID(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
	}))
	defer api.Close()

	client, err := New(Config{
		Enabled:  true,
		BotToken: "token",
		ChatIDs:  []string{"123"},
		APIBase:  api.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := client.SendMessageWithResult(context.Background(), "123", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if result.MessageID != 42 {
		t.Fatalf("message ID = %d, want 42", result.MessageID)
	}
}

func TestSendFormattedMessageUsesHTMLParseMode(t *testing.T) {
	var gotPath, gotText, gotParseMode string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		gotText = r.Form.Get("text")
		gotParseMode = r.Form.Get("parse_mode")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":7}}`))
	}))
	defer api.Close()

	client, err := New(Config{
		Enabled:  true,
		BotToken: "token",
		ChatIDs:  []string{"123"},
		APIBase:  api.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := client.SendFormattedMessageWithResult(context.Background(), "123", FormattedMessage{
		Plain: "plain alert",
		HTML:  "<b>html alert</b>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MessageID != 7 {
		t.Fatalf("message ID = %d, want 7", result.MessageID)
	}
	if gotPath != "/bottoken/sendMessage" || gotParseMode != "HTML" || gotText != "<b>html alert</b>" {
		t.Fatalf("unexpected request path=%q parse_mode=%q text=%q", gotPath, gotParseMode, gotText)
	}
}

func TestSendFormattedMessageUsesRichMode(t *testing.T) {
	var payload struct {
		ChatID      int64 `json:"chat_id"`
		RichMessage struct {
			HTML                string `json:"html"`
			SkipEntityDetection bool   `json:"skip_entity_detection"`
		} `json:"rich_message"`
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottoken/sendRichMessage" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":9}}`))
	}))
	defer api.Close()

	client, err := New(Config{
		Enabled:   true,
		BotToken:  "token",
		ChatIDs:   []string{"123"},
		APIBase:   api.URL,
		AlertMode: string(AlertModeRich),
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := client.SendFormattedMessageWithResult(context.Background(), "123", FormattedMessage{
		Plain:    "plain alert",
		HTML:     "<b>html alert</b>",
		RichHTML: "<h3>rich alert</h3>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MessageID != 9 {
		t.Fatalf("message ID = %d, want 9", result.MessageID)
	}
	if payload.ChatID != 123 || payload.RichMessage.HTML != "<h3>rich alert</h3>" || !payload.RichMessage.SkipEntityDetection {
		t.Fatalf("unexpected rich payload: %+v", payload)
	}
}

func TestSendFormattedMessageFallsBackWhenRichUnsupported(t *testing.T) {
	paths := make([]string, 0, 2)
	var fallbackText, fallbackParseMode string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/bottoken/sendRichMessage" {
			writeTelegramError(w, http.StatusNotFound, "Not Found")
			return
		}
		if r.URL.Path != "/bottoken/sendMessage" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		fallbackText = r.Form.Get("text")
		fallbackParseMode = r.Form.Get("parse_mode")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":10}}`))
	}))
	defer api.Close()

	client, err := New(Config{
		Enabled:   true,
		BotToken:  "token",
		ChatIDs:   []string{"123"},
		APIBase:   api.URL,
		AlertMode: string(AlertModeRich),
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := client.SendFormattedMessageWithResult(context.Background(), "123", FormattedMessage{
		Plain:    "plain alert",
		HTML:     "<b>html alert</b>",
		RichHTML: "<h3>rich alert</h3>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MessageID != 10 {
		t.Fatalf("message ID = %d, want 10", result.MessageID)
	}
	if len(paths) != 2 || paths[0] != "/bottoken/sendRichMessage" || paths[1] != "/bottoken/sendMessage" {
		t.Fatalf("paths = %#v", paths)
	}
	if fallbackParseMode != "HTML" || fallbackText != "<b>html alert</b>" {
		t.Fatalf("unexpected fallback parse_mode=%q text=%q", fallbackParseMode, fallbackText)
	}
}

func TestNewValidatesEnabledConfig(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		want   string
	}{
		{
			name: "missing token",
			config: Config{
				Enabled: true,
				ChatIDs: []string{"123"},
			},
			want: "bot token",
		},
		{
			name: "missing chat IDs",
			config: Config{
				Enabled:  true,
				BotToken: "token",
				ChatIDs:  []string{" "},
			},
			want: "chat IDs",
		},
		{
			name: "invalid alert mode",
			config: Config{
				Enabled:   true,
				BotToken:  "token",
				ChatIDs:   []string{"123"},
				AlertMode: "fancy",
			},
			want: "alert mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.config)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.want)
			}
		})
	}
}

func TestNewOnlyParsesProxyWhenEnabled(t *testing.T) {
	if _, err := New(Config{ProxyURL: "://proxy-user:proxy-pass@example.com"}); err != nil {
		t.Fatalf("disabled telegram should ignore proxy URL: %v", err)
	}

	_, err := New(Config{
		Enabled:  true,
		BotToken: "token",
		ChatIDs:  []string{"123"},
		ProxyURL: "://proxy-user:proxy-pass@example.com",
	})
	if err == nil {
		t.Fatal("expected invalid proxy error")
	}
	if strings.Contains(err.Error(), "proxy-user") || strings.Contains(err.Error(), "proxy-pass") {
		t.Fatalf("proxy error leaked credentials: %v", err)
	}

	_, err = New(Config{
		Enabled:  true,
		BotToken: "token",
		ChatIDs:  []string{"123"},
		ProxyURL: "proxy.example.com:3128",
	})
	if err == nil {
		t.Fatal("expected proxy URL without scheme to fail")
	}

	_, err = New(Config{
		Enabled:  true,
		BotToken: "token",
		ChatIDs:  []string{"123"},
		ProxyURL: "ftp://proxy.example.com:21",
	})
	if err == nil {
		t.Fatal("expected unsupported proxy URL scheme to fail")
	}
}

func TestNewValidatesAPIBaseWhenEnabled(t *testing.T) {
	tests := []struct {
		name    string
		apiBase string
		wantErr bool
	}{
		{name: "default", apiBase: "", wantErr: false},
		{name: "https", apiBase: "https://botapi.example.com", wantErr: false},
		{name: "loopback http", apiBase: "http://127.0.0.1:8081", wantErr: false},
		{name: "localhost http", apiBase: "http://localhost:8081", wantErr: false},
		{name: "missing scheme", apiBase: "api.telegram.org", wantErr: true},
		{name: "ftp", apiBase: "ftp://api.telegram.org", wantErr: true},
		{name: "remote http", apiBase: "http://botapi.example.com", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(Config{
				Enabled:  true,
				BotToken: "token",
				ChatIDs:  []string{"123"},
				APIBase:  tt.apiBase,
			})
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestFormatEventMessageIncludesFields(t *testing.T) {
	event, err := events.ParseWebhookPayload([]byte(`{"Status":"disconnected","ID":"abc","IP":"192.0.2.1:5555","HostName":"user.host","Version":"v1","Timestamp":"2026-06-09T12:00:00Z"}`), time.Date(2026, 6, 9, 12, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	message := FormatEventMessage(event)
	for _, want := range []string{"DISCONNECTED", "user: user", "computer: host", "ip: 192.0.2.1:5555"} {
		if !strings.Contains(message, want) {
			t.Fatalf("message missing %q: %s", want, message)
		}
	}
}

func TestFormatEnrichedEventAlertIncludesRoutingContextAndEscapesHTML(t *testing.T) {
	message := FormatEnrichedEventAlert(events.EnrichedEvent{
		SourceEventHash:   "1234567890abcdef",
		CorrelationStatus: "matched",
		CorrelationMethod: "nearest_time",
		Status:            "connected",
		ReverseSSHID:      "abcdef1234567890abcdef",
		HostName:          "user.<host>",
		UserName:          "user",
		ComputerName:      "<host>",
		IPRaw:             "192.0.2.1:5555",
		IPAddr:            "192.0.2.1",
		IPPort:            5555,
		RealClientIP:      "198.51.100.10",
		ClientPort:        53000,
		Transport:         "wss",
		VPSName:           "edge-1",
		VPSPublicIP:       "203.0.113.20",
		ReceivedAt:        time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC),
	})
	for _, want := range []string{
		"real_ip: 198.51.100.10:53000",
		"via: wss / edge-1",
		"id: abcdef123456...abcdef",
		"time: 2026-07-08 12:00:00Z",
		"alert_id: 1234567890ab",
	} {
		if !strings.Contains(message.Plain, want) {
			t.Fatalf("plain message missing %q:\n%s", want, message.Plain)
		}
	}
	if !strings.Contains(message.HTML, "user.&lt;host&gt;") {
		t.Fatalf("html message did not escape host: %s", message.HTML)
	}
	for _, want := range []string{"<ul>", "<li><b>real_ip</b> <code>198.51.100.10:53000</code></li>", "<li><b>via</b> <code>wss / edge-1</code></li>"} {
		if !strings.Contains(message.RichHTML, want) {
			t.Fatalf("rich message missing %q: %s", want, message.RichHTML)
		}
	}
	if strings.Contains(message.RichHTML, "<blockquote>") || strings.Contains(message.RichHTML, "<table") {
		t.Fatalf("rich message should avoid quote/table containers: %s", message.RichHTML)
	}
}

func TestFormatReverseSSHErrorMessageIncludesFields(t *testing.T) {
	event, err := events.NormalizeReverseSSHErrorEvent(events.ReverseSSHErrorEvent{
		Source:     "journalctl",
		Unit:       "reverse_ssh",
		Message:    "public key fingerprint mismatch from 198.51.100.10:53000",
		RemoteAddr: "198.51.100.10:53000",
		ObservedAt: time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC),
	}, time.Date(2026, 7, 8, 12, 0, 1, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	message := FormatReverseSSHErrorMessage(event)
	for _, want := range []string{"reverse_ssh ERROR", "reason: fingerprint_mismatch", "remote: 198.51.100.10:53000", "source: journalctl", "unit: reverse_ssh", "alert_id: " + ReverseSSHErrorAlertID(event)} {
		if !strings.Contains(message, want) {
			t.Fatalf("message missing %q: %s", want, message)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func writeTelegramError(w http.ResponseWriter, status int, description string) {
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"ok":false,"description":%q}`, description)
}
