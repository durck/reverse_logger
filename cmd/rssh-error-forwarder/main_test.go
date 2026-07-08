package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/durck/reverse_logger/internal/events"
)

func TestRunForwardsClassifiedStderrLines(t *testing.T) {
	received := make(chan events.ReverseSSHErrorEvent, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reverse-ssh-errors/token" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var event events.ReverseSSHErrorEvent
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Fatal(err)
		}
		received <- event
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer server.Close()

	command := "printf '%s\n' 'Multiplexing failed (unwrapping): initial determination: unknown protocol' >&2"
	if runtime.GOOS == "windows" {
		command = "echo Multiplexing failed (unwrapping): initial determination: unknown protocol 1>&2"
	}
	cfg := config{
		ForwardURL: server.URL + "/reverse-ssh-errors",
		Token:      "token",
		Unit:       "reverse_ssh",
		Source:     "test",
		Command:    command,
		Timeout:    time.Second,
	}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-received:
		if event.Reason != "malformed_probe" {
			t.Fatalf("reason = %q", event.Reason)
		}
		if event.Severity != "info" {
			t.Fatalf("severity = %q", event.Severity)
		}
		if !strings.Contains(event.Message, "Multiplexing failed") {
			t.Fatalf("message = %q", event.Message)
		}
	default:
		t.Fatal("stderr line was not forwarded")
	}
}
