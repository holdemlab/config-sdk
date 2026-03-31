package configsdk

import (
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
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
	logger         *slog.Logger

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

	return &Client{
		host:           strings.TrimRight(opts.Host, "/"),
		serviceToken:   opts.ServiceToken,
		encryptionKey:  key,
		httpClient:     opts.HTTPClient,
		requestTimeout: opts.RequestTimeout,
		retryCount:     opts.RetryCount,
		retryDelay:     opts.RetryDelay,
		logger:         opts.Logger,
		onError:        opts.OnError,
		onChange:       opts.OnChange,
		closeCh:        make(chan struct{}),
	}, nil
}

func validateOptions(opts Options) error {
	if opts.Host == "" {
		return errors.New("configsdk: Host is required")
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

// maskToken returns a masked version of the service token for safe logging.
// Shows "****" + last 4 characters.
func maskToken(token string) string {
	if len(token) <= 4 {
		return "****"
	}
	return "****" + token[len(token)-4:]
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
func NewFromEnv() (*Client, error) {
	return New(Options{
		Host:          os.Getenv("CONFIG_SERVICE_HOST"),
		ServiceToken:  os.Getenv("CONFIG_SERVICE_TOKEN"),
		EncryptionKey: os.Getenv("CONFIG_SERVICE_KEY"),
	})
}
