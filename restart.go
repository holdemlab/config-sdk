package configsdk

import (
	"context"
	"errors"
	"math/rand"
	"reflect"
	"time"
)

// restartDelay returns the debounce window: restartDebounce plus a random
// addition in [0, restartJitter).
func (c *Client) restartDelay() time.Duration {
	if c.restartJitter <= 0 {
		return c.restartDebounce
	}
	return c.restartDebounce + time.Duration(rand.Int63n(int64(c.restartJitter)))
}

var errNilRestart = errors.New("configsdk: restart must be non-nil")

// RestartOnChange watches configName and calls restart exactly once when the
// config content really differs from current, then returns nil.
//
// It blocks; typical use is:
//
//	go RestartOnChange(ctx, client, name, cfg, stop)
//
// current must be decoded by Get into a zero value of T (var cfg T;
// client.Get(ctx, name, &cfg)) with the client's environment
// (Options.Environment / CONFIG_SERVICE_ENVIRONMENT), not by GetWithOptions
// with a per-call Environment: RestartOnChange always checks the client
// environment. Do not pre-fill the value before Get and do not post-process it;
// a copy you modify must not share maps, slices or pointers with current
// (decode a second value with another Get, or deep-copy; a plain assignment
// shares them). Otherwise the first check
// after the watch connects restarts the service, on every start. Values are
// compared with reflect.DeepEqual: unexported fields count, and a config that
// holds a NaN float or a non-nil func field never compares equal, so it has
// the same effect.
//
// A check (fetch and compare) runs every time the watch stream connects or
// reconnects, including the first connection, and on every change event
// except "deleted". Reconnect checks are rate-limited to one per 30s after a
// successful check: a reconnect inside that window postpones its check until
// the window ends (it is never dropped), so a change made during an SSE gap is
// always noticed, at most ~30s later. Change events are never delayed by this.
// Checks are debounced (a fixed window from the first trigger, with random
// jitter): a burst causes one fetch, and triggers that arrive during a fetch
// cause exactly one more check.
//
// A failed fetch never restarts the service; every failed attempt is logged
// and reported via Options.OnError. Every error except ErrNotFound,
// ErrUnauthorized, ErrForbidden, ErrDecryptionFailed, ErrUnmarshal and
// ErrResponseTooLarge is retried automatically with the Watch reconnect
// backoff (exponential from Options.RetryDelay, capped at 30s) until it
// succeeds, a newer change event or reconnect arrives (which starts a fresh
// debounce window), ctx is cancelled or the Client is closed. Consecutive
// failures keep increasing the backoff, even across new events, until a fetch
// succeeds. The listed errors are not retried: the service keeps running and
// the next change event or reconnect triggers a new check.
//
// It returns nil after restart was called (even if ctx was cancelled
// concurrently). Otherwise it returns the error of Watch without calling
// restart: ctx.Err() on cancel, context.Canceled after Client.Close, or
// ErrUnauthorized/ErrForbidden/ErrNotFound on 401/403/404.
func RestartOnChange[T any](ctx context.Context, c *Client, configName string, current T, restart func()) error {
	if restart == nil {
		return errNilRestart
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	check := make(chan struct{}, 1)
	trigger := func() {
		select {
		case check <- struct{}{}:
		default:
		}
	}
	connect := make(chan struct{}, 1)
	done := make(chan struct{})
	changed := false

	go func() {
		defer close(done)
		var window, deferred <-chan time.Time
		var lastOK time.Time // last successful fetch; owned by this goroutine
		var failures int
		var backoff bool // window is a backoff wait, not a debounce
		schedule := func() {
			if window == nil || backoff {
				window = time.After(c.restartDelay())
				backoff = false
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-check:
				schedule()
			case <-connect:
				if since := time.Since(lastOK); !lastOK.IsZero() && since < c.restartConnectCooldown {
					if deferred == nil { // postpone, never drop
						deferred = time.After(c.restartConnectCooldown - since)
					}
				} else {
					schedule()
				}
			case <-deferred:
				deferred = nil
				schedule()
			case <-window:
				window, backoff = nil, false
				select { // a token already queued precedes this fetch, which covers it
				case <-check:
				default:
				}
				var next T
				if err := c.Get(ctx, configName, &next); err != nil {
					if ctx.Err() != nil {
						return
					}
					c.log().Warn("RestartOnChange: failed to fetch config", "config", configName, "error", err)
					if c.onError != nil {
						c.onError(err)
					}
					if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrUnauthorized) && !errors.Is(err, ErrForbidden) &&
						!errors.Is(err, ErrDecryptionFailed) && !errors.Is(err, ErrUnmarshal) && !errors.Is(err, ErrResponseTooLarge) {
						failures++
						window, backoff = time.After(c.watchBackoffDelay(failures)), true
					}
					continue
				}
				failures = 0
				lastOK, deferred = time.Now(), nil
				if !reflect.DeepEqual(next, current) {
					c.log().Info("RestartOnChange: config changed", "config", configName)
					changed = true
					cancel()
					return
				}
			}
		}
	}()

	err := c.watch(ctx, configName, func(ev ConfigChangeEvent) {
		if ev.Type != "deleted" {
			trigger()
		}
	}, func() {
		select {
		case connect <- struct{}{}:
		default:
		}
	})
	cancel()
	<-done
	if changed {
		restart()
		return nil
	}
	return err
}
