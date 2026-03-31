package configsdk

import (
"context"
"errors"
"net/http"
"net/http/httptest"
"testing"
"time"
)

func TestList_Success(t *testing.T) {
	response := `[{"name":"db","is_valid":true,"valid_from":"2025-01-01T00:00:00Z","updated_at":"2025-06-15T12:00:00Z"},{"name":"cache","is_valid":false,"valid_from":"2025-02-01T00:00:00Z","updated_at":"2025-06-10T08:00:00Z"}]`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
if r.URL.Path != "/api/v1/service/configs" {
t.Errorf("path = %q, want /api/v1/service/configs", r.URL.Path)
}
if got := r.Header.Get("X-Service-Token"); got != "test-token" {
			t.Errorf("X-Service-Token = %q, want %q", got, "test-token")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer srv.Close()

	client, _ := New(Options{
Host:          srv.URL,
ServiceToken:  "test-token",
EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
HTTPClient:    srv.Client(),
	})

	configs, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(configs) != 2 {
		t.Fatalf("len = %d, want 2", len(configs))
	}

	if configs[0].Name != "db" {
		t.Errorf("configs[0].Name = %q, want %q", configs[0].Name, "db")
	}
	if !configs[0].IsValid {
		t.Error("configs[0].IsValid = false, want true")
	}
	if configs[1].Name != "cache" {
		t.Errorf("configs[1].Name = %q, want %q", configs[1].Name, "cache")
	}
	if configs[1].IsValid {
		t.Error("configs[1].IsValid = true, want false")
	}
}

func TestList_EmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
w.WriteHeader(http.StatusOK)
w.Write([]byte("[]"))
}))
	defer srv.Close()

	client, _ := New(Options{
Host:          srv.URL,
ServiceToken:  "token",
EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
HTTPClient:    srv.Client(),
	})

	configs, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(configs) != 0 {
		t.Fatalf("len = %d, want 0", len(configs))
	}
}

func TestList_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
w.WriteHeader(http.StatusUnauthorized)
}))
	defer srv.Close()

	client, _ := New(Options{
Host:          srv.URL,
ServiceToken:  "bad-token",
EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
HTTPClient:    srv.Client(),
	})

	_, err := client.List(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v, want ErrUnauthorized", err)
	}
}

func TestList_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
w.WriteHeader(http.StatusOK)
w.Write([]byte("not json"))
}))
	defer srv.Close()

	client, _ := New(Options{
Host:          srv.URL,
ServiceToken:  "token",
EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
HTTPClient:    srv.Client(),
	})

	_, err := client.List(context.Background())
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("got %v, want ErrInvalidResponse", err)
	}
}

func TestList_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
w.WriteHeader(http.StatusInternalServerError)
}))
	defer srv.Close()

	client, _ := New(Options{
Host:           srv.URL,
ServiceToken:   "token",
EncryptionKey:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
HTTPClient:     srv.Client(),
		RetryCount:     1,
		RetryDelay:     time.Millisecond,
		RequestTimeout: 5 * time.Second,
	})

	_, err := client.List(context.Background())
	if !errors.Is(err, ErrConnectionFailed) {
		t.Fatalf("got %v, want ErrConnectionFailed", err)
	}
}
