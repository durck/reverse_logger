package edgeapp

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/durck/reverse_logger/internal/store"
)

func TestProxyConnectionLimit(t *testing.T) {
	proxy := NewProxy(Config{MaxConnections: 1, MaxAcceptRate: 100}, nil)

	if !proxy.acquireSlot() {
		t.Fatal("first slot was rejected")
	}
	if proxy.acquireSlot() {
		t.Fatal("second slot exceeded connection limit")
	}
	proxy.releaseSlot()
	if !proxy.acquireSlot() {
		t.Fatal("slot was not released")
	}
}

func TestTokenBucketRateLimits(t *testing.T) {
	bucket := newTokenBucket(1)

	if !bucket.Allow() {
		t.Fatal("first token was rejected")
	}
	if bucket.Allow() {
		t.Fatal("second token exceeded rate limit")
	}

	bucket.mu.Lock()
	bucket.last = bucket.last.Add(-time.Second)
	bucket.mu.Unlock()

	if !bucket.Allow() {
		t.Fatal("token was not refilled")
	}
}

func TestLoadConfigRequiresForwardURLWhenEnabled(t *testing.T) {
	t.Setenv("EDGE_TARGET_ADDR", "192.0.2.10:3232")
	t.Setenv("EDGE_FORWARD_ENABLED", "true")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected missing EDGE_FORWARD_URL to fail")
	}
}

func TestProxyServeRejectsWhenConnectionLimitReached(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	listener := &singleConnListener{conn: serverConn}
	proxy := NewProxy(Config{TargetAddr: "127.0.0.1:1", MaxConnections: 1, MaxAcceptRate: 100}, nil)
	if !proxy.acquireSlot() {
		t.Fatal("failed to reserve initial slot")
	}
	defer proxy.releaseSlot()

	if err := proxy.Serve(listener); !errors.Is(err, errListenerDone) {
		t.Fatalf("Serve error = %v", err)
	}

	_ = clientConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	buf := make([]byte, 1)
	_, err := clientConn.Read(buf)
	if err == nil {
		t.Fatal("limited connection stayed open")
	}
}

func TestProxyServeCopiesTraffic(t *testing.T) {
	targetListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer targetListener.Close()

	targetDone := make(chan error, 1)
	go func() {
		conn, err := targetListener.Accept()
		if err != nil {
			targetDone <- err
			return
		}
		defer conn.Close()
		_, err = io.Copy(conn, conn)
		targetDone <- err
	}()

	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxyListener.Close()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	proxy := NewProxy(Config{
		TargetAddr:     targetListener.Addr().String(),
		VPSName:        "test-vps",
		VPSPublicIP:    "203.0.113.20",
		VPSPort:        3232,
		MaxConnections: 4,
		MaxAcceptRate:  100,
		DialTimeout:    time.Second,
		IdleTimeout:    time.Second,
	}, st)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- proxy.Serve(proxyListener)
	}()

	client, err := net.Dial("tcp", proxyListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(client, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo = %q", string(buf))
	}

	_ = client.Close()
	_ = proxyListener.Close()
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("proxy Serve did not stop after listener close")
	}
	select {
	case <-targetDone:
	case <-time.After(time.Second):
		t.Fatal("target connection did not close")
	}
}

var errListenerDone = errors.New("listener done")

type singleConnListener struct {
	conn     net.Conn
	returned bool
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	if l.returned {
		return nil, errListenerDone
	}
	l.returned = true
	return l.conn, nil
}

func (l *singleConnListener) Close() error {
	return nil
}

func (l *singleConnListener) Addr() net.Addr {
	return dummyAddr("single-listener")
}

type dummyAddr string

func (a dummyAddr) Network() string {
	return "test"
}

func (a dummyAddr) String() string {
	return string(a)
}
