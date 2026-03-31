package configsdk

import (
"context"
"encoding/hex"
"encoding/json"
"errors"
"fmt"
"net/http"
"net/http/httptest"
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

func TestWatch_AuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
w.WriteHeader(http.StatusUnauthorized)
}))
	defer srv.Close()

	var onErrorCalled int32
	client, _ := New(Options{
Host:          srv.URL,
ServiceToken:  "bad-token",
EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
HTTPClient:    srv.Client(),
		RetryDelay:    10 * time.Millisecond,
		OnError: func(err error) {
			atomic.AddInt32(&onErrorCalled, 1)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_ = client.Watch(ctx, "app", func(event ConfigChangeEvent) {})

	if n := atomic.LoadInt32(&onErrorCalled); n < 1 {
		t.Error("OnError was never called for auth error")
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
