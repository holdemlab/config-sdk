package configsdk

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoRequest_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Service-Token"); got != "test-token" {
			t.Errorf("X-Service-Token = %q, want %q", got, "test-token")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := &Client{
		host:           srv.URL,
		serviceToken:   "test-token",
		httpClient:     srv.Client(),
		requestTimeout: 5 * time.Second,
		retryCount:     0,
		retryDelay:     time.Millisecond,
	}

	body, _, err := c.doRequest(context.Background(), "GET", "/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != "ok" {
		t.Fatalf("body = %q, want %q", body, "ok")
	}
}

func TestDoRequest_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := &Client{
		host:           srv.URL,
		serviceToken:   "bad-token",
		httpClient:     srv.Client(),
		requestTimeout: 5 * time.Second,
		retryCount:     2,
		retryDelay:     time.Millisecond,
	}

	_, _, err := c.doRequest(context.Background(), "GET", "/test")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v, want ErrUnauthorized", err)
	}
}

func TestDoRequest_403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := &Client{
		host:           srv.URL,
		serviceToken:   "token",
		httpClient:     srv.Client(),
		requestTimeout: 5 * time.Second,
		retryCount:     2,
		retryDelay:     time.Millisecond,
	}

	_, _, err := c.doRequest(context.Background(), "GET", "/test")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("got %v, want ErrForbidden", err)
	}
}

func TestDoRequest_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := &Client{
		host:           srv.URL,
		serviceToken:   "token",
		httpClient:     srv.Client(),
		requestTimeout: 5 * time.Second,
		retryCount:     2,
		retryDelay:     time.Millisecond,
	}

	_, _, err := c.doRequest(context.Background(), "GET", "/test")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestDoRequest_NoRetryOn4xx(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := &Client{
		host:           srv.URL,
		serviceToken:   "token",
		httpClient:     srv.Client(),
		requestTimeout: 5 * time.Second,
		retryCount:     3,
		retryDelay:     time.Millisecond,
	}

	_, _, err := c.doRequest(context.Background(), "GET", "/test")
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("got %v, want ErrInvalidResponse", err)
	}
	if got := atomic.LoadInt32(&count); got != 1 {
		t.Fatalf("request count = %d, want 1 (no retry on 4xx)", got)
	}
}

func TestDoRequest_RetryOn5xx(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&count, 1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("recovered"))
	}))
	defer srv.Close()

	c := &Client{
		host:           srv.URL,
		serviceToken:   "token",
		httpClient:     srv.Client(),
		requestTimeout: 5 * time.Second,
		retryCount:     3,
		retryDelay:     time.Millisecond,
	}

	body, _, err := c.doRequest(context.Background(), "GET", "/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != "recovered" {
		t.Fatalf("body = %q, want %q", body, "recovered")
	}
	if got := atomic.LoadInt32(&count); got != 3 {
		t.Fatalf("request count = %d, want 3", got)
	}
}

func TestDoRequest_RetryExhausted(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := &Client{
		host:           srv.URL,
		serviceToken:   "token",
		httpClient:     srv.Client(),
		requestTimeout: 5 * time.Second,
		retryCount:     2,
		retryDelay:     time.Millisecond,
	}

	_, _, err := c.doRequest(context.Background(), "GET", "/test")
	if !errors.Is(err, ErrConnectionFailed) {
		t.Fatalf("got %v, want ErrConnectionFailed", err)
	}
	// 1 initial + 2 retries = 3 total
	if got := atomic.LoadInt32(&count); got != 3 {
		t.Fatalf("request count = %d, want 3", got)
	}
}

func TestDoRequest_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := &Client{
		host:           srv.URL,
		serviceToken:   "token",
		httpClient:     srv.Client(),
		requestTimeout: 5 * time.Second,
		retryCount:     10,
		retryDelay:     100 * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, _, err := c.doRequest(ctx, "GET", "/test")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestDoRequest_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &Client{
		host:           srv.URL,
		serviceToken:   "token",
		httpClient:     srv.Client(),
		requestTimeout: 50 * time.Millisecond,
		retryCount:     0,
		retryDelay:     time.Millisecond,
	}

	_, _, err := c.doRequest(context.Background(), "GET", "/test")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
