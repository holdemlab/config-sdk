package configsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/holdemlab/config-sdk/internal/sse"
)

// Watch subscribes to configuration changes via SSE.
// It calls the callback for each change event.
// Blocks until the context is cancelled. Automatically reconnects on connection drops.
func (c *Client) Watch(ctx context.Context, configName string, callback WatchCallback) error {
	path := "/api/v1/service/configs/" + configName + "/watch"
	url := c.host + path

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

	for {
		err := c.watchOnce(watchCtx, url, callback)
		if err == nil {
			// Stream ended normally (server closed); reconnect
		} else if watchCtx.Err() != nil {
			return watchCtx.Err()
		} else {
			c.log().Warn("watch connection lost, reconnecting",
				"config", configName,
				"error", err,
			)
			if c.onError != nil {
				c.onError(err)
			}
		}

		// Wait before reconnecting with backoff capped at retryDelay
		select {
		case <-watchCtx.Done():
			return watchCtx.Err()
		case <-time.After(c.retryDelay):
		}
	}
}

// watchOnce opens a single SSE connection and processes events until the stream ends or an error occurs.
func (c *Client) watchOnce(ctx context.Context, url string, callback WatchCallback) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}
	req.Header.Set("X-Service-Token", c.serviceToken)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}
	defer resp.Body.Close()

	if err := mapStatusError(resp.StatusCode); err != nil {
		return err
	}

	reader := sse.NewReader(resp.Body)
	for {
		event, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		if event.Data == "" {
			continue
		}

		var changeEvent ConfigChangeEvent
		if err := json.Unmarshal([]byte(event.Data), &changeEvent); err != nil {
			c.log().Warn("failed to parse SSE event data",
				"error", err,
				"data", event.Data,
			)
			if c.onError != nil {
				c.onError(fmt.Errorf("%w: %v", ErrUnmarshal, err))
			}
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
// dst must be a pointer to a struct. Updates are protected by sync.RWMutex
// for thread safety. Blocks until the context is cancelled.
func (c *Client) WatchAndDecode(ctx context.Context, configName string, dst interface{}) error {
	dstVal := reflect.ValueOf(dst)
	if dstVal.Kind() != reflect.Ptr || dstVal.IsNil() {
		return fmt.Errorf("%w: dst must be a non-nil pointer", ErrInvalidResponse)
	}

	var mu sync.RWMutex

	return c.Watch(ctx, configName, func(event ConfigChangeEvent) {
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

		mu.Lock()
		dstVal.Elem().Set(reflect.ValueOf(newVal).Elem())
		mu.Unlock()
	})
}
