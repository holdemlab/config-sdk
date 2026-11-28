package configsdk

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	t.Setenv("CONFIG_SERVICE_HOST", "")
	t.Setenv("CONFIG_SERVICE_TOKEN", "")
	t.Setenv("CONFIG_SERVICE_KEY", "")

	_, err := NewFromEnv()
	if err == nil {
		t.Fatal("expected error when env vars are missing")
	}
}

const testKeyHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestNew_HostValidation(t *testing.T) {
	const malformed = "configsdk: Host must be an absolute http(s) URL"
	tests := []struct {
		host      string
		allowHTTP bool
		kind      string // ok | insecure | malformed
	}{
		{"https://config.example.com", false, "ok"},
		{"http://localhost", false, "ok"},
		{"http://LOCALHOST:8080", false, "ok"},
		{"http://127.0.0.1:8080", false, "ok"},
		{"http://127.5.5.5", false, "ok"},
		{"http://[::1]:8080", false, "ok"},
		{"http://config.example.com", true, "ok"},
		{"http://config.example.com", false, "insecure"},
		{"http://10.0.0.1", false, "insecure"},
		{"http://localhost.evil.com", false, "insecure"},
		{"http://127.0.0.1.evil.com", false, "insecure"},
		{"http://user:pass@evil.com", false, "insecure"},
		{"config.example.com", false, "malformed"},
		{"localhost:8080", false, "malformed"},
		{"ftp://x.example.com", false, "malformed"},
		{"https://", false, "malformed"},
		{"://bad", false, "malformed"},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			_, err := New(Options{
				Host: tt.host, ServiceToken: "t", EncryptionKey: testKeyHex,
				AllowInsecureHTTP: tt.allowHTTP,
			})
			switch tt.kind {
			case "ok":
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			case "insecure":
				if !errors.Is(err, ErrInsecureHost) {
					t.Fatalf("got %v, want ErrInsecureHost", err)
				}
			case "malformed":
				if err == nil || err.Error() != malformed || errors.Is(err, ErrInsecureHost) {
					t.Fatalf("got %v, want plain %q", err, malformed)
				}
				if strings.Contains(err.Error(), tt.host) {
					t.Fatalf("error echoes host: %v", err)
				}
			}
		})
	}
}

func TestNewFromEnv_AllowInsecureHTTP(t *testing.T) {
	tests := []struct {
		val string
		ok  bool
	}{
		{"true", true}, {"1", true}, {"", false}, {"garbage", false},
	}
	for _, tt := range tests {
		t.Run("val="+tt.val, func(t *testing.T) {
			t.Setenv("CONFIG_SERVICE_HOST", "http://config.example.com")
			t.Setenv("CONFIG_SERVICE_TOKEN", "t")
			t.Setenv("CONFIG_SERVICE_KEY", testKeyHex)
			t.Setenv("CONFIG_SERVICE_ALLOW_INSECURE_HTTP", tt.val)
			_, err := NewFromEnv()
			if tt.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.ok && !errors.Is(err, ErrInsecureHost) {
				t.Fatalf("got %v, want ErrInsecureHost", err)
			}
		})
	}
}

func TestIsLoopbackHost(t *testing.T) {
	tests := map[string]bool{
		"localhost": true, "LocalHost": true, "127.0.0.1": true, "127.5.5.5": true, "::1": true,
		"foo.localhost": false, "127.0.0.1.evil.com": false, "10.0.0.1": false,
		"example.com": false, "": false, "0.0.0.0": false,
	}
	for host, want := range tests {
		if got := isLoopbackHost(host); got != want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestNew_DoesNotMutateHTTPClient(t *testing.T) {
	hc := &http.Client{Timeout: 7 * time.Second, Transport: &http.Transport{}}
	c, err := New(Options{Host: "https://example.com", ServiceToken: "t", EncryptionKey: testKeyHex, HTTPClient: hc})
	if err != nil {
		t.Fatal(err)
	}
	if hc.CheckRedirect != nil {
		t.Error("user's HTTPClient.CheckRedirect was mutated")
	}
	if c.httpClient == hc {
		t.Error("httpClient must be a copy")
	}
	if c.httpClient.Transport != hc.Transport || c.httpClient.Timeout != hc.Timeout {
		t.Error("Transport/Timeout not carried over")
	}
	if c.httpClient.CheckRedirect == nil {
		t.Error("copy must disable redirects")
	}

	if _, err := New(Options{Host: "https://example.com", ServiceToken: "t", EncryptionKey: testKeyHex}); err != nil {
		t.Fatal(err)
	}
	if http.DefaultClient.CheckRedirect != nil {
		t.Error("http.DefaultClient was mutated")
	}
}

func TestNew_WatchIdleTimeoutDefaults(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want func(time.Duration) bool
	}{
		{0, func(d time.Duration) bool { return d == 5*time.Minute }},
		{-1, func(d time.Duration) bool { return d <= 0 }},
		{50 * time.Millisecond, func(d time.Duration) bool { return d == 50*time.Millisecond }},
	}
	for _, tt := range tests {
		c, err := New(Options{Host: "https://example.com", ServiceToken: "t", EncryptionKey: testKeyHex, WatchIdleTimeout: tt.in})
		if err != nil {
			t.Fatal(err)
		}
		if !tt.want(c.watchIdleTimeout) {
			t.Errorf("WatchIdleTimeout %v -> %v", tt.in, c.watchIdleTimeout)
		}
	}
}

func TestNewFromEnv_Environment(t *testing.T) {
	for name, env := range map[string]string{"set": "qa", "unset": ""} {
		t.Run(name, func(t *testing.T) {
			srv, got := envServer(t, http.StatusOK, "")
			t.Setenv("CONFIG_SERVICE_HOST", srv.URL)
			t.Setenv("CONFIG_SERVICE_TOKEN", "test-token")
			t.Setenv("CONFIG_SERVICE_KEY", testKeyHex)
			t.Setenv("CONFIG_SERVICE_ENVIRONMENT", env)

			c, err := NewFromEnv()
			if err != nil {
				t.Fatalf("NewFromEnv: %v", err)
			}
			var v map[string]any
			if err := c.Get(context.Background(), "app", &v); err != nil {
				t.Fatalf("Get: %v", err)
			}
			want := ""
			if env != "" {
				want = "env=" + env
			}
			if q := lastQuery(t, got); q != want {
				t.Errorf("query = %q, want %q", q, want)
			}
		})
	}
}

// setSecretEnv sets all four secret-related vars (empty when unused) plus a valid host.
func setSecretEnv(t *testing.T, token, tokenFile, key, keyFile string) {
	t.Helper()
	t.Setenv("CONFIG_SERVICE_HOST", "https://config.example.com")
	t.Setenv("CONFIG_SERVICE_TOKEN", token)
	t.Setenv("CONFIG_SERVICE_TOKEN_FILE", tokenFile)
	t.Setenv("CONFIG_SERVICE_KEY", key)
	t.Setenv("CONFIG_SERVICE_KEY_FILE", keyFile)
}

func writeSecretFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnvOrFile(t *testing.T) {
	const name = "CONFIG_SERVICE_TOKEN"
	tests := []struct {
		name    string
		file    *string // file content; nil = no _FILE var
		env     string
		want    string
		wantErr string
	}{
		{name: "lf", file: ptr("v\n"), want: "v"},
		{name: "crlf", file: ptr("v\r\n"), want: "v"},
		{name: "many newlines", file: ptr("v\n\n\n"), want: "v"},
		{name: "no newline", file: ptr("v"), want: "v"},
		{name: "leading space kept", file: ptr(" v"), want: " v"},
		{name: "inner space kept", file: ptr("a b\n"), want: "a b"},
		{name: "trailing space kept", file: ptr("v \n"), want: "v "},
		{name: "space only kept", file: ptr(" \n"), want: " "},
		{name: "zero bytes", file: ptr(""), wantErr: "configsdk: " + name + "_FILE is empty"},
		{name: "lf only", file: ptr("\n"), wantErr: "configsdk: " + name + "_FILE is empty"},
		{name: "crlf only", file: ptr("\r\n"), wantErr: "configsdk: " + name + "_FILE is empty"},
		{name: "env untrimmed", env: "v\n", want: "v\n"},
		{name: "neither", want: ""},
		{name: "env empty file used", env: "", file: ptr("f\n"), want: "f"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(name, tt.env)
			path := ""
			if tt.file != nil {
				path = writeSecretFile(t, *tt.file)
			}
			t.Setenv(name+"_FILE", path)
			got, err := envOrFile(name)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func ptr(s string) *string { return &s }

func TestEnvOrFile_Errors(t *testing.T) {
	const name = "CONFIG_SERVICE_KEY"
	missing := filepath.Join(t.TempDir(), "nope")

	t.Run("missing file", func(t *testing.T) {
		t.Setenv(name, "")
		t.Setenv(name+"_FILE", missing)
		_, err := envOrFile(name)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("err = %v, want fs.ErrNotExist", err)
		}
		if !strings.HasPrefix(err.Error(), "configsdk: read "+name+"_FILE: ") {
			t.Errorf("err = %q", err)
		}
	})
	t.Run("directory", func(t *testing.T) {
		t.Setenv(name, "")
		t.Setenv(name+"_FILE", t.TempDir())
		_, err := envOrFile(name)
		if err == nil || !strings.HasPrefix(err.Error(), "configsdk: read "+name+"_FILE: ") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("both set, file not read, no leak", func(t *testing.T) {
		t.Setenv(name, "super-secret-value")
		t.Setenv(name+"_FILE", missing)
		_, err := envOrFile(name)
		want := "configsdk: " + name + " and " + name + "_FILE are mutually exclusive"
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %q", err, want)
		}
		if strings.Contains(err.Error(), "super-secret-value") {
			t.Error("error leaks env value")
		}
	})
}

func TestNewFromEnv_FileSecrets(t *testing.T) {
	wantKey, _ := hex.DecodeString(testKeyHex)

	t.Run("both from files", func(t *testing.T) {
		setSecretEnv(t, "", writeSecretFile(t, "file-token\n"), "", writeSecretFile(t, testKeyHex+"\n"))
		c, err := NewFromEnv()
		if err != nil {
			t.Fatalf("NewFromEnv: %v", err)
		}
		if c.serviceToken != "file-token" || !bytes.Equal(c.encryptionKey, wantKey) {
			t.Errorf("token=%q key mismatch", c.serviceToken)
		}
	})
	t.Run("token file, key env", func(t *testing.T) {
		setSecretEnv(t, "", writeSecretFile(t, "file-token"), testKeyHex, "")
		c, err := NewFromEnv()
		if err != nil || c.serviceToken != "file-token" {
			t.Fatalf("c=%v err=%v", c, err)
		}
	})
	t.Run("token env, key file", func(t *testing.T) {
		setSecretEnv(t, "env-token", "", "", writeSecretFile(t, testKeyHex))
		c, err := NewFromEnv()
		if err != nil || c.serviceToken != "env-token" || !bytes.Equal(c.encryptionKey, wantKey) {
			t.Fatalf("c=%v err=%v", c, err)
		}
	})
	t.Run("conflict for token and key", func(t *testing.T) {
		for _, v := range []string{"CONFIG_SERVICE_TOKEN", "CONFIG_SERVICE_KEY"} {
			setSecretEnv(t, "t", "", testKeyHex, "")
			t.Setenv(v+"_FILE", writeSecretFile(t, "x"))
			_, err := NewFromEnv()
			want := "configsdk: " + v + " and " + v + "_FILE are mutually exclusive"
			if err == nil || err.Error() != want {
				t.Errorf("%s: err = %v, want %q", v, err, want)
			}
		}
	})
	t.Run("token error wins over key error", func(t *testing.T) {
		setSecretEnv(t, "t", writeSecretFile(t, "x"), "", filepath.Join(t.TempDir(), "nope"))
		_, err := NewFromEnv()
		want := "configsdk: CONFIG_SERVICE_TOKEN and CONFIG_SERVICE_TOKEN_FILE are mutually exclusive"
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %q", err, want)
		}
	})
	t.Run("file read error precedes New validation", func(t *testing.T) {
		setSecretEnv(t, "", filepath.Join(t.TempDir(), "nope"), testKeyHex, "")
		t.Setenv("CONFIG_SERVICE_HOST", "")
		_, err := NewFromEnv()
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("err = %v, want fs.ErrNotExist", err)
		}
	})
	t.Run("key content validated by New", func(t *testing.T) {
		tests := []struct{ content, want string }{
			{strings.Repeat("a", 63), "configsdk: EncryptionKey must be 64 hex characters (32 bytes)"},
			{strings.Repeat("z", 64), "configsdk: encryption key is not valid hex"},
		}
		for _, tt := range tests {
			setSecretEnv(t, "tok", "", "", writeSecretFile(t, tt.content))
			_, err := NewFromEnv()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		}
	})
	t.Run("invalid key content not leaked", func(t *testing.T) {
		const marker = "LEAKMARKER"
		setSecretEnv(t, "tok", "", "", writeSecretFile(t, marker+strings.Repeat("z", 54)))
		_, err := NewFromEnv()
		if err == nil || strings.Contains(err.Error(), marker) {
			t.Fatalf("err = %v", err)
		}
	})
}
