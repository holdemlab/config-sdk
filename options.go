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
}
