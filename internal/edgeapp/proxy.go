package edgeapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/durck/reverse_logger/internal/events"
	"github.com/durck/reverse_logger/internal/store"
)

type Proxy struct {
	config Config
	store  *store.Store
	client *http.Client
	slots  chan struct{}
	accept *tokenBucket
}

func NewProxy(config Config, store *store.Store) *Proxy {
	if config.DialTimeout == 0 {
		config.DialTimeout = 10 * time.Second
	}
	if config.IdleTimeout == 0 {
		config.IdleTimeout = 10 * time.Minute
	}
	if config.MaxConnections <= 0 {
		config.MaxConnections = 1024
	}
	if config.MaxAcceptRate <= 0 {
		config.MaxAcceptRate = 100
	}
	timeout := config.ForwardTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	proxy := &Proxy{
		config: config,
		store:  store,
		client: &http.Client{Timeout: timeout},
	}
	if config.MaxConnections > 0 {
		proxy.slots = make(chan struct{}, config.MaxConnections)
	}
	if config.MaxAcceptRate > 0 {
		proxy.accept = newTokenBucket(config.MaxAcceptRate)
	}
	return proxy
}

func (p *Proxy) Serve(listener net.Listener) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		if p.accept != nil && !p.accept.Allow() {
			log.Printf("edge connection rejected by rate limit: %s", conn.RemoteAddr())
			_ = conn.Close()
			continue
		}
		if !p.acquireSlot() {
			log.Printf("edge connection rejected by connection limit: %s", conn.RemoteAddr())
			_ = conn.Close()
			continue
		}
		go p.handle(conn)
	}
}

func (p *Proxy) handle(client net.Conn) {
	defer client.Close()
	defer p.releaseSlot()

	clientIP, clientPort := remoteHostPort(client.RemoteAddr())
	event := events.NewEdgeEvent(
		p.config.VPSName,
		p.config.VPSPublicIP,
		p.config.VPSPort,
		clientIP,
		clientPort,
		time.Now(),
		nil,
	)

	if _, err := p.store.InsertEdgeEvent(event); err != nil {
		log.Printf("edge event store failed: %v", err)
	}
	if p.config.ForwardEnabled {
		go func() {
			if err := p.forwardEvent(context.Background(), event); err != nil {
				log.Printf("edge event forward failed: %v", err)
			}
		}()
	}

	dialTimeout := p.config.DialTimeout
	if dialTimeout == 0 {
		dialTimeout = 10 * time.Second
	}
	target, err := net.DialTimeout("tcp", p.config.TargetAddr, dialTimeout)
	if err != nil {
		log.Printf("dial target %s failed: %v", p.config.TargetAddr, err)
		return
	}
	defer target.Close()

	errCh := make(chan error, 2)
	go copyConn(errCh, target, client, p.config.IdleTimeout)
	go copyConn(errCh, client, target, p.config.IdleTimeout)
	<-errCh
}

func (p *Proxy) acquireSlot() bool {
	if p.slots == nil {
		return true
	}
	select {
	case p.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (p *Proxy) releaseSlot() {
	if p.slots == nil {
		return
	}
	select {
	case <-p.slots:
	default:
	}
}

func (p *Proxy) forwardEvent(ctx context.Context, event events.EdgeEvent) error {
	endpoint := p.forwardEndpoint()
	if endpoint == "" {
		return errors.New("edge forward endpoint is empty")
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("central edge event endpoint returned status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (p *Proxy) forwardEndpoint() string {
	endpoint := strings.TrimRight(strings.TrimSpace(p.config.ForwardURL), "/")
	token := strings.TrimSpace(p.config.ForwardToken)
	if endpoint == "" {
		return ""
	}
	if token != "" && !strings.HasSuffix(endpoint, "/"+token) {
		endpoint += "/" + token
	}
	return endpoint
}

func remoteHostPort(addr net.Addr) (string, int) {
	if tcp, ok := addr.(*net.TCPAddr); ok {
		return tcp.IP.String(), tcp.Port
	}
	host, portRaw, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String(), 0
	}
	port, _ := net.LookupPort("tcp", portRaw)
	return host, port
}

func copyConn(errCh chan<- error, dst net.Conn, src net.Conn, idleTimeout time.Duration) {
	var err error
	if idleTimeout <= 0 {
		_, err = io.Copy(dst, src)
	} else {
		err = copyWithIdleDeadline(dst, src, idleTimeout)
	}
	_ = dst.Close()
	_ = src.Close()
	errCh <- err
}

func copyWithIdleDeadline(dst net.Conn, src net.Conn, idleTimeout time.Duration) error {
	buf := make([]byte, 32*1024)
	for {
		_ = src.SetReadDeadline(time.Now().Add(idleTimeout))
		n, readErr := src.Read(buf)
		if n > 0 {
			_ = dst.SetWriteDeadline(time.Now().Add(idleTimeout))
			written := 0
			for written < n {
				m, writeErr := dst.Write(buf[written:n])
				written += m
				if writeErr != nil {
					return writeErr
				}
				if m == 0 {
					return io.ErrShortWrite
				}
			}
		}
		if readErr != nil {
			return readErr
		}
	}
}

type tokenBucket struct {
	mu       sync.Mutex
	rate     int
	capacity int
	tokens   int
	last     time.Time
}

func newTokenBucket(rate int) *tokenBucket {
	return &tokenBucket{
		rate:     rate,
		capacity: rate,
		tokens:   rate,
		last:     time.Now(),
	}
}

func (b *tokenBucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	if b.last.IsZero() {
		b.last = now
	}
	refill := int(now.Sub(b.last) * time.Duration(b.rate) / time.Second)
	if refill > 0 {
		b.tokens += refill
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.last = b.last.Add(time.Duration(refill) * time.Second / time.Duration(b.rate))
	}
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}
