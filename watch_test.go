package configsdk

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sseEvent(data string) string {
	return fmt.Sprintf("data: %s\n\n", data)
}

func TestWatch_ReceivesEvents(t *testing.T) {
	event1 := ConfigChangeEvent{ConfigName: "app", Version: 1, ChangedBy: 10}
	event2 := ConfigChangeEvent{ConfigName: "app", Version: 2, ChangedBy: 11}

	e1, _ := json.Marshal(event1)
	e2, _ := json.Marshal(event2)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, sseEvent(string(e1)))
		flusher.Flush()
		fmt.Fprint(w, sseEvent(string(e2)))
		flusher.Flush()
	}))
	defer srv.Close()

	client, _ := New(Options{
		Host:          srv.URL,
		ServiceToken:  "token",
		EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		HTTPClient:    srv.Client(),
		RetryDelay:    50 * time.Millisecond,
	})

	var mu sync.Mutex
	var received []ConfigChangeEvent

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := client.Watch(ctx, "app", func(event ConfigChangeEvent) {
		mu.Lock()
		received = append(received, event)
		if len(received) >= 2 {
			cancel()
		}
		mu.Unlock()
	})

	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Watch: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Fatalf("received %d events, want 2", len(received))
	}
	if received[0].Version != 1 {
		t.Errorf("event[0].Version = %d, want 1", received[0].Version)
	}
	if received[1].Version != 2 {
		t.Errorf("event[1].Version = %d, want 2", received[1].Version)
	}
}

func TestWatch_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Keep connection open until client disconnects
		<-r.Context().Done()
	}))
	defer srv.Close()

	client, _ := New(Options{
		Host:          srv.URL,
		ServiceToken:  "token",
		EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		HTTPClient:    srv.Client(),
		RetryDelay:    time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := client.Watch(ctx, "app", func(event ConfigChangeEvent) {})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want context.DeadlineExceeded", err)
	}
}

func TestWatch_ReconnectsOnDrop(t *testing.T) {
	var connectCount int32
	event := ConfigChangeEvent{ConfigName: "svc", Version: 1}
	eJSON, _ := json.Marshal(event)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&connectCount, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if n <= 2 {
			// Close immediately to simulate disconnect
			return
		}
		// Third connection sends an event
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, sseEvent(string(eJSON)))
		flusher.Flush()
	}))
	defer srv.Close()

	client, _ := New(Options{
		Host:          srv.URL,
		ServiceToken:  "token",
		EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		HTTPClient:    srv.Client(),
		RetryDelay:    10 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	gotEvent := make(chan struct{}, 1)
	err := client.Watch(ctx, "svc", func(event ConfigChangeEvent) {
		select {
		case gotEvent <- struct{}{}:
		default:
		}
		cancel()
	})

	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Watch: %v", err)
	}

	select {
	case <-gotEvent:
	default:
		t.Fatal("never received event after reconnects")
	}

	if n := atomic.LoadInt32(&connectCount); n < 3 {
		t.Errorf("connectCount = %d, want >= 3", n)
	}
}

func TestWatch_OnErrorCallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Send invalid JSON event
		fmt.Fprint(w, sseEvent("not valid json"))
	}))
	defer srv.Close()

	var onErrorCalled int32

	client, _ := New(Options{
		Host:          srv.URL,
		ServiceToken:  "token",
		EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		HTTPClient:    srv.Client(),
		RetryDelay:    10 * time.Millisecond,
		OnError: func(err error) {
			atomic.AddInt32(&onErrorCalled, 1)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_ = client.Watch(ctx, "app", func(event ConfigChangeEvent) {})

	if n := atomic.LoadInt32(&onErrorCalled); n < 1 {
		t.Error("OnError was never called")
	}
}

func TestWatch_OnChangeCallback(t *testing.T) {
	event := ConfigChangeEvent{ConfigName: "my-svc", Version: 5}
	eJSON, _ := json.Marshal(event)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, sseEvent(string(eJSON)))
		flusher.Flush()
	}))
	defer srv.Close()

	var changedName atomic.Value

	client, _ := New(Options{
		Host:          srv.URL,
		ServiceToken:  "token",
		EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		HTTPClient:    srv.Client(),
		RetryDelay:    10 * time.Millisecond,
		OnChange: func(name string) {
			changedName.Store(name)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = client.Watch(ctx, "my-svc", func(event ConfigChangeEvent) {
		cancel()
	})

	if v := changedName.Load(); v == nil || v.(string) != "my-svc" {
		t.Errorf("OnChange name = %v, want %q", v, "my-svc")
	}
}

func TestClose_StopsWatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		<-r.Context().Done()
	}))
	defer srv.Close()

	client, _ := New(Options{
		Host:          srv.URL,
		ServiceToken:  "token",
		EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		HTTPClient:    srv.Client(),
		RetryDelay:    time.Millisecond,
	})

	done := make(chan error, 1)
	go func() {
		done <- client.Watch(context.Background(), "app", func(event ConfigChangeEvent) {})
	}()

	time.Sleep(50 * time.Millisecond)
	client.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error from Watch after Close")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Watch did not stop after Close")
	}
}

func TestWatchAndDecode_UpdatesDst(t *testing.T) {
	keyHex := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	key, _ := hex.DecodeString(keyHex)

	type AppConfig struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}

	configJSON := `{"host":"updated.example.com","port":9090}`
	encrypted, _ := encrypt([]byte(configJSON), key)

	event := ConfigChangeEvent{ConfigName: "app", Version: 2}
	eJSON, _ := json.Marshal(event)

	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/service/configs/app/watch" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher, _ := w.(http.Flusher)
			fmt.Fprint(w, sseEvent(string(eJSON)))
			flusher.Flush()
			// Keep alive briefly then close
			time.Sleep(200 * time.Millisecond)
			return
		}
		// GET config endpoint
		atomic.AddInt32(&reqCount, 1)
		w.WriteHeader(http.StatusOK)
		w.Write(encrypted)
	}))
	defer srv.Close()

	client, _ := New(Options{
		Host:          srv.URL,
		ServiceToken:  "token",
		EncryptionKey: keyHex,
		HTTPClient:    srv.Client(),
		RetryDelay:    10 * time.Millisecond,
	})

	var cfg AppConfig

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- client.WatchAndDecode(ctx, "app", &cfg)
	}()

	// Wait for the config to be fetched
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			cancel()
			t.Fatal("timeout waiting for WatchAndDecode to update cfg")
		default:
		}
		if atomic.LoadInt32(&reqCount) > 0 {
			time.Sleep(50 * time.Millisecond) // let decode finish
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	<-done

	if cfg.Host != "updated.example.com" {
		t.Errorf("Host = %q, want %q", cfg.Host, "updated.example.com")
	}
	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want %d", cfg.Port, 9090)
	}
}

func TestWatchAndDecode_NilPointer(t *testing.T) {
	client, _ := New(Options{
		Host:          "http://localhost",
		ServiceToken:  "token",
		EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})

	err := client.WatchAndDecode(context.Background(), "app", nil)
	if err == nil {
		t.Fatal("expected error for nil dst")
	}
}

// --- helpers for the security-review tests ---

func newWatchTestClient(t *testing.T, srv *httptest.Server, mod func(*Options)) *Client {
	t.Helper()
	o := Options{
		Host: srv.URL, ServiceToken: "token", EncryptionKey: testKeyHex,
		HTTPClient: srv.Client(), RetryCount: 1, RetryDelay: time.Millisecond,
	}
	if mod != nil {
		mod(&o)
	}
	c, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// startStream writes SSE headers and flushes them so the client's Do returns.
func startStream(w http.ResponseWriter) http.Flusher {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	f := w.(http.Flusher)
	f.Flush()
	return f
}

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
	}
}

const evJSON = `{"config_name":"app","version":1}`

func TestWatch_AuthError(t *testing.T) {
	// Terminal errors: Watch returns the sentinel after exactly one request,
	// calling OnError exactly once.
	tests := []struct {
		code int
		want error
	}{
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusForbidden, ErrForbidden},
		{http.StatusNotFound, ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.code), func(t *testing.T) {
			var hits, onErr int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&hits, 1)
				w.WriteHeader(tt.code)
			}))
			defer srv.Close()
			c := newWatchTestClient(t, srv, func(o *Options) {
				o.OnError = func(error) { atomic.AddInt32(&onErr, 1) }
			})

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := c.Watch(ctx, "app", func(ConfigChangeEvent) {})
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
			if ctx.Err() != nil {
				t.Fatal("Watch did not return before the deadline")
			}
			if n := atomic.LoadInt32(&hits); n != 1 {
				t.Errorf("hits = %d, want 1", n)
			}
			if n := atomic.LoadInt32(&onErr); n != 1 {
				t.Errorf("OnError calls = %d, want 1", n)
			}
		})
	}
}

func TestWatch_RetriesNonTerminal(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch atomic.AddInt32(&hits, 1) {
		case 1:
			w.WriteHeader(http.StatusInternalServerError)
		case 2:
			w.WriteHeader(http.StatusBadRequest)
		default:
			startStream(w).Flush()
			fmt.Fprint(w, sseEvent(evJSON))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
	}))
	defer srv.Close()
	c := newWatchTestClient(t, srv, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := make(chan struct{}, 1)
	_ = c.Watch(ctx, "app", func(ConfigChangeEvent) {
		got <- struct{}{}
		cancel()
	})
	waitSignal(t, got, "event after retries")
	if n := atomic.LoadInt32(&hits); n < 3 {
		t.Errorf("hits = %d, want >= 3", n)
	}
}

func TestWatch_NoRedirect(t *testing.T) {
	var hitsB int32
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hitsB, 1)
	}))
	defer b.Close()
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, b.URL, http.StatusFound)
	}))
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	c := newWatchTestClient(t, a, func(o *Options) {
		o.OnError = func(err error) {
			select {
			case errCh <- err:
			default:
			}
			cancel()
		}
	})

	done := make(chan error, 1)
	go func() { done <- c.Watch(ctx, "app", func(ConfigChangeEvent) {}) }()

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("OnError got %v, want ErrInvalidResponse", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnError never called")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Watch returned %v, want context.Canceled (3xx is non-terminal)", err)
	}
	if n := atomic.LoadInt32(&hitsB); n != 0 {
		t.Fatalf("redirect target hit %d times", n)
	}
}

func TestWatch_PathEscaped(t *testing.T) {
	tests := []struct{ name, wantURI string }{
		{"../admin", "/api/v1/service/configs/..%2Fadmin/watch"},
		{"x?foo=1", "/api/v1/service/configs/x%3Ffoo=1/watch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uriCh := make(chan string, 1)
			queryCh := make(chan string, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case uriCh <- r.RequestURI:
					queryCh <- r.URL.RawQuery
				default:
				}
				w.WriteHeader(http.StatusNotFound) // terminal: Watch returns
			}))
			defer srv.Close()
			c := newWatchTestClient(t, srv, nil)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = c.Watch(ctx, tt.name, func(ConfigChangeEvent) {})

			if got := <-uriCh; got != tt.wantURI {
				t.Errorf("RequestURI = %q, want %q", got, tt.wantURI)
			}
			if got := <-queryCh; got != "" {
				t.Errorf("RawQuery = %q, want empty", got)
			}
		})
	}
}

func TestWatch_EventTooLarge(t *testing.T) {
	var conns int32
	second := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f := startStream(w)
		if atomic.AddInt32(&conns, 1) == 1 {
			fmt.Fprint(w, "data: "+strings.Repeat("x", 70000)+"\n\n")
			f.Flush()
			return
		}
		close(second)
		<-r.Context().Done()
	}))
	defer srv.Close()

	errCh := make(chan error, 1)
	var cbCalls int32
	c := newWatchTestClient(t, srv, func(o *Options) {
		o.OnError = func(err error) {
			select {
			case errCh <- err:
			default:
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- c.Watch(ctx, "app", func(ConfigChangeEvent) { atomic.AddInt32(&cbCalls, 1) })
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrResponseTooLarge) {
			t.Fatalf("OnError got %v, want ErrResponseTooLarge", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnError never called")
	}
	waitSignal(t, second, "reconnect")
	cancel()
	<-done
	if n := atomic.LoadInt32(&cbCalls); n != 0 {
		t.Errorf("callback called %d times", n)
	}
}

func TestWatch_IdleReconnects(t *testing.T) {
	conns := make(chan struct{}, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startStream(w)
		conns <- struct{}{}
		<-r.Context().Done()
	}))
	defer srv.Close()
	var onErr int32
	c := newWatchTestClient(t, srv, func(o *Options) {
		o.WatchIdleTimeout = 50 * time.Millisecond
		o.OnError = func(error) { atomic.AddInt32(&onErr, 1) }
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Watch(ctx, "app", func(ConfigChangeEvent) {}) }()

	waitSignal(t, conns, "first connection")
	waitSignal(t, conns, "idle reconnect")
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if n := atomic.LoadInt32(&onErr); n != 0 {
		t.Errorf("OnError calls = %d, want 0 for idle reconnect", n)
	}
}

func TestWatch_IdleBeforeHeaders(t *testing.T) {
	conns := make(chan struct{}, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conns <- struct{}{}
		<-r.Context().Done() // never writes headers
	}))
	defer srv.Close()
	c := newWatchTestClient(t, srv, func(o *Options) { o.WatchIdleTimeout = 50 * time.Millisecond })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Watch(ctx, "app", func(ConfigChangeEvent) {}) }()

	waitSignal(t, conns, "first connection")
	waitSignal(t, conns, "reconnect after header stall")
	cancel()
	<-done
}

func TestWatch_HeartbeatKeepsAlive(t *testing.T) {
	var conns int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&conns, 1)
		f := startStream(w)
		for i := 0; i < 40; i++ {
			fmt.Fprint(w, ": ping\n\n")
			f.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
		fmt.Fprint(w, sseEvent(evJSON))
		f.Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := newWatchTestClient(t, srv, func(o *Options) { o.WatchIdleTimeout = 500 * time.Millisecond })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got := make(chan struct{}, 1)
	_ = c.Watch(ctx, "app", func(ConfigChangeEvent) {
		got <- struct{}{}
		cancel()
	})
	waitSignal(t, got, "event")
	if n := atomic.LoadInt32(&conns); n != 1 {
		t.Errorf("connections = %d, want 1", n)
	}
}

func TestWatch_SlowCallbackNoIdle(t *testing.T) {
	var conns int32
	proceed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&conns, 1)
		f := startStream(w)
		fmt.Fprint(w, sseEvent(evJSON))
		f.Flush()
		select {
		case <-proceed:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, sseEvent(evJSON))
		f.Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := newWatchTestClient(t, srv, func(o *Options) { o.WatchIdleTimeout = 50 * time.Millisecond })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var n int32
	_ = c.Watch(ctx, "app", func(ConfigChangeEvent) {
		if atomic.AddInt32(&n, 1) == 1 {
			time.Sleep(200 * time.Millisecond) // slow callback, 4x the idle timeout
			close(proceed)
			return
		}
		cancel()
	})
	if got := atomic.LoadInt32(&n); got != 2 {
		t.Fatalf("events = %d, want 2", got)
	}
	if got := atomic.LoadInt32(&conns); got != 1 {
		t.Errorf("connections = %d, want 1", got)
	}
}

func TestWatch_IdleDisabled(t *testing.T) {
	var conns int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&conns, 1)
		startStream(w)
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := newWatchTestClient(t, srv, func(o *Options) { o.WatchIdleTimeout = -1 })
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := c.Watch(ctx, "app", func(ConfigChangeEvent) {})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if n := atomic.LoadInt32(&conns); n != 1 {
		t.Errorf("connections = %d, want 1", n)
	}
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestWatch_ParseFailureDoesNotLogData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f := startStream(w)
		fmt.Fprint(w, sseEvent("SECRET_MARKER_9f3a not json"))
		f.Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	buf := &lockedBuf{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := newWatchTestClient(t, srv, func(o *Options) {
		o.Logger = slog.New(slog.NewJSONHandler(buf, nil))
		o.OnError = func(err error) {
			if errors.Is(err, ErrUnmarshal) {
				cancel()
			}
		}
	})
	_ = c.Watch(ctx, "app", func(ConfigChangeEvent) {})

	out := buf.String()
	if strings.Contains(out, "SECRET_MARKER_9f3a") {
		t.Errorf("raw event data leaked into logs: %s", out)
	}
	if !strings.Contains(out, "data_len") {
		t.Errorf("expected data_len in log, got: %s", out)
	}
}

func TestWatchBackoffDelay(t *testing.T) {
	const maxDelay = 30 * time.Second
	tests := []struct {
		retryDelay time.Duration
		n          int
		lo, hi     time.Duration
	}{
		{time.Second, -1, time.Second, 1500 * time.Millisecond},
		{time.Second, 0, time.Second, 1500 * time.Millisecond},
		{time.Second, 1, time.Second, 1500 * time.Millisecond},
		{time.Second, 2, 2 * time.Second, 3 * time.Second},
		{time.Second, 5, 16 * time.Second, 24 * time.Second},
		{time.Second, 6, maxDelay, maxDelay},
		{time.Second, 1_000_000, maxDelay, maxDelay},
		{time.Minute, 1, maxDelay, maxDelay},
	}
	for _, tt := range tests {
		c := &Client{retryDelay: tt.retryDelay}
		for i := 0; i < 100; i++ {
			got := c.watchBackoffDelay(tt.n)
			if got < tt.lo || got > tt.hi {
				t.Fatalf("retryDelay=%v n=%d: %v not in [%v, %v]", tt.retryDelay, tt.n, got, tt.lo, tt.hi)
			}
		}
	}
}

// --- WatchSnapshot ---

type snapCfg struct {
	Host string `json:"host"`
}

func snapServer(t *testing.T, getStatus int, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/watch") {
			f := startStream(w)
			fmt.Fprint(w, sseEvent(evJSON))
			f.Flush()
			<-r.Context().Done()
			return
		}
		w.WriteHeader(getStatus)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWatchSnapshot_Updates(t *testing.T) {
	key, _ := hex.DecodeString(testKeyHex)
	enc, _ := encrypt([]byte(`{"host":"new.example.com"}`), key)
	srv := snapServer(t, http.StatusOK, enc)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	changed := make(chan struct{}, 1)
	c := newWatchTestClient(t, srv, func(o *Options) {
		o.OnChange = func(string) { // fires after the callback has stored
			select {
			case changed <- struct{}{}:
			default:
			}
		}
	})
	snap := NewSnapshot[snapCfg]()

	stop := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
				_ = snap.Load()
			}
		}
	}()

	done := make(chan error, 1)
	go func() { done <- WatchSnapshot(ctx, c, "app", snap) }()
	waitSignal(t, changed, "snapshot update")
	cancel()
	<-done
	close(stop)
	<-readerDone

	if got := snap.Load().Host; got != "new.example.com" {
		t.Errorf("Host = %q", got)
	}
}

func TestWatchSnapshot_NilSnap(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer srv.Close()
	c := newWatchTestClient(t, srv, nil)
	err := WatchSnapshot[snapCfg](context.Background(), c, "app", nil)
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("got %v, want ErrInvalidResponse", err)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Errorf("hits = %d, want 0", n)
	}
}

func TestWatchSnapshot_GetError(t *testing.T) {
	srv := snapServer(t, http.StatusInternalServerError, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	c := newWatchTestClient(t, srv, func(o *Options) {
		o.OnError = func(err error) {
			select {
			case errCh <- err:
			default:
			}
		}
	})
	snap := NewSnapshot[snapCfg]()
	snap.Store(snapCfg{Host: "old"})

	done := make(chan error, 1)
	go func() { done <- WatchSnapshot(ctx, c, "app", snap) }()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("OnError never called")
	}
	cancel()
	<-done
	if got := snap.Load().Host; got != "old" {
		t.Errorf("snapshot changed on error: %q", got)
	}
}

// --- environments / real service stream ---

func typedEvent(typ, data string) string {
	return fmt.Sprintf("event: %s\ndata: %s\n\n", typ, data)
}

// rawStreamServer sends raw as-is on every watch connection, then holds it open.
func rawStreamServer(t *testing.T, raw string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f := startStream(w)
		fmt.Fprint(w, raw)
		f.Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// watchCollect watches until the first delivered event (or the deadline) and returns
// it, plus the OnChange names and OnError errors seen. ok is false if nothing arrived.
type watchResult struct {
	first    ConfigChangeEvent
	ok       bool
	changed  []string
	errs     []error
	logs     string
	received int
}

func watchFirst(t *testing.T, raw string, mod func(*Options)) watchResult {
	t.Helper()
	srv := rawStreamServer(t, raw)
	var mu sync.Mutex
	var res watchResult
	buf := &lockedBuf{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := newWatchTestClient(t, srv, func(o *Options) {
		o.Logger = slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		o.OnChange = func(n string) { mu.Lock(); res.changed = append(res.changed, n); mu.Unlock() }
		o.OnError = func(err error) { mu.Lock(); res.errs = append(res.errs, err); mu.Unlock() }
		if mod != nil {
			mod(o)
		}
	})
	_ = c.Watch(ctx, "app", func(ev ConfigChangeEvent) {
		mu.Lock()
		res.received++
		if !res.ok {
			res.first, res.ok = ev, true
		}
		mu.Unlock()
		cancel() // stop after first delivered event; OnChange for it still runs before Watch returns
	})
	mu.Lock()
	defer mu.Unlock()
	res.logs = buf.String()
	return res
}

func TestWatch_RealServiceStream(t *testing.T) {
	raw := typedEvent("connected", `{"config":"app"}`) +
		": ping\n\n" +
		typedEvent("config_changed", `{"Type":"updated","ConfigID":7,"ConfigName":"app","GroupID":3,"Env":"prod","ChangedBy":42,"Data":{"password":"SECRET"},"Timestamp":"2026-11-23T10:00:00Z"}`)
	res := watchFirst(t, raw, func(o *Options) { o.Environment = "prod" })
	if !res.ok {
		t.Fatal("no event delivered")
	}
	ev := res.first
	wantTS := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	if ev.ConfigName != "app" || ev.Version != 0 || ev.ChangedBy != 42 ||
		ev.Type != "updated" || ev.Environment != "prod" || !ev.Timestamp.Equal(wantTS) {
		t.Errorf("event = %+v", ev)
	}
	if len(res.errs) != 0 {
		t.Errorf("OnError called: %v", res.errs)
	}
	if len(res.changed) != 1 || res.changed[0] != "app" {
		t.Errorf("OnChange = %v, want [app]", res.changed)
	}
}

func TestWatch_SkipsNonChangeEvents(t *testing.T) {
	good := typedEvent("config_changed", `{"ConfigName":"second","Env":""}`)
	for name, first := range map[string]string{
		"connected":  typedEvent("connected", `{"config":"app"}`),
		"unknown":    typedEvent("something_else", `{"ConfigName":"first"}`),
		"bad json":   typedEvent("connected", `not json`), // data of skipped events is never parsed
		"prefix":     typedEvent("config_changed_v2", `{"ConfigName":"first"}`),
		"wrong case": typedEvent("Config_Changed", `{"ConfigName":"first"}`),
	} {
		t.Run(name, func(t *testing.T) {
			res := watchFirst(t, first+good, nil)
			if !res.ok || res.first.ConfigName != "second" {
				t.Fatalf("first delivered = %+v (ok=%v), want second", res.first, res.ok)
			}
			if len(res.errs) != 0 {
				t.Errorf("OnError called: %v", res.errs)
			}
			if len(res.changed) != 1 {
				t.Errorf("OnChange = %v, want exactly one call", res.changed)
			}
		})
	}
}

func TestWatch_EnvironmentFilter(t *testing.T) {
	norm := func(s string) string {
		if s == "" {
			return "default"
		}
		return s
	}
	envs := []string{"", "default", "prod"}
	for _, clientEnv := range envs {
		for _, evEnv := range envs {
			t.Run(fmt.Sprintf("client=%q/event=%q", clientEnv, evEnv), func(t *testing.T) {
				payload := func(name, env string) string {
					if env == "" {
						return fmt.Sprintf(`{"ConfigName":%q}`, name)
					}
					return fmt.Sprintf(`{"ConfigName":%q,"Env":%q}`, name, env)
				}
				// Candidate first, then a guaranteed match; the first delivered tells us
				// whether the candidate passed the filter.
				raw := typedEvent("config_changed", payload("candidate", evEnv)) +
					typedEvent("config_changed", payload("sentinel", norm(clientEnv)))
				res := watchFirst(t, raw, func(o *Options) { o.Environment = clientEnv })
				want := "sentinel"
				if norm(clientEnv) == norm(evEnv) {
					want = "candidate"
				}
				if !res.ok || res.first.ConfigName != want {
					t.Fatalf("first delivered = %q (ok=%v), want %q", res.first.ConfigName, res.ok, want)
				}
				if len(res.errs) != 0 {
					t.Errorf("OnError called: %v", res.errs)
				}
				if len(res.changed) != 1 || res.changed[0] != want {
					t.Errorf("OnChange = %v, want [%s]", res.changed, want)
				}
			})
		}
	}
}

func TestWatch_EnvironmentFilterCaseSensitive(t *testing.T) {
	raw := typedEvent("config_changed", `{"ConfigName":"first","Env":"PROD"}`) +
		typedEvent("config_changed", `{"ConfigName":"second","Env":"prod"}`)
	res := watchFirst(t, raw, func(o *Options) { o.Environment = "prod" })
	if !res.ok || res.first.ConfigName != "second" {
		t.Fatalf("first delivered = %+v, want second", res.first)
	}
}

func TestWatch_LegacyUntypedSnakeCase(t *testing.T) {
	res := watchFirst(t, sseEvent(`{"config_name":"legacy","version":3,"changed_by":7}`), nil)
	if !res.ok || res.first.ConfigName != "legacy" || res.first.ChangedBy != 7 || res.first.Version != 3 {
		t.Fatalf("event = %+v (ok=%v)", res.first, res.ok)
	}
}

func TestWatch_KeyPrecedence(t *testing.T) {
	for name, tt := range map[string]struct {
		data string
		name string
		by   int
	}{
		"real first":   {`{"ConfigName":"a","config_name":"b","ChangedBy":5,"changed_by":6}`, "a", 5},
		"real last":    {`{"config_name":"b","ConfigName":"a","changed_by":6,"ChangedBy":5}`, "a", 5},
		"snake only":   {`{"config_name":"b","changed_by":6}`, "b", 6},
		"empty real":   {`{"ConfigName":"","config_name":"b"}`, "b", 0},
		"zero real by": {`{"ChangedBy":0,"changed_by":6}`, "", 6},
	} {
		t.Run(name, func(t *testing.T) {
			res := watchFirst(t, typedEvent("config_changed", tt.data), nil)
			if !res.ok || res.first.ConfigName != tt.name || res.first.ChangedBy != tt.by {
				t.Fatalf("event = %+v (ok=%v), want name=%q by=%d", res.first, res.ok, tt.name, tt.by)
			}
		})
	}
}

func TestWatch_ParseFailureThenValid(t *testing.T) {
	raw := typedEvent("config_changed", `{not json`) + typedEvent("config_changed", `{"ConfigName":"ok"}`)
	res := watchFirst(t, raw, nil)
	if !res.ok || res.first.ConfigName != "ok" {
		t.Fatalf("event = %+v (ok=%v), want ok", res.first, res.ok)
	}
	if len(res.errs) != 1 || !errors.Is(res.errs[0], ErrUnmarshal) {
		t.Errorf("errs = %v, want one ErrUnmarshal", res.errs)
	}
}

func TestWatch_DataNeverLeaks(t *testing.T) {
	const marker = "DATA_MARKER_77c1"
	raw := typedEvent("config_changed", `{"ConfigName":"other","Env":"dev","Data":{"password":"`+marker+`"}}`) + // filtered out
		typedEvent("config_changed", `{"ConfigName":"bad","Data":{"password":"`+marker+`"} oops`) + // malformed
		typedEvent("config_changed", `{"ConfigName":"app","Data":{"password":"`+marker+`"}}`) // valid
	res := watchFirst(t, raw, nil)
	if !res.ok || res.first.ConfigName != "app" {
		t.Fatalf("event = %+v (ok=%v)", res.first, res.ok)
	}
	if strings.Contains(res.logs, marker) {
		t.Errorf("Data leaked into logs: %s", res.logs)
	}
	for _, err := range res.errs {
		if strings.Contains(err.Error(), marker) {
			t.Errorf("Data leaked into OnError: %v", err)
		}
	}
	if len(res.errs) != 1 {
		t.Errorf("errs = %v, want exactly the malformed-event error", res.errs)
	}
	if strings.Contains(fmt.Sprintf("%+v", res.first), marker) {
		t.Errorf("Data leaked into event: %+v", res.first)
	}
}

func TestWatchSnapshot_EnvironmentFilter(t *testing.T) {
	key, _ := hex.DecodeString(testKeyHex)
	var gets atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/watch") {
			f := startStream(w)
			fmt.Fprint(w, typedEvent("connected", `{"config":"app"}`))
			fmt.Fprint(w, typedEvent("config_changed", `{"ConfigName":"app","Env":"default"}`))
			fmt.Fprint(w, typedEvent("config_changed", `{"ConfigName":"app","Env":"prod"}`))
			f.Flush()
			<-r.Context().Done()
			return
		}
		gets.Add(1)
		host := "wrong.example.com"
		if r.URL.Query().Get("env") == "prod" {
			host = "prod.example.com"
		}
		enc, _ := encrypt([]byte(`{"host":"`+host+`"}`), key)
		_, _ = w.Write(enc)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	changed := make(chan struct{}, 2)
	c := newWatchTestClient(t, srv, func(o *Options) {
		o.Environment = "prod"
		o.OnChange = func(string) { changed <- struct{}{} } // fires after the snapshot callback
	})
	snap := NewSnapshot[snapCfg]()
	done := make(chan error, 1)
	go func() { done <- WatchSnapshot(ctx, c, "app", snap) }()
	waitSignal(t, changed, "prod event")
	cancel()
	<-done

	if got := snap.Load().Host; got != "prod.example.com" {
		t.Errorf("Host = %q, want prod.example.com", got)
	}
	if n := gets.Load(); n != 1 {
		t.Errorf("GET requests = %d, want 1 (default-env event must not trigger a fetch)", n)
	}
}
