package events

import (
	"testing"
	"time"
)

func TestParseWebhookPayloadRaw(t *testing.T) {
	body := []byte(`{"Status":"connected","ID":"abc123","IP":"203.0.113.10:51444","HostName":"alice.workstation","Version":"v2","Timestamp":"2026-06-09T12:00:00Z"}`)

	event, err := ParseWebhookPayload(body, time.Date(2026, 6, 9, 12, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	if event.Status != "connected" {
		t.Fatalf("status = %q", event.Status)
	}
	if event.UserName != "alice" || event.ComputerName != "workstation" {
		t.Fatalf("hostname parsed as user=%q computer=%q", event.UserName, event.ComputerName)
	}
	if event.IPAddr != "203.0.113.10" || event.IPPort != 51444 {
		t.Fatalf("endpoint parsed as addr=%q port=%d", event.IPAddr, event.IPPort)
	}
	if event.EventHash == "" {
		t.Fatal("event hash is empty")
	}
}

func TestParseWebhookPayloadWrapper(t *testing.T) {
	body := []byte(`{"Full":"{\"Status\":\"disconnected\",\"ID\":\"id-1\",\"IP\":\"[2001:db8::1]:4444\",\"HostName\":\"bob.laptop.office\",\"Version\":\"v3\",\"Timestamp\":\"2026-06-09T12:00:00Z\"}","text":"bob.laptop.office id-1 v3 disconnected"}`)

	event, err := ParseWebhookPayload(body, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	if event.Status != "disconnected" {
		t.Fatalf("status = %q", event.Status)
	}
	if event.UserName != "bob" || event.ComputerName != "laptop.office" {
		t.Fatalf("hostname parsed as user=%q computer=%q", event.UserName, event.ComputerName)
	}
	if event.IPAddr != "2001:db8::1" || event.IPPort != 4444 {
		t.Fatalf("endpoint parsed as addr=%q port=%d", event.IPAddr, event.IPPort)
	}
}

func TestParseWebhookPayloadMalformed(t *testing.T) {
	_, err := ParseWebhookPayload([]byte(`{"Full":"not-json"}`), time.Now())
	if err == nil {
		t.Fatal("expected malformed wrapper payload to fail")
	}
}

func TestParseWebhookPayloadRejectsMissingRequiredFields(t *testing.T) {
	tests := [][]byte{
		[]byte(`{}`),
		[]byte(`{"Status":"connected","HostName":"u.c"}`),
		[]byte(`{"Status":"connected","ID":"abc"}`),
		[]byte(`{"Full":"{}"}`),
	}

	for _, body := range tests {
		if _, err := ParseWebhookPayload(body, time.Now()); err == nil {
			t.Fatalf("expected %s to fail", string(body))
		}
	}
}

func TestParseHostName(t *testing.T) {
	tests := []struct {
		input    string
		user     string
		computer string
	}{
		{"alice.pc", "alice", "pc"},
		{"alice.pc.lab", "alice", "pc.lab"},
		{"singlehost", "", "singlehost"},
		{"", "", ""},
	}

	for _, tt := range tests {
		user, computer := ParseHostName(tt.input)
		if user != tt.user || computer != tt.computer {
			t.Fatalf("ParseHostName(%q) = %q, %q", tt.input, user, computer)
		}
	}
}

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		input string
		host  string
		port  int
	}{
		{"192.0.2.10:3232", "192.0.2.10", 3232},
		{"[2001:db8::1]:3232", "2001:db8::1", 3232},
		{"2001:db8::1", "2001:db8::1", 0},
		{"", "", 0},
	}

	for _, tt := range tests {
		host, port := ParseEndpoint(tt.input)
		if host != tt.host || port != tt.port {
			t.Fatalf("ParseEndpoint(%q) = %q, %d", tt.input, host, port)
		}
	}
}
