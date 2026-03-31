package configsdk

import (
	"os"
	"testing"
	"time"
)

func TestNew_ValidOptions(t *testing.T) {
	c, err := New(Options{
		Host:          "https://config.example.com",
		ServiceToken:  "my-token",
		EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.host != "https://config.example.com" {
		t.Errorf("host = %q", c.host)
	}
	if c.serviceToken != "my-token" {
		t.Errorf("serviceToken = %q", c.serviceToken)
	}
	if len(c.encryptionKey) != 32 {
		t.Errorf("encryptionKey len = %d, want 32", len(c.encryptionKey))
	}
	// Defaults
	if c.requestTimeout != 10*time.Second {
		t.Errorf("requestTimeout = %v, want 10s", c.requestTimeout)
	}
	if c.retryCount != 3 {
		t.Errorf("retryCount = %d, want 3", c.retryCount)
	}
	if c.retryDelay != 1*time.Second {
		t.Errorf("retryDelay = %v, want 1s", c.retryDelay)
	}
	if c.httpClient == nil {
		t.Error("httpClient is nil")
	}
}

func TestNew_TrimsTrailingSlash(t *testing.T) {
	c, err := New(Options{
		Host:          "https://config.example.com/",
		ServiceToken:  "token",
		EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.host != "https://config.example.com" {
		t.Errorf("host = %q, want trailing slash trimmed", c.host)
	}
}

func TestNew_MissingHost(t *testing.T) {
	_, err := New(Options{
		ServiceToken:  "token",
		EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})
	if err == nil {
		t.Fatal("expected error for missing Host")
	}
}

func TestNew_MissingServiceToken(t *testing.T) {
	_, err := New(Options{
		Host:          "https://example.com",
		EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})
	if err == nil {
		t.Fatal("expected error for missing ServiceToken")
	}
}

func TestNew_MissingEncryptionKey(t *testing.T) {
	_, err := New(Options{
		Host:         "https://example.com",
		ServiceToken: "token",
	})
	if err == nil {
		t.Fatal("expected error for missing EncryptionKey")
	}
}

func TestNew_ShortEncryptionKey(t *testing.T) {
	_, err := New(Options{
		Host:          "https://example.com",
		ServiceToken:  "token",
		EncryptionKey: "0123456789abcdef",
	})
	if err == nil {
		t.Fatal("expected error for short EncryptionKey")
	}
}

func TestNew_InvalidHexEncryptionKey(t *testing.T) {
	_, err := New(Options{
		Host:          "https://example.com",
		ServiceToken:  "token",
		EncryptionKey: "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
	})
	if err == nil {
		t.Fatal("expected error for invalid hex EncryptionKey")
	}
}

func TestMaskToken(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"my-secret-token", "****oken"},
		{"abcd", "****"},
		{"abc", "****"},
		{"", "****"},
		{"a]b2", "****"},
		{"longer-service-token-value", "****alue"},
	}
	for _, tt := range tests {
		got := maskToken(tt.input)
		if got != tt.want {
			t.Errorf("maskToken(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNewFromEnv_Success(t *testing.T) {
	t.Setenv("CONFIG_SERVICE_HOST", "https://config.example.com")
	t.Setenv("CONFIG_SERVICE_TOKEN", "test-token")
	t.Setenv("CONFIG_SERVICE_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	c, err := NewFromEnv()
	if err != nil {
		t.Fatalf("NewFromEnv: %v", err)
	}
	if c.host != "https://config.example.com" {
		t.Errorf("host = %q", c.host)
	}
}

func TestNewFromEnv_MissingVars(t *testing.T) {
	// Ensure env vars are not set.
	os.Unsetenv("CONFIG_SERVICE_HOST")
	os.Unsetenv("CONFIG_SERVICE_TOKEN")
	os.Unsetenv("CONFIG_SERVICE_KEY")

	_, err := NewFromEnv()
	if err == nil {
		t.Fatal("expected error when env vars are missing")
	}
}
