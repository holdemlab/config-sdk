package configsdk

import (
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client is the main client for interacting with Config Service.
type Client struct {
	host          string
	serviceToken  string
	encryptionKey []byte

	httpClient     *http.Client
	requestTimeout time.Duration
	retryCount     int
	retryDelay     time.Duration

	watchIdleTimeout time.Duration // <= 0 disables the watch idle timeout
	logger           *slog.Logger

	onError  func(error)
	onChange func(configName string)

	closeCh   chan struct{}
	closeOnce sync.Once
}

// New creates a new Config Service client with the given options.
// It validates all required parameters and returns an error if any are missing or invalid.
func New(opts Options) (*Client, error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
	}

	key, err := hex.DecodeString(opts.EncryptionKey)
	if err != nil {
		return nil, errors.New("configsdk: encryption key is not valid hex")
	}

	opts.applyDefaults()

	// Copy the HTTP client so the caller's instance is never mutated, and never
	// follow redirects (the service token must not be forwarded to other hosts).
	hc := *opts.HTTPClient
	hc.CheckRedirect = noRedirect

	return &Client{
		host:             strings.TrimRight(opts.Host, "/"),
		serviceToken:     opts.ServiceToken,
		encryptionKey:    key,
		httpClient:       &hc,
		requestTimeout:   opts.RequestTimeout,
		retryCount:       opts.RetryCount,
		retryDelay:       opts.RetryDelay,
		watchIdleTimeout: opts.WatchIdleTimeout,
		logger:           opts.Logger,
		onError:          opts.OnError,
		onChange:         opts.OnChange,
		closeCh:          make(chan struct{}),
	}, nil
}

func validateOptions(opts Options) error {
	if opts.Host == "" {
		return errors.New("configsdk: Host is required")
	}
	u, err := url.Parse(opts.Host)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		// The Host value is deliberately not echoed: it may contain credentials.
		return errors.New("configsdk: Host must be an absolute http(s) URL")
	}
	if u.Scheme == "http" && !opts.AllowInsecureHTTP && !isLoopbackHost(u.Hostname()) {
		return ErrInsecureHost
	}
	if opts.ServiceToken == "" {
		return errors.New("configsdk: ServiceToken is required")
	}
	if opts.EncryptionKey == "" {
		return errors.New("configsdk: EncryptionKey is required")
	}
	if len(opts.EncryptionKey) != 64 {
		return errors.New("configsdk: EncryptionKey must be 64 hex characters (32 bytes)")
	}
	return nil
}

func (c *Client) log() *slog.Logger {
	if c.logger != nil {
		return c.logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// noRedirect makes the HTTP client return 3xx responses instead of following them.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// isLoopbackHost reports whether host is "localhost" or a loopback IP literal.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Close closes the client and terminates all active SSE watch connections.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.closeCh)
	})
	return nil
}

// NewFromEnv creates a new Client using environment variables:
//   - CONFIG_SERVICE_HOST
//   - CONFIG_SERVICE_TOKEN
//   - CONFIG_SERVICE_KEY
//   - CONFIG_SERVICE_ALLOW_INSECURE_HTTP (optional, boolean; permits plain-http non-loopback hosts)
func NewFromEnv() (*Client, error) {
	allowHTTP, _ := strconv.ParseBool(os.Getenv("CONFIG_SERVICE_ALLOW_INSECURE_HTTP"))
	return New(Options{
		AllowInsecureHTTP: allowHTTP,
		Host:              os.Getenv("CONFIG_SERVICE_HOST"),
		ServiceToken:      os.Getenv("CONFIG_SERVICE_TOKEN"),
		EncryptionKey:     os.Getenv("CONFIG_SERVICE_KEY"),
	})
}
