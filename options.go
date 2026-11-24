package configsdk

import (
	"log/slog"
	"net/http"
	"time"
)

const (
	defaultRequestTimeout = 10 * time.Second
	defaultRetryCount     = 3
	defaultRetryDelay     = 1 * time.Second

	defaultWatchIdleTimeout = 5 * time.Minute

	defaultRestartDebounce = 2 * time.Second
	defaultRestartJitter   = 2 * time.Second

	// defaultRestartConnectCooldown: a reconnect this soon after a successful
	// RestartOnChange check postpones its check until this much time has passed.
	defaultRestartConnectCooldown = 30 * time.Second
)

// Options contains the configuration for connecting to Config Service.
type Options struct {
	// Host is the base URL of Config Service (e.g. "https://config.example.com"). Required.
	Host string

	// ServiceToken is a plain-text service token obtained during token creation. Required.
	ServiceToken string

	// EncryptionKey is a hex-encoded AES-256 key (64 hex characters = 32 bytes). Required.
	EncryptionKey string

	// HTTPClient is an optional custom HTTP client. Defaults to http.DefaultClient.
	// It is copied (never mutated) and redirects are never followed.
	HTTPClient *http.Client

	// RequestTimeout is the timeout for individual HTTP requests. Default: 10s.
	RequestTimeout time.Duration

	// RetryCount is the number of retry attempts for retriable errors. Default: 3.
	RetryCount int

	// RetryDelay is the base delay between retries (with exponential backoff). Default: 1s.
	RetryDelay time.Duration

	// Logger is an optional structured logger. If nil, logging is disabled.
	Logger *slog.Logger

	// OnError is called when an error occurs in watch mode. Optional.
	OnError func(error)

	// OnChange is called when a configuration changes in watch mode. Optional.
	OnChange func(configName string)

	// AllowInsecureHTTP permits a plain-http Host that is not loopback. Default: false.
	AllowInsecureHTTP bool

	// WatchIdleTimeout is the maximum time Watch waits for response headers or for
	// any bytes on the SSE stream before reconnecting.
	// 0 = default (5m). Negative = disabled.
	WatchIdleTimeout time.Duration

	// Environment is the config environment used by every call unless overridden
	// per call via GetOptions.Environment. Empty means the service default ("default").
	Environment string
}

func (o *Options) applyDefaults() {
	if o.HTTPClient == nil {
		o.HTTPClient = http.DefaultClient
	}
	if o.RequestTimeout <= 0 {
		o.RequestTimeout = defaultRequestTimeout
	}
	if o.RetryCount <= 0 {
		o.RetryCount = defaultRetryCount
	}
	if o.RetryDelay <= 0 {
		o.RetryDelay = defaultRetryDelay
	}
	if o.WatchIdleTimeout == 0 {
		o.WatchIdleTimeout = defaultWatchIdleTimeout
	}
}
