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

func TestNormalizeIngressEvent(t *testing.T) {
	event, err := NormalizeIngressEvent(IngressEvent{
		Transport:       "WSS",
		VPSName:         "vps-1",
		VPSInternalIP:   "192.0.2.2",
		ClientIP:        "198.51.100.10",
		ClientPort:      5555,
		URI:             "/ws",
		Method:          "get",
		Upgrade:         "websocket",
		NginxReceivedAt: time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC),
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if event.Transport != "wss" || event.Method != "GET" {
		t.Fatalf("normalization failed: transport=%q method=%q", event.Transport, event.Method)
	}
	if event.EventHash == "" {
		t.Fatal("event hash is empty")
	}
}

func TestNormalizeIngressEventRejectsInvalidIP(t *testing.T) {
	_, err := NormalizeIngressEvent(IngressEvent{
		Transport: "https",
		VPSName:   "vps-1",
		ClientIP:  "bad-ip",
		URI:       "/push",
		Method:    "HEAD",
	}, time.Now())
	if err == nil {
		t.Fatal("expected invalid client_ip to fail")
	}
}

func TestParseWebhookPayloadOptionalTransportFields(t *testing.T) {
	body := []byte(`{"Status":"connected","ID":"abc","IP":"198.51.100.10:0","HostName":"u.c","Version":"SSH-test","Transport":"wss","PublicKeyFingerprint":"fp","ProxySourceIP":"192.0.2.10"}`)
	event, err := ParseWebhookPayload(body, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if event.Transport != "wss" {
		t.Fatalf("transport = %q", event.Transport)
	}
	if event.PublicKeyFingerprint != "fp" {
		t.Fatalf("public key fingerprint = %q", event.PublicKeyFingerprint)
	}
	if event.ProxySourceIP != "192.0.2.10" {
		t.Fatalf("proxy source ip = %q", event.ProxySourceIP)
	}
}

func TestNormalizeIngressEventRejectsMalformedTransportShape(t *testing.T) {
	tests := []IngressEvent{
		{
			Transport:      "wss",
			VPSName:        "vps-1",
			VPSInternalIP:  "192.0.2.2",
			ClientIP:       "198.51.100.10",
			URI:            "/ws",
			Method:         "GET",
			PollingKeySHA1: "",
		},
		{
			Transport: "https",
			VPSName:   "vps-1",
			ClientIP:  "198.51.100.10",
			URI:       "/push",
			Method:    "HEAD",
		},
		{
			Transport:      "https",
			VPSName:        "vps-1",
			ClientIP:       "198.51.100.10",
			URI:            "/push",
			Method:         "HEAD",
			PollingKeySHA1: "not-a-sha1",
		},
	}
	for _, event := range tests {
		if _, err := NormalizeIngressEvent(event, time.Now()); err == nil {
			t.Fatalf("expected malformed event to fail: %+v", event)
		}
	}
}

func TestValidateIngressRouteRejectsWrongPath(t *testing.T) {
	tests := []IngressEvent{
		{Transport: "wss", URI: "/not-ws"},
		{Transport: "https", URI: "/not-push?key=abc"},
	}
	for _, event := range tests {
		if err := ValidateIngressRoute(event, "/ws", "/push"); err == nil {
			t.Fatalf("expected wrong path to fail: %+v", event)
		}
	}
}

func TestValidateIngressRouteAcceptsConfiguredPath(t *testing.T) {
	tests := []IngressEvent{
		{Transport: "wss", URI: "/custom-ws"},
		{Transport: "https", URI: "/custom-push?key=abc"},
	}
	for _, event := range tests {
		if err := ValidateIngressRoute(event, "/custom-ws", "/custom-push"); err != nil {
			t.Fatalf("expected configured path to pass: %+v err=%v", event, err)
		}
	}
}
