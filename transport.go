package configsdk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"time"
)

// doRequest performs an HTTP request with retry logic, auth headers, and error mapping.
// It retries on 5xx and network errors with exponential backoff + jitter.
// It does NOT retry on 4xx (client errors).
func (c *Client) doRequest(ctx context.Context, method, path string) ([]byte, http.Header, error) {
	url := c.host + path

	var lastErr error
	for attempt := 0; attempt <= c.retryCount; attempt++ {
		if attempt > 0 {
			delay := c.backoffDelay(attempt)
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		body, headers, err := c.executeRequest(ctx, method, url)
		if err == nil {
			return body, headers, nil
		}

		// Do not retry client errors (4xx)
		if !isRetriable(err) {
			return nil, nil, err
		}

		lastErr = err
		c.log().Warn("request failed, retrying",
			"attempt", attempt+1,
			"max_attempts", c.retryCount+1,
			"error", err,
		)
	}

	return nil, nil, fmt.Errorf("%w: %v", ErrConnectionFailed, lastErr)
}

func (c *Client) executeRequest(ctx context.Context, method, url string) ([]byte, http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}

	req.Header.Set("X-Service-Token", c.serviceToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, &retriableError{err: fmt.Errorf("%w: %v", ErrConnectionFailed, err)}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, &retriableError{err: fmt.Errorf("%w: %v", ErrConnectionFailed, err)}
	}

	if err := mapStatusError(resp.StatusCode); err != nil {
		return nil, nil, err
	}

	return body, resp.Header, nil
}

// mapStatusError maps HTTP status codes to sentinel errors.
func mapStatusError(code int) error {
	switch {
	case code >= 200 && code < 300:
		return nil
	case code == http.StatusUnauthorized:
		return ErrUnauthorized
	case code == http.StatusForbidden:
		return ErrForbidden
	case code == http.StatusNotFound:
		return ErrNotFound
	case code >= 400 && code < 500:
		return fmt.Errorf("%w: HTTP %d", ErrInvalidResponse, code)
	default:
		// 5xx — retriable
		return &retriableError{err: fmt.Errorf("%w: HTTP %d", ErrConnectionFailed, code)}
	}
}

// backoffDelay calculates exponential backoff with jitter.
func (c *Client) backoffDelay(attempt int) time.Duration {
	base := float64(c.retryDelay) * math.Pow(2, float64(attempt-1))
	jitter := rand.Float64() * 0.5 * base // 0–50% jitter
	return time.Duration(base + jitter)
}

// retriableError wraps an error to signal that the request can be retried.
type retriableError struct {
	err error
}

func (e *retriableError) Error() string { return e.err.Error() }
func (e *retriableError) Unwrap() error { return e.err }

func isRetriable(err error) bool {
	var re *retriableError
	return errors.As(err, &re)
}
