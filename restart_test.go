package configsdk

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// restartEnv is one httptest server serving both the SSE watch endpoint (events
// pushed from the test) and the GET config endpoint (hits counted and signalled).
type restartEnv struct {
	srv    *httptest.Server
	events chan string   // raw SSE chunks; one chunk is written and flushed at once
	conn   chan struct{} // signalled on every watch connection
	drop   chan struct{} // a receive ends the current watch stream cleanly (Watch reconnects)
	hitCh  chan struct{} // signalled on every GET (after the response was chosen)
	hits   atomic.Int32
	resp   atomic.Pointer[restartResp]
	onGet  atomic.Pointer[func()]
	key    []byte
}

type restartResp struct {
	status int
	body   []byte
}

func newRestartEnv(t *testing.T) *restartEnv {
	t.Helper()
	e := &restartEnv{
		events: make(chan string, 64),
		conn:   make(chan struct{}, 64),
		drop:   make(chan struct{}, 4),
		hitCh:  make(chan struct{}, 64),
	}
	e.key, _ = hex.DecodeString(testKeyHex)
	e.setHost("old")
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/watch") {
			f := startStream(w)
			e.conn <- struct{}{}
			for {
				select {
				case chunk := <-e.events:
					_, _ = io.WriteString(w, chunk)
					f.Flush()
				case <-e.drop:
					return
				case <-r.Context().Done():
					return
				}
			}
		}
		e.hits.Add(1)
		if fn := e.onGet.Load(); fn != nil {
			(*fn)()
		}
		resp := e.resp.Load()
		select {
		case e.hitCh <- struct{}{}:
		default:
		}
		w.WriteHeader(resp.status)
		_, _ = w.Write(resp.body)
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *restartEnv) setHost(host string) {
	e.setJSON(fmt.Sprintf(`{"host":%q}`, host))
}

func (e *restartEnv) setJSON(js string) {
	enc, _ := encrypt([]byte(js), e.key)
	e.resp.Store(&restartResp{status: http.StatusOK, body: enc})
}

func (e *restartEnv) setRaw(status int, body []byte) {
	e.resp.Store(&restartResp{status: status, body: body})
}

func (e *restartEnv) send(chunks ...string) {
	e.events <- strings.Join(chunks, "")
}

// rev builds a config_changed event in the v0.11.0 payload shape.
func rev(typ string, version int) string {
	return typedEvent("config_changed",
		fmt.Sprintf(`{"type":%q,"config_name":"app","env":"","version":%d}`, typ, version))
}

func (e *restartEnv) client(t *testing.T, debounce time.Duration, mod func(*Options)) *Client {
	t.Helper()
	c := newWatchTestClient(t, e.srv, mod)
	c.restartDebounce = debounce
	c.restartJitter = 0
	c.restartConnectCooldown = 0 // reconnects always check unless a test sets a cooldown
	return c
}

func (e *restartEnv) startRestart(ctx context.Context, c *Client, restart func()) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- RestartOnChange(ctx, c, "app", snapCfg{Host: "old"}, restart) }()
	return ch
}

// startSynced starts RestartOnChange and waits until the watch is connected and
// the connect check (GET #1, content equal to current) has hit the server.
func (e *restartEnv) startSynced(t *testing.T, ctx context.Context, c *Client, restart func()) <-chan error {
	t.Helper()
	done := e.startRestart(ctx, c, restart)
	waitSignal(t, e.conn, "watch connection")
	waitSignal(t, e.hitCh, "connect check GET #1")
	return done
}

func wantCanceled(t *testing.T, ch <-chan error) {
	t.Helper()
	if err := waitErr(t, ch); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func safetyCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx, cancel
}

func waitErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("RestartOnChange did not return")
		return nil
	}
}

func wantNil(t *testing.T, ch <-chan error) {
	t.Helper()
	if err := waitErr(t, ch); err != nil {
		t.Fatalf("RestartOnChange = %v, want nil", err)
	}
}

func wantHits(t *testing.T, e *restartEnv, want int32) {
	t.Helper()
	if n := e.hits.Load(); n != want {
		t.Errorf("GET hits = %d, want %d", n, want)
	}
}

func wantRestarts(t *testing.T, n *atomic.Int32, want int32) {
	t.Helper()
	if got := n.Load(); got != want {
		t.Errorf("restart calls = %d, want %d", got, want)
	}
}

// negativeWait is the bounded wait used only for "nothing must happen" assertions.
func negativeWait() { time.Sleep(150 * time.Millisecond) }

func TestRestartOnChange_NilRestart(t *testing.T) { // T1
	e := newRestartEnv(t)
	c := e.client(t, 10*time.Millisecond, nil)
	ctx, _ := safetyCtx(t)
	err := RestartOnChange[snapCfg](ctx, c, "app", snapCfg{}, nil)
	if err == nil || errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("got %v, want a plain error that is not ErrInvalidResponse", err)
	}
	if !strings.Contains(err.Error(), "restart must be non-nil") {
		t.Errorf("message = %q", err)
	}
	wantHits(t, e, 0)
	select {
	case <-e.conn:
		t.Error("watch connection opened for nil restart")
	default:
	}
}

func TestRestartOnChange_InitialConnectCheck_NoContentLogged(t *testing.T) { // T2 + T21
	const marker = "SECRETMARKER-7f3a"
	e := newRestartEnv(t)
	e.setHost(marker)
	buf := &lockedBuf{}
	c := e.client(t, 20*time.Millisecond, func(o *Options) {
		o.Logger = slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	})
	ctx, _ := safetyCtx(t)
	var restarts atomic.Int32
	done := e.startRestart(ctx, c, func() { restarts.Add(1) }) // no event sent
	wantNil(t, done)
	wantRestarts(t, &restarts, 1)
	wantHits(t, e, 1)
	if strings.Contains(buf.String(), marker) {
		t.Errorf("config content leaked into logs: %s", buf.String())
	}
}

func TestRestartOnChange_EqualThenChanged(t *testing.T) { // T3
	e := newRestartEnv(t)
	c := e.client(t, 20*time.Millisecond, nil)
	ctx, _ := safetyCtx(t)
	var restarts atomic.Int32
	done := e.startSynced(t, ctx, c, func() { restarts.Add(1) })
	e.send(rev("updated", 1))
	waitSignal(t, e.hitCh, "GET #2")
	wantRestarts(t, &restarts, 0)
	e.setHost("new")
	e.send(rev("updated", 2))
	wantNil(t, done)
	wantRestarts(t, &restarts, 1)
	wantHits(t, e, 3)
}

func TestRestartOnChange_BurstOneFetch(t *testing.T) { // T4
	e := newRestartEnv(t)
	c := e.client(t, 100*time.Millisecond, nil)
	ctx, cancel := safetyCtx(t)
	var restarts atomic.Int32
	done := e.startSynced(t, ctx, c, func() { restarts.Add(1) })
	e.send(rev("updated", 1), rev("updated", 2), rev("updated", 3), rev("updated", 4), rev("updated", 5))
	waitSignal(t, e.hitCh, "GET #2")
	negativeWait()
	wantHits(t, e, 2)
	cancel()
	wantCanceled(t, done)
	wantRestarts(t, &restarts, 0)
}

func TestRestartOnChange_CallbackNotBlockedByDebounce(t *testing.T) { // T5
	e := newRestartEnv(t)
	var changed atomic.Int32
	var seenAtGet atomic.Int32
	fn := func() { seenAtGet.Store(changed.Load()) }
	e.onGet.Store(&fn)
	c := e.client(t, 300*time.Millisecond, func(o *Options) {
		o.OnChange = func(string) { changed.Add(1) }
	})
	ctx, _ := safetyCtx(t)
	done := e.startSynced(t, ctx, c, func() {})
	e.setHost("new")
	e.send(rev("updated", 1), rev("updated", 2), rev("updated", 3))
	wantNil(t, done)
	if n := seenAtGet.Load(); n != 3 {
		t.Errorf("OnChange count when last GET arrived = %d, want 3", n)
	}
	wantHits(t, e, 2)
}

func TestRestartOnChange_DeletedSkipped(t *testing.T) { // T6
	e := newRestartEnv(t)
	c := e.client(t, 20*time.Millisecond, nil)
	ctx, _ := safetyCtx(t)
	var restarts atomic.Int32
	done := e.startSynced(t, ctx, c, func() { restarts.Add(1) })
	e.send(rev("deleted", 5))
	negativeWait()
	wantHits(t, e, 1)
	e.setHost("new")
	e.send(rev("updated", 6))
	wantNil(t, done)
	wantRestarts(t, &restarts, 1)
	wantHits(t, e, 2)
}

func TestRestartOnChange_PermanentErrorReportedKeepsWatching(t *testing.T) { // T11
	tests := []struct {
		name string
		set  func(e *restartEnv)
		want error
	}{
		{"404", func(e *restartEnv) { e.setRaw(http.StatusNotFound, nil) }, ErrNotFound},
		{"garbage body", func(e *restartEnv) {
			e.setRaw(http.StatusOK, []byte("not-a-valid-ciphertext-at-all-0123456789"))
		}, ErrDecryptionFailed},
		{"wrong type", func(e *restartEnv) { e.setJSON(`{"host":5}`) }, ErrUnmarshal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRestartEnv(t)
			tt.set(e)
			mod, errCh := errCollector()
			c := e.client(t, 20*time.Millisecond, mod)
			ctx, _ := safetyCtx(t)
			var restarts atomic.Int32
			done := e.startRestart(ctx, c, func() { restarts.Add(1) }) // connect check fails, no event
			if err := waitErrCh(t, errCh); !errors.Is(err, tt.want) {
				t.Fatalf("OnError got %v, want %v", err, tt.want)
			}
			wantRestarts(t, &restarts, 0)
			negativeWait()
			wantHits(t, e, 1) // permanent errors are not retried
			e.setHost("new")
			e.send(rev("updated", 1))
			wantNil(t, done)
			wantRestarts(t, &restarts, 1)
			wantHits(t, e, 2)
			if len(errCh) != 0 {
				t.Errorf("OnError called more than once: %d extra", len(errCh))
			}
		})
	}
}

// Retried: every error except the permanent set (ErrNotFound, ErrUnauthorized,
// ErrForbidden, ErrDecryptionFailed, ErrUnmarshal, ErrResponseTooLarge).
func TestRestartOnChange_InvalidResponseErrorsRetried(t *testing.T) {
	for _, code := range []int{http.StatusBadRequest, http.StatusTooManyRequests, http.StatusRequestTimeout} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			e := newRestartEnv(t)
			e.setRaw(code, nil)
			hook := func() {
				if e.hits.Load() == 2 {
					e.setHost("new") // the retry (GET #2) sees changed content
				}
			}
			e.onGet.Store(&hook)
			mod, errCh := errCollector()
			c := e.client(t, 20*time.Millisecond, mod)
			c.retryDelay = time.Millisecond
			ctx, _ := safetyCtx(t)
			var restarts atomic.Int32
			done := e.startRestart(ctx, c, func() { restarts.Add(1) }) // no event: only the retry can produce GET #2
			if err := waitErrCh(t, errCh); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("OnError got %v, want ErrInvalidResponse", err)
			}
			wantNil(t, done)
			wantRestarts(t, &restarts, 1)
			wantHits(t, e, 2)
		})
	}
}

func TestRestartOnChange_ContextCancel(t *testing.T) { // T12
	e := newRestartEnv(t)
	c := e.client(t, 20*time.Millisecond, nil)
	ctx, cancel := safetyCtx(t)
	var restarts atomic.Int32
	done := e.startRestart(ctx, c, func() { restarts.Add(1) })
	waitSignal(t, e.conn, "watch connection")
	cancel()
	if err := waitErr(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	wantRestarts(t, &restarts, 0)
}

func TestRestartOnChange_CancelDuringPendingWindow(t *testing.T) { // T13
	e := newRestartEnv(t)
	e.setHost("new")
	changed := make(chan struct{}, 4)
	c := e.client(t, 5*time.Second, func(o *Options) {
		o.OnChange = func(string) { changed <- struct{}{} }
	})
	ctx, cancel := safetyCtx(t)
	var restarts atomic.Int32
	done := e.startRestart(ctx, c, func() { restarts.Add(1) })
	e.send(rev("updated", 1))
	waitSignal(t, changed, "OnChange")
	cancel()
	if err := waitErr(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	negativeWait()
	wantHits(t, e, 0)
	wantRestarts(t, &restarts, 0)
}

func TestRestartOnChange_Close(t *testing.T) { // T14
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprintf("pendingWindow=%v", pending), func(t *testing.T) {
			e := newRestartEnv(t)
			e.setHost("new")
			changed := make(chan struct{}, 4)
			c := e.client(t, 5*time.Second, func(o *Options) {
				o.OnChange = func(string) { changed <- struct{}{} }
			})
			ctx, _ := safetyCtx(t)
			var restarts atomic.Int32
			done := e.startRestart(ctx, c, func() { restarts.Add(1) })
			waitSignal(t, e.conn, "watch connection")
			if pending {
				e.send(rev("updated", 1))
				waitSignal(t, changed, "OnChange")
			}
			c.Close()
			if err := waitErr(t, done); !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want context.Canceled", err)
			}
			negativeWait()
			wantHits(t, e, 0)
			wantRestarts(t, &restarts, 0)
		})
	}
}

func TestRestartOnChange_TerminalWatchErrors(t *testing.T) { // T15
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
			var hits, onErr atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.WriteHeader(tt.code)
			}))
			defer srv.Close()
			c := newWatchTestClient(t, srv, func(o *Options) {
				o.OnError = func(error) { onErr.Add(1) }
			})
			c.restartDebounce, c.restartJitter = 10*time.Millisecond, 0
			ctx, _ := safetyCtx(t)
			var restarts atomic.Int32
			err := RestartOnChange(ctx, c, "app", snapCfg{}, func() { restarts.Add(1) })
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
			wantRestarts(t, &restarts, 0)
			if n := hits.Load(); n != 1 {
				t.Errorf("server hits = %d, want 1", n)
			}
			if n := onErr.Load(); n != 1 {
				t.Errorf("OnError calls = %d, want 1", n)
			}
		})
	}
}

func TestRestartOnChange_RestartCancelsContext(t *testing.T) { // T16
	e := newRestartEnv(t)
	e.setHost("new")
	c := e.client(t, 20*time.Millisecond, nil)
	ctx, cancel := safetyCtx(t)
	var restarts atomic.Int32
	done := e.startRestart(ctx, c, func() { restarts.Add(1); cancel() })
	wantNil(t, done)
	wantRestarts(t, &restarts, 1)
}

func TestRestartDelay(t *testing.T) { // T17
	t.Run("jitter bounds", func(t *testing.T) {
		c := &Client{restartDebounce: 50 * time.Millisecond, restartJitter: 20 * time.Millisecond}
		seen := map[time.Duration]bool{}
		for range 200 {
			d := c.restartDelay()
			if d < 50*time.Millisecond || d >= 70*time.Millisecond {
				t.Fatalf("delay %v outside [50ms, 70ms)", d)
			}
			seen[d] = true
		}
		if len(seen) < 2 {
			t.Errorf("no jitter: %d distinct values", len(seen))
		}
	})
	for name, tt := range map[string]struct {
		d, j, want time.Duration
	}{
		"zero jitter":     {50 * time.Millisecond, 0, 50 * time.Millisecond},
		"negative jitter": {50 * time.Millisecond, -time.Second, 50 * time.Millisecond},
		"zero value":      {0, 0, 0},
	} {
		t.Run(name, func(t *testing.T) {
			c := &Client{restartDebounce: tt.d, restartJitter: tt.j}
			if got := c.restartDelay(); got != tt.want {
				t.Errorf("delay = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNew_RestartDefaults(t *testing.T) { // T18
	c, err := New(Options{Host: "https://example.com", ServiceToken: "t", EncryptionKey: testKeyHex})
	if err != nil {
		t.Fatal(err)
	}
	if c.restartDebounce != 2*time.Second || c.restartJitter != 2*time.Second {
		t.Errorf("defaults = %v / %v, want 2s / 2s", c.restartDebounce, c.restartJitter)
	}
	if c.restartConnectCooldown != 30*time.Second {
		t.Errorf("restartConnectCooldown = %v, want 30s", c.restartConnectCooldown)
	}
}

// errCollector returns an OnError option hook and the channel it fills.
func errCollector() (opt func(*Options), errs chan error) {
	ch := make(chan error, 64)
	return func(o *Options) { o.OnError = func(err error) { ch <- err } }, ch
}

func waitErrCh(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("OnError never called")
		return nil
	}
}

func TestRestartOnChange_TransientFetchRetried(t *testing.T) { // T22
	e := newRestartEnv(t)
	hook := func() {
		switch n := e.hits.Load(); {
		case n <= 2:
			e.setRaw(http.StatusServiceUnavailable, nil)
		case n == 3:
			e.setHost("old")
		default:
			e.setHost("new")
		}
	}
	e.onGet.Store(&hook)
	mod, errCh := errCollector()
	c := e.client(t, 20*time.Millisecond, mod)
	c.retryCount = 0
	c.retryDelay = time.Millisecond
	ctx, _ := safetyCtx(t)
	var restarts atomic.Int32
	done := e.startRestart(ctx, c, func() { restarts.Add(1) }) // no initial event
	for i := 1; i <= 3; i++ {
		waitSignal(t, e.hitCh, fmt.Sprintf("GET #%d", i))
	}
	negativeWait()
	wantHits(t, e, 3)
	if n := len(errCh); n != 2 {
		t.Fatalf("OnError calls = %d, want 2", n)
	}
	for range 2 {
		if err := <-errCh; !errors.Is(err, ErrConnectionFailed) {
			t.Errorf("OnError got %v, want ErrConnectionFailed", err)
		}
	}
	wantRestarts(t, &restarts, 0)
	e.send(rev("updated", 3))
	wantNil(t, done)
	wantRestarts(t, &restarts, 1)
	wantHits(t, e, 4)
}

func TestRestartOnChange_BackoffWaitNoTightLoop(t *testing.T) { // T23
	for _, useClose := range []bool{false, true} {
		t.Run(fmt.Sprintf("close=%v", useClose), func(t *testing.T) {
			e := newRestartEnv(t)
			e.setRaw(http.StatusServiceUnavailable, nil)
			mod, errCh := errCollector()
			c := e.client(t, 20*time.Millisecond, mod)
			c.retryCount = 0
			c.retryDelay = 30 * time.Second
			ctx, cancel := safetyCtx(t)
			var restarts atomic.Int32
			done := e.startRestart(ctx, c, func() { restarts.Add(1) }) // no event
			waitErrCh(t, errCh)
			negativeWait()
			wantHits(t, e, 1)
			if n := len(errCh); n != 0 {
				t.Errorf("extra OnError calls during backoff: %d", n)
			}
			if useClose {
				c.Close()
			} else {
				cancel()
			}
			wantCanceled(t, done)
			wantRestarts(t, &restarts, 0)
			negativeWait()
			wantHits(t, e, 1)
			if n := len(errCh); n != 0 {
				t.Errorf("OnError calls after stop: %d", n)
			}
		})
	}
}

// T24: an event during a backoff wait does not replace or shorten it.
func TestRestartOnChange_EventDoesNotShortenBackoff(t *testing.T) { // T24
	e := newRestartEnv(t)
	e.setRaw(http.StatusServiceUnavailable, nil)
	mod, errCh := errCollector()
	c := e.client(t, 20*time.Millisecond, mod)
	c.retryCount = 0
	c.retryDelay = 30 * time.Second
	ctx, cancel := safetyCtx(t)
	var restarts atomic.Int32
	done := e.startRestart(ctx, c, func() { restarts.Add(1) }) // connect check: failure #1
	waitErrCh(t, errCh)
	e.send(rev("updated", 1))
	negativeWait()
	wantHits(t, e, 1)
	wantRestarts(t, &restarts, 0)
	cancel()
	wantCanceled(t, done)
	wantHits(t, e, 1)
}

// A burst of events during a backoff wait yields exactly one GET, and only
// after the full backoff (one-sided lower bound: level 1 is >= retryDelay).
func TestRestartOnChange_BurstDuringBackoffOneGet(t *testing.T) {
	const retryDelay = 200 * time.Millisecond
	e := newRestartEnv(t)
	e.setRaw(http.StatusServiceUnavailable, nil)
	at := e.recordArrivals()
	hook := e.onGet.Load()
	wrapped := func() {
		(*hook)()
		if e.hits.Load() == 2 {
			e.setHost("old") // equal to current: no restart
		}
	}
	e.onGet.Store(&wrapped)
	mod, errCh := errCollector()
	c := e.client(t, 10*time.Millisecond, mod)
	c.retryCount = 0
	c.retryDelay = retryDelay
	ctx, _ := safetyCtx(t)
	var restarts atomic.Int32
	done := e.startRestart(ctx, c, func() { restarts.Add(1) })
	waitSignal(t, e.hitCh, "GET #1")
	waitErrCh(t, errCh)
	e.send(rev("updated", 1), rev("updated", 2), rev("updated", 3))
	waitSignal(t, e.hitCh, "GET #2")
	if gap := at(2).Sub(at(1)); gap < retryDelay {
		t.Errorf("gap between GET #1 and #2 = %v, want >= %v (event shortened the backoff)", gap, retryDelay)
	}
	negativeWait()
	wantHits(t, e, 2) // the burst was covered by GET #2
	wantRestarts(t, &restarts, 0)
	e.setHost("new")
	e.send(rev("updated", 4)) // still alive: a later event is handled normally
	wantNil(t, done)
	wantRestarts(t, &restarts, 1)
	wantHits(t, e, 3)
}

func TestRestartOnChange_CallbackNotBlockedByFetch_StopInFlight(t *testing.T) { // T25
	for _, useClose := range []bool{false, true} {
		t.Run(fmt.Sprintf("close=%v", useClose), func(t *testing.T) {
			e := newRestartEnv(t)
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock) // registered after srv.Close, so it runs first (LIFO)
			started := make(chan struct{}, 4)
			hook := func() {
				started <- struct{}{}
				<-release
			}
			e.onGet.Store(&hook)
			changed := make(chan struct{}, 4)
			var onErr atomic.Int32
			c := e.client(t, 10*time.Millisecond, func(o *Options) {
				o.OnChange = func(string) { changed <- struct{}{} }
				o.OnError = func(error) { onErr.Add(1) }
			})
			ctx, cancel := safetyCtx(t)
			var restarts atomic.Int32
			done := e.startRestart(ctx, c, func() { restarts.Add(1) })
			waitSignal(t, started, "connect check GET in flight")
			e.send(rev("updated", 1), rev("updated", 2))
			waitSignal(t, changed, "OnChange #1") // callbacks run while the GET is blocked
			waitSignal(t, changed, "OnChange #2")
			wantHits(t, e, 1)
			if useClose {
				c.Close()
			} else {
				cancel()
			}
			wantCanceled(t, done) // while the handler is still blocked
			wantRestarts(t, &restarts, 0)
			if n := onErr.Load(); n != 0 {
				t.Errorf("OnError calls = %d, want 0", n)
			}
			unblock()
			negativeWait()
			wantHits(t, e, 1)
		})
	}
}

// T26: events sent during backoff waits neither shorten them nor reset the
// consecutive-failure counter: GET #1 -> #2 waits level 1 (>= retryDelay) and
// GET #2 -> #3 waits level 2 (watchBackoffDelay: [2*base, 3*base)). Events are
// sent only after the preceding failure was reported. The assertions are
// one-sided lower bounds on server-side arrival times (timers never fire early).
func TestRestartOnChange_BackoffNotResetByEvent(t *testing.T) { // T26
	const retryDelay = 200 * time.Millisecond
	e := newRestartEnv(t)
	e.setRaw(http.StatusServiceUnavailable, nil)
	at := e.recordArrivals()
	mod, errCh := errCollector()
	c := e.client(t, 10*time.Millisecond, mod)
	c.retryCount = 0
	c.retryDelay = retryDelay
	ctx, cancel := safetyCtx(t)
	var restarts atomic.Int32
	done := e.startRestart(ctx, c, func() { restarts.Add(1) }) // connect check: failure #1
	waitSignal(t, e.hitCh, "GET #1")
	waitErrCh(t, errCh)
	e.send(rev("updated", 1)) // must not shorten the backoff
	waitSignal(t, e.hitCh, "GET #2")
	waitErrCh(t, errCh)
	e.send(rev("updated", 2)) // must not shorten the backoff, nor reset the counter
	waitSignal(t, e.hitCh, "GET #3")
	if gap := at(2).Sub(at(1)); gap < retryDelay {
		t.Errorf("gap between GET #1 and #2 = %v, want >= %v (event shortened the backoff)", gap, retryDelay)
	}
	if gap := at(3).Sub(at(2)); gap < 2*retryDelay {
		t.Errorf("gap between GET #2 and #3 = %v, want >= %v (failure counter was reset)", gap, 2*retryDelay)
	}
	cancel()
	wantCanceled(t, done)
	wantRestarts(t, &restarts, 0)
}

func TestRestartOnChange_ReconnectTriggersCheck(t *testing.T) { // N2 + cooldown (b)
	for name, cooldown := range map[string]time.Duration{"zero": 0, "negative": -time.Second} {
		t.Run(name, func(t *testing.T) {
			e := newRestartEnv(t)
			c := e.client(t, 20*time.Millisecond, nil)
			c.restartConnectCooldown = cooldown // <= 0 disables suppression
			ctx, _ := safetyCtx(t)
			var restarts atomic.Int32
			done := e.startSynced(t, ctx, c, func() { restarts.Add(1) })
			e.setHost("new") // no event: only the reconnect can reveal it
			e.drop <- struct{}{}
			waitSignal(t, e.conn, "watch reconnection")
			wantNil(t, done)
			wantRestarts(t, &restarts, 1)
			wantHits(t, e, 2)
		})
	}
}

// recordArrivals makes the fake server timestamp every GET by its hit number.
func (e *restartEnv) recordArrivals() func(n int32) time.Time {
	var mu sync.Mutex
	arrival := map[int32]time.Time{}
	hook := func() {
		mu.Lock()
		arrival[e.hits.Load()] = time.Now()
		mu.Unlock()
	}
	e.onGet.Store(&hook)
	return func(n int32) time.Time {
		mu.Lock()
		defer mu.Unlock()
		return arrival[n]
	}
}

// cooldown (a): a reconnect inside the cooldown postpones its check, never drops it.
// The lower bound is one-sided: GET #1 arrived before lastOK was set, and the
// deferred check fires no earlier than lastOK+cooldown.
func TestRestartOnChange_ConnectCooldownDefersReconnectCheck(t *testing.T) {
	const cooldown = 300 * time.Millisecond
	e := newRestartEnv(t)
	at := e.recordArrivals()
	c := e.client(t, 20*time.Millisecond, nil)
	c.restartConnectCooldown = cooldown
	ctx, _ := safetyCtx(t)
	var restarts atomic.Int32
	done := e.startSynced(t, ctx, c, func() { restarts.Add(1) })
	e.setHost("new") // no event: only the reconnect can reveal it
	e.drop <- struct{}{}
	waitSignal(t, e.conn, "watch reconnection")
	waitSignal(t, e.hitCh, "deferred GET #2")
	wantNil(t, done)
	wantRestarts(t, &restarts, 1)
	wantHits(t, e, 2)
	if gap := at(2).Sub(at(1)); gap < cooldown {
		t.Errorf("gap between GET #1 and #2 = %v, want >= %v (check was not postponed)", gap, cooldown)
	}
}

// Event triggers are not subject to the cooldown, and a pending deferral does not
// fire before it ends.
func TestRestartOnChange_ConnectCooldownNotImmediateEventsUnaffected(t *testing.T) {
	e := newRestartEnv(t)
	c := e.client(t, 20*time.Millisecond, nil)
	c.restartConnectCooldown = time.Hour
	ctx, _ := safetyCtx(t)
	var restarts atomic.Int32
	done := e.startSynced(t, ctx, c, func() { restarts.Add(1) }) // first connect always checks
	e.setHost("new")                                             // a check would restart
	e.drop <- struct{}{}
	waitSignal(t, e.conn, "watch reconnection")
	negativeWait()
	wantHits(t, e, 1)
	wantRestarts(t, &restarts, 0)
	e.send(rev("updated", 1))
	wantNil(t, done)
	wantRestarts(t, &restarts, 1)
	wantHits(t, e, 2)
}

// cooldown (d): several reconnects inside the cooldown yield exactly one deferred check.
func TestRestartOnChange_ConnectCooldownAbsorbsRepeatedReconnects(t *testing.T) {
	e := newRestartEnv(t)
	c := e.client(t, 20*time.Millisecond, nil)
	c.restartConnectCooldown = 500 * time.Millisecond
	ctx, cancel := safetyCtx(t)
	var restarts atomic.Int32
	done := e.startSynced(t, ctx, c, func() { restarts.Add(1) })
	for range 3 {
		e.drop <- struct{}{}
		waitSignal(t, e.conn, "watch reconnection")
	}
	waitSignal(t, e.hitCh, "deferred GET #2")
	negativeWait()
	wantHits(t, e, 2)
	cancel()
	wantCanceled(t, done)
	wantRestarts(t, &restarts, 0)
}

// cooldown (e): a successful event-triggered check completing after the deferral
// was scheduled cancels it. We wait past the deferral's due time and assert no GET.
func TestRestartOnChange_EventCheckCancelsDeferredConnectCheck(t *testing.T) {
	const cooldown = 400 * time.Millisecond
	e := newRestartEnv(t)
	at := e.recordArrivals()
	c := e.client(t, 20*time.Millisecond, nil)
	c.restartConnectCooldown = cooldown
	ctx, cancel := safetyCtx(t)
	var restarts atomic.Int32
	done := e.startSynced(t, ctx, c, func() { restarts.Add(1) })
	e.drop <- struct{}{}
	waitSignal(t, e.conn, "watch reconnection") // deferral is scheduled for ~GET #1 + cooldown
	e.send(rev("updated", 1))
	waitSignal(t, e.hitCh, "event GET #2")
	if !at(2).Before(at(1).Add(cooldown)) {
		t.Skip("event check arrived after the deferral was due; machine too slow for this scenario")
	}
	time.Sleep(time.Until(at(1).Add(cooldown + 150*time.Millisecond)))
	wantHits(t, e, 2)
	cancel()
	wantCanceled(t, done)
	wantRestarts(t, &restarts, 0)
}

func TestRestartOnChange_FailedCheckDoesNotStartCooldown(t *testing.T) { // cooldown (c)
	e := newRestartEnv(t)
	e.setRaw(http.StatusNotFound, nil) // permanent (not retried) failure on the connect check
	mod, errCh := errCollector()
	c := e.client(t, 20*time.Millisecond, mod)
	c.restartConnectCooldown = time.Hour
	ctx, _ := safetyCtx(t)
	var restarts atomic.Int32
	done := e.startRestart(ctx, c, func() { restarts.Add(1) })
	waitSignal(t, e.conn, "watch connection")
	waitErrCh(t, errCh)
	wantRestarts(t, &restarts, 0)
	e.setHost("new")
	e.drop <- struct{}{}
	waitSignal(t, e.conn, "watch reconnection")
	wantNil(t, done) // the reconnect check ran despite the huge cooldown
	wantRestarts(t, &restarts, 1)
	wantHits(t, e, 2)
}

func TestRestartOnChange_BackoffNotResetByNonTransientError(t *testing.T) {
	const retryDelay = 200 * time.Millisecond
	e := newRestartEnv(t)
	var mu sync.Mutex
	arrival := map[int32]time.Time{}
	hook := func() {
		n := e.hits.Load()
		mu.Lock()
		arrival[n] = time.Now()
		mu.Unlock()
		if n == 2 {
			e.setRaw(http.StatusNotFound, nil) // permanent, not retried
		} else {
			e.setRaw(http.StatusServiceUnavailable, nil)
		}
	}
	e.onGet.Store(&hook)
	e.setRaw(http.StatusServiceUnavailable, nil)
	mod, errCh := errCollector()
	c := e.client(t, 10*time.Millisecond, mod)
	c.retryCount = 0
	c.retryDelay = retryDelay
	ctx, cancel := safetyCtx(t)
	var restarts atomic.Int32
	done := e.startRestart(ctx, c, func() { restarts.Add(1) })
	// GET #1 -> 503 (failure 1, level-1 backoff pending)
	waitSignal(t, e.hitCh, "GET #1")
	waitErrCh(t, errCh)
	e.send(rev("updated", 1)) // does not shorten the backoff; the retry covers it
	// GET #2 (after the backoff) -> 404: not retried, must NOT reset the counter
	waitSignal(t, e.hitCh, "GET #2")
	if err := waitErrCh(t, errCh); !errors.Is(err, ErrNotFound) {
		t.Fatalf("OnError got %v, want ErrNotFound", err)
	}
	e.send(rev("updated", 2))
	// GET #3 -> 503: failure 2 overall, so level-2 backoff [2*base, 3*base)
	waitSignal(t, e.hitCh, "GET #3")
	waitErrCh(t, errCh)
	waitSignal(t, e.hitCh, "GET #4")
	mu.Lock()
	gap, gap12 := arrival[4].Sub(arrival[3]), arrival[2].Sub(arrival[1])
	mu.Unlock()
	if gap12 < retryDelay {
		t.Errorf("gap between GET #1 and #2 = %v, want >= %v (event shortened the backoff)", gap12, retryDelay)
	}
	if gap < 2*retryDelay {
		t.Errorf("gap between GET #3 and #4 = %v, want >= %v (counter reset by permanent error)", gap, 2*retryDelay)
	}
	cancel()
	wantCanceled(t, done)
	wantRestarts(t, &restarts, 0)
}

func TestRestartOnChange_TriggersDuringFetchCoalesce(t *testing.T) { // N4
	tests := []struct {
		name   string
		events []string
		want   int32
	}{
		{"3 updated", []string{rev("updated", 1), rev("updated", 2), rev("updated", 3)}, 2},
		{"2 deleted", []string{rev("deleted", 1), rev("deleted", 2)}, 1},
		{"deleted updated deleted", []string{rev("deleted", 1), rev("updated", 2), rev("deleted", 3)}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRestartEnv(t)
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			started := make(chan struct{}, 4)
			hook := func() {
				if e.hits.Load() == 1 {
					started <- struct{}{}
					<-release
				}
			}
			e.onGet.Store(&hook)
			changed := make(chan struct{}, 8)
			c := e.client(t, 10*time.Millisecond, func(o *Options) {
				o.OnChange = func(string) { changed <- struct{}{} }
			})
			ctx, cancel := safetyCtx(t)
			var restarts atomic.Int32
			done := e.startRestart(ctx, c, func() { restarts.Add(1) })
			waitSignal(t, started, "connect check GET in flight")
			e.send(tt.events...)
			for i := range tt.events {
				waitSignal(t, changed, fmt.Sprintf("OnChange #%d", i+1))
			}
			unblock()
			for i := range tt.want {
				waitSignal(t, e.hitCh, fmt.Sprintf("GET #%d", i+1))
			}
			negativeWait()
			wantHits(t, e, tt.want)
			cancel()
			wantCanceled(t, done)
			wantRestarts(t, &restarts, 0)
		})
	}
}

func TestRestartOnChange_VersionNotUsed(t *testing.T) { // N5
	tests := []struct{ name, event string }{
		{"same version", rev("updated", 5)},
		{"lower version", rev("updated", 2)},
		{"created v1", rev("created", 1)},
		{"version zero", rev("updated", 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRestartEnv(t)
			c := e.client(t, 20*time.Millisecond, nil)
			ctx, _ := safetyCtx(t)
			var restarts atomic.Int32
			done := e.startSynced(t, ctx, c, func() { restarts.Add(1) })
			e.send(rev("updated", 5))
			waitSignal(t, e.hitCh, "GET #2")
			e.setHost("new")
			e.send(tt.event)
			wantNil(t, done)
			wantRestarts(t, &restarts, 1)
			wantHits(t, e, 3)
		})
	}
}
