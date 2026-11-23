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

func listClient(t *testing.T, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return newWatchTestClient(t, srv, nil)
}

func TestList_Envelope(t *testing.T) {
	c := listClient(t, `{"data":[{"name":"db","is_valid":true,"valid_from":"2025-01-01T00:00:00Z","updated_at":"2025-06-15T12:00:00Z"},{"name":"cache","is_valid":false,"valid_from":"2025-02-01T00:00:00Z","updated_at":"2025-06-10T08:00:00Z"}],"extra":1}`)
	got, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Name != "db" || !got[0].IsValid || got[1].Name != "cache" || got[1].IsValid {
		t.Errorf("unexpected items: %+v", got)
	}
	if want := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC); !got[0].UpdatedAt.Equal(want) {
		t.Errorf("UpdatedAt = %v, want %v", got[0].UpdatedAt, want)
	}
}

func TestList_EnvelopeEmpty(t *testing.T) {
	got, err := listClient(t, `{"data":[]}`).List(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want empty, nil", got, err)
	}
}

func TestList_BareArrayLeadingWhitespace(t *testing.T) {
	got, err := listClient(t, " \n\t[{\"name\":\"db\"}]").List(context.Background())
	if err != nil || len(got) != 1 || got[0].Name != "db" {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestList_BadEnvelope(t *testing.T) {
	for name, body := range map[string]string{
		"empty object":   `{}`,
		"null data":      `{"data":null}`,
		"data not array": `{"data":"x"}`,
		"data object":    `{"data":{"name":"db"}}`,
		"scalar":         `42`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := listClient(t, body).List(context.Background())
			if !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("got %v, want ErrInvalidResponse", err)
			}
		})
	}
}
