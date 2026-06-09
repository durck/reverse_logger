package telegram

import (
	"context"
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
