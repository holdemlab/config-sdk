package configsdk

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/holdemlab/config-sdk/internal/sse"
)

const (
	watchMaxBackoff    = 30 * time.Second
	defaultEnvironment = "default"
)

// watchPayload is the wire shape of a config_changed event. It deliberately has
// no Data field: config-service before v0.9.2 sent the decrypted config there,
// and the SDK never decodes it.
type watchPayload struct {
	Type          string    `json:"Type"`
	ConfigName    string    `json:"ConfigName"`
	ConfigNameLow string    `json:"config_name"`
	Env           string    `json:"Env"`
	ChangedBy     int       `json:"ChangedBy"`
	ChangedByLow  int       `json:"changed_by"`
	Version       int       `json:"Version"`
	Timestamp     time.Time `json:"Timestamp"`
}

func (p watchPayload) event() ConfigChangeEvent {
	return ConfigChangeEvent{
		ConfigName:  cmp.Or(p.ConfigName, p.ConfigNameLow),
		Version:     p.Version,
		ChangedBy:   cmp.Or(p.ChangedBy, p.ChangedByLow),
		Timestamp:   p.Timestamp,
		Type:        p.Type,
		Environment: p.Env,
	}
}

// errWatchIdle signals that the connection was idle for too long. Never returned from Watch.
var errWatchIdle = errors.New("configsdk: watch idle timeout")

// watchBackoffDelay returns the delay before reconnecting after the given number of
// consecutive failures: exponential from retryDelay, plus up to 50% jitter, capped at 30s.
func (c *Client) watchBackoffDelay(failures int) time.Duration {
	base := c.retryDelay
	for i := 1; i < failures && base < watchMaxBackoff; i++ {
		base *= 2
	}
	if base >= watchMaxBackoff {
		return watchMaxBackoff
	}
	jitter := time.Duration(0)
	if half := int64(base / 2); half > 0 {
		jitter = time.Duration(rand.Int63n(half))
	}
	return min(base+jitter, watchMaxBackoff)
}

// Watch subscribes to configuration changes via SSE.
// It calls the callback for each change event.
// Blocks until the context is cancelled, or until the server answers 401, 403 or 404
// (the error is then returned; call Watch again to keep waiting).
// Other failures reconnect automatically with exponential backoff capped at 30s.
func (c *Client) Watch(ctx context.Context, configName string, callback WatchCallback) error {
	return c.watch(ctx, configName, callback, nil)
}

// watch is Watch plus onConnect, which is called on the watch goroutine every
// time a stream is established (2xx response), before any event of that
// stream is delivered. onConnect may be nil and must not block.
func (c *Client) watch(ctx context.Context, configName string, callback WatchCallback, onConnect func()) error {
	path := "/api/v1/service/configs/" + url.PathEscape(configName) + "/watch"
	target := c.host + path

	// Derive a context that cancels on both user cancellation and client Close()
	watchCtx, watchCancel := context.WithCancel(ctx)
	defer watchCancel()

	go func() {
		select {
		case <-c.closeCh:
			watchCancel()
		case <-watchCtx.Done():
		}
	}()

	failures := 0
	for {
		established, err := c.watchOnce(watchCtx, target, callback, onConnect)
		switch {
		case watchCtx.Err() != nil:
			return watchCtx.Err()
		case errors.Is(err, errWatchIdle):
			c.log().Debug("watch idle, reconnecting", "config", configName)
			continue
		case errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound):
			c.log().Error("watch terminated", "config", configName, "error", err)
			if c.onError != nil {
				c.onError(err)
			}
			return err
		case err != nil:
			c.log().Warn("watch connection lost, reconnecting",
				"config", configName,
				"error", err,
			)
			if c.onError != nil {
				c.onError(err)
			}
		}

		if established {
			failures = 0
		} else {
			failures++
		}

		select {
		case <-watchCtx.Done():
			return watchCtx.Err()
		case <-time.After(c.watchBackoffDelay(failures)):
		}
	}
}

// idleBody restarts the idle timer around each blocking Read.
type idleBody struct {
	io.Reader
	timer *time.Timer
	d     time.Duration
}

func (b *idleBody) Read(p []byte) (int, error) {
	b.timer.Reset(b.d)
	n, err := b.Reader.Read(p)
	b.timer.Stop()
	return n, err
}

// watchOnce opens a single SSE connection and processes events until the stream ends or an error occurs.
// established reports whether a 2xx response was received.
func (c *Client) watchOnce(ctx context.Context, rawURL string, callback WatchCallback, onConnect func()) (established bool, err error) {
	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var idle atomic.Bool
	var timer *time.Timer
	if c.watchIdleTimeout > 0 {
		timer = time.AfterFunc(c.watchIdleTimeout, func() {
			idle.Store(true)
			cancel()
		})
		defer timer.Stop()
	}
	// fail maps an I/O error to errWatchIdle when the idle timer caused it.
	fail := func(err error) error {
		if idle.Load() && ctx.Err() == nil {
			return errWatchIdle
		}
		return err
	}

	req, err := http.NewRequestWithContext(connCtx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}
	req.Header.Set("X-Service-Token", c.serviceToken)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if timer != nil {
		timer.Stop()
	}
	if err != nil {
		return false, fail(fmt.Errorf("%w: %v", ErrConnectionFailed, err))
	}
	defer func() { _ = resp.Body.Close() }()

	if err := mapStatusError(resp.StatusCode); err != nil {
		return false, err
	}
	if onConnect != nil {
		onConnect()
	}

	var body io.Reader = resp.Body
	if timer != nil {
		body = &idleBody{Reader: resp.Body, timer: timer, d: c.watchIdleTimeout}
	}
	reader := sse.NewReader(body)
	for {
		event, err := reader.Next()
		if err == io.EOF {
			return true, nil
		}
		if err != nil {
			err = fail(err)
			switch {
			case errors.Is(err, errWatchIdle):
				return true, err
			case errors.Is(err, sse.ErrEventTooLarge):
				return true, fmt.Errorf("%w: %v", ErrResponseTooLarge, err)
			}
			return true, fmt.Errorf("%w: %v", ErrConnectionFailed, err)
		}

		if ctx.Err() != nil { // cancelled by a callback: drop events already buffered
			return true, ctx.Err()
		}

		if event.Data == "" {
			continue
		}

		if event.Type != "" && event.Type != "config_changed" {
			continue
		}

		var wire watchPayload
		if err := json.Unmarshal([]byte(event.Data), &wire); err != nil {
			c.log().Warn("failed to parse SSE event data",
				"error", err,
				"data_len", len(event.Data),
			)
			if c.onError != nil {
				c.onError(fmt.Errorf("%w: %v", ErrUnmarshal, err))
			}
			continue
		}

		changeEvent := wire.event()
		if cmp.Or(changeEvent.Environment, defaultEnvironment) != cmp.Or(c.environment, defaultEnvironment) {
			c.log().Debug("watch event for other environment skipped",
				"config", changeEvent.ConfigName,
				"env", changeEvent.Environment,
			)
			continue
		}

		callback(changeEvent)

		if c.onChange != nil {
			c.onChange(changeEvent.ConfigName)
		}
	}
}

// WatchAndDecode subscribes to configuration changes via SSE and automatically
// re-fetches and unmarshals the configuration into dst on each change.
// dst must be a pointer to a struct. Blocks until the context is cancelled.
//
// Writes to *dst are not synchronised: reading dst from another goroutine while
// WatchAndDecode runs is a data race.
//
// Deprecated: use WatchSnapshot.
func (c *Client) WatchAndDecode(ctx context.Context, configName string, dst interface{}) error {
	dstVal := reflect.ValueOf(dst)
	if dstVal.Kind() != reflect.Pointer || dstVal.IsNil() {
		return fmt.Errorf("%w: dst must be a non-nil pointer", ErrInvalidResponse)
	}

	return c.Watch(ctx, configName, func(_ ConfigChangeEvent) {
		// Create a new instance of the same type as dst points to
		newVal := reflect.New(dstVal.Elem().Type()).Interface()

		if err := c.Get(ctx, configName, newVal); err != nil {
			c.log().Warn("WatchAndDecode: failed to fetch config",
				"config", configName,
				"error", err,
			)
			if c.onError != nil {
				c.onError(err)
			}
			return
		}

		dstVal.Elem().Set(reflect.ValueOf(newVal).Elem())
	})
}

// WatchSnapshot watches configName and stores the freshly fetched and decoded
// config in snap on every change event; snap is safe for concurrent reads.
// There is no initial fetch. Return values are identical to Watch.
func WatchSnapshot[T any](ctx context.Context, c *Client, configName string, snap *Snapshot[T]) error {
	if snap == nil {
		return fmt.Errorf("%w: snap must be non-nil", ErrInvalidResponse)
	}
	return c.Watch(ctx, configName, func(_ ConfigChangeEvent) {
		var v T
		if err := c.Get(ctx, configName, &v); err != nil {
			c.log().Warn("WatchSnapshot: failed to fetch config",
				"config", configName,
				"error", err,
			)
			if c.onError != nil {
				c.onError(err)
			}
			return
		}
		snap.Store(v)
	})
}
