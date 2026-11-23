package configsdk

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestClientAndServer creates a test HTTP server that returns encrypted JSON
// and a client configured to talk to it.
func newTestClientAndServer(t *testing.T, configJSON string) (*Client, *httptest.Server) {
	t.Helper()

	keyHex := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	key, _ := hex.DecodeString(keyHex)

	encrypted, err := encrypt([]byte(configJSON), key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(encrypted)
	}))

	client, err := New(Options{
		Host:           srv.URL,
		ServiceToken:   "test-token",
		EncryptionKey:  keyHex,
		HTTPClient:     srv.Client(),
		RequestTimeout: 5 * time.Second,
		RetryCount:     1,
		RetryDelay:     time.Millisecond,
	})
	if err != nil {
		srv.Close()
		t.Fatalf("New: %v", err)
	}

	return client, srv
}

func TestGet_SimpleStruct(t *testing.T) {
	type Config struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}

	client, srv := newTestClientAndServer(t, `{"host":"localhost","port":5432}`)
	defer srv.Close()

	var cfg Config
	if err := client.Get(context.Background(), "db", &cfg); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if cfg.Host != "localhost" {
		t.Errorf("Host = %q, want %q", cfg.Host, "localhost")
	}
	if cfg.Port != 5432 {
		t.Errorf("Port = %d, want %d", cfg.Port, 5432)
	}
}

func TestGet_NestedStruct(t *testing.T) {
	type DB struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	type Config struct {
		Database DB     `json:"database"`
		LogLevel string `json:"log_level"`
	}

	configJSON := `{"database":{"host":"db.example.com","port":3306},"log_level":"info"}`
	client, srv := newTestClientAndServer(t, configJSON)
	defer srv.Close()

	var cfg Config
	if err := client.Get(context.Background(), "app", &cfg); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if cfg.Database.Host != "db.example.com" {
		t.Errorf("Database.Host = %q, want %q", cfg.Database.Host, "db.example.com")
	}
	if cfg.Database.Port != 3306 {
		t.Errorf("Database.Port = %d, want %d", cfg.Database.Port, 3306)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
}

func TestGet_UnknownFieldsIgnored(t *testing.T) {
	type Config struct {
		Host string `json:"host"`
	}

	client, srv := newTestClientAndServer(t, `{"host":"localhost","unknown_field":"value","extra":123}`)
	defer srv.Close()

	var cfg Config
	if err := client.Get(context.Background(), "svc", &cfg); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if cfg.Host != "localhost" {
		t.Errorf("Host = %q, want %q", cfg.Host, "localhost")
	}
}

func TestGet_EmptyConfig(t *testing.T) {
	type Config struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}

	client, srv := newTestClientAndServer(t, `{}`)
	defer srv.Close()

	var cfg Config
	if err := client.Get(context.Background(), "empty", &cfg); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if cfg.Host != "" {
		t.Errorf("Host = %q, want empty", cfg.Host)
	}
	if cfg.Port != 0 {
		t.Errorf("Port = %d, want 0", cfg.Port)
	}
}

func TestGet_InvalidJSON(t *testing.T) {
	client, srv := newTestClientAndServer(t, `not json at all`)
	defer srv.Close()

	var cfg struct{ X string }
	err := client.Get(context.Background(), "bad", &cfg)
	if !errors.Is(err, ErrUnmarshal) {
		t.Fatalf("got %v, want ErrUnmarshal", err)
	}
}

func TestGetRaw(t *testing.T) {
	client, srv := newTestClientAndServer(t, `{"key":"value","num":42}`)
	defer srv.Close()

	raw, err := client.GetRaw(context.Background(), "test")
	if err != nil {
		t.Fatalf("GetRaw: %v", err)
	}

	if raw["key"] != "value" {
		t.Errorf("key = %v, want %q", raw["key"], "value")
	}
	if raw["num"] != json.Number("42") && raw["num"] != float64(42) {
		t.Errorf("num = %v (%T)", raw["num"], raw["num"])
	}
}

func TestGetBytes(t *testing.T) {
	expected := `{"host":"localhost"}`
	client, srv := newTestClientAndServer(t, expected)
	defer srv.Close()

	got, err := client.GetBytes(context.Background(), "test")
	if err != nil {
		t.Fatalf("GetBytes: %v", err)
	}

	if string(got) != expected {
		t.Fatalf("got %q, want %q", got, expected)
	}
}

func TestGetWithOptions_QueryParams(t *testing.T) {
	keyHex := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	key, _ := hex.DecodeString(keyHex)

	encrypted, _ := encrypt([]byte(`{"ok":true}`), key)

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		w.WriteHeader(http.StatusOK)
		w.Write(encrypted)
	}))
	defer srv.Close()

	client, _ := New(Options{
		Host:          srv.URL,
		ServiceToken:  "token",
		EncryptionKey: keyHex,
		HTTPClient:    srv.Client(),
	})

	var dst map[string]interface{}
	err := client.GetWithOptions(context.Background(), "my-config", &dst, GetOptions{
		Environment: "production",
		Version:     3,
	})
	if err != nil {
		t.Fatalf("GetWithOptions: %v", err)
	}

	if !strings.Contains(gotPath, "env=production") {
		t.Errorf("path %q missing env param", gotPath)
	}
	if strings.Contains(gotPath, "environment=") {
		t.Errorf("path %q must not contain environment param", gotPath)
	}
	if !strings.Contains(gotPath, "version=3") {
		t.Errorf("path %q missing version param", gotPath)
	}
}

func TestGet_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client, _ := New(Options{
		Host:          srv.URL,
		ServiceToken:  "token",
		EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		HTTPClient:    srv.Client(),
	})

	var cfg struct{}
	err := client.Get(context.Background(), "missing", &cfg)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestGet_DecryptionFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("this is not encrypted data but long enough for nonce"))
	}))
	defer srv.Close()

	client, _ := New(Options{
		Host:          srv.URL,
		ServiceToken:  "token",
		EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		HTTPClient:    srv.Client(),
	})

	var cfg struct{}
	err := client.Get(context.Background(), "bad-crypto", &cfg)
	if !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("got %v, want ErrDecryptionFailed", err)
	}
}

func TestGetFormatted_JSON(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"host":"localhost","port":5432}`))
	}))
	defer srv.Close()

	client, _ := New(Options{
		Host:          srv.URL,
		ServiceToken:  "token",
		EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		HTTPClient:    srv.Client(),
	})

	body, err := client.GetFormatted(context.Background(), "db", FormatJSON)
	if err != nil {
		t.Fatalf("GetFormatted: %v", err)
	}

	if !strings.Contains(gotPath, "/formatted") {
		t.Errorf("path %q missing /formatted", gotPath)
	}
	if !strings.Contains(gotPath, "format=json") {
		t.Errorf("path %q missing format=json", gotPath)
	}
	if !strings.Contains(string(body), `"host"`) {
		t.Errorf("body = %q, expected JSON with host", body)
	}
}

func TestGetFormatted_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client, _ := New(Options{
		Host:          srv.URL,
		ServiceToken:  "token",
		EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		HTTPClient:    srv.Client(),
		RetryCount:    1,
		RetryDelay:    time.Millisecond,
	})

	_, err := client.GetFormatted(context.Background(), "missing", FormatYAML)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestGetRaw_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	client, _ := New(Options{
		Host:          srv.URL,
		ServiceToken:  "token",
		EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		HTTPClient:    srv.Client(),
		RetryCount:    1,
		RetryDelay:    time.Millisecond,
	})

	_, err := client.GetRaw(context.Background(), "cfg")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("got %v, want ErrForbidden", err)
	}
}

// --- environments ---

// envServer records the raw query of every request. Paths ending in /formatted
// answer with formattedBody; everything else answers with encrypted data.
func envServer(t *testing.T, status int, formattedBody string) (server *httptest.Server, recorded func() []string) {
	t.Helper()
	key, _ := hex.DecodeString(testKeyHex)
	enc, _ := encrypt([]byte(`{"ok":true}`), key)
	var mu sync.Mutex
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.RawQuery)
		mu.Unlock()
		w.WriteHeader(status)
		if strings.HasSuffix(r.URL.Path, "/formatted") {
			_, _ = w.Write([]byte(formattedBody))
			return
		}
		_, _ = w.Write(enc)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), queries...)
	}
}

// lastQuery asserts exactly one request was made and returns its raw query.
func lastQuery(t *testing.T, got func() []string) string {
	t.Helper()
	q := got()
	if len(q) != 1 {
		t.Fatalf("requests = %d, want 1", len(q))
	}
	return q[0]
}

func TestGet_ClientEnvironmentSent(t *testing.T) {
	calls := map[string]func(c *Client) error{
		"Get": func(c *Client) error {
			var v map[string]any
			return c.Get(context.Background(), "app", &v)
		},
		"GetRaw": func(c *Client) error {
			_, err := c.GetRaw(context.Background(), "app")
			return err
		},
		"GetBytes": func(c *Client) error {
			_, err := c.GetBytes(context.Background(), "app")
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			srv, got := envServer(t, http.StatusOK, "")
			c := newWatchTestClient(t, srv, func(o *Options) { o.Environment = "staging" })
			if err := call(c); err != nil {
				t.Fatal(err)
			}
			if q := lastQuery(t, got); q != "env=staging" {
				t.Errorf("query = %q, want env=staging", q)
			}
		})
	}
}

func TestGet_NoEnvironmentEmptyQuery(t *testing.T) {
	srv, got := envServer(t, http.StatusOK, "")
	c := newWatchTestClient(t, srv, nil)
	var v map[string]any
	if err := c.Get(context.Background(), "app", &v); err != nil {
		t.Fatal(err)
	}
	if q := lastQuery(t, got); q != "" {
		t.Errorf("query = %q, want empty", q)
	}
}

func TestGetWithOptions_Environment(t *testing.T) {
	tests := []struct {
		name      string
		clientEnv string
		opts      GetOptions
		want      url.Values
	}{
		{"per-call overrides client", "staging", GetOptions{Environment: "prod"}, url.Values{"env": {"prod"}}},
		{"client env used when per-call empty", "staging", GetOptions{}, url.Values{"env": {"staging"}}},
		{"literal default is sent", "default", GetOptions{}, url.Values{"env": {"default"}}},
		{"version zero not sent", "", GetOptions{Environment: "prod"}, url.Values{"env": {"prod"}}},
		{"version sent when positive", "", GetOptions{Version: 3}, url.Values{"version": {"3"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, got := envServer(t, http.StatusOK, "")
			c := newWatchTestClient(t, srv, func(o *Options) { o.Environment = tt.clientEnv })
			var v map[string]any
			if err := c.GetWithOptions(context.Background(), "app", &v, tt.opts); err != nil {
				t.Fatal(err)
			}
			q, err := url.ParseQuery(lastQuery(t, got))
			if err != nil {
				t.Fatal(err)
			}
			if q.Encode() != tt.want.Encode() {
				t.Errorf("query = %v, want %v", q, tt.want)
			}
		})
	}
}

func TestGetWithOptions_EnvironmentEscaped(t *testing.T) {
	srv, got := envServer(t, http.StatusOK, "")
	c := newWatchTestClient(t, srv, nil)
	var v map[string]any
	if err := c.GetWithOptions(context.Background(), "app", &v, GetOptions{Environment: "a&version=9"}); err != nil {
		t.Fatal(err)
	}
	q, _ := url.ParseQuery(lastQuery(t, got))
	if q.Get("env") != "a&version=9" {
		t.Errorf("env = %q, want %q", q.Get("env"), "a&version=9")
	}
	if q.Has("version") {
		t.Errorf("injected version parameter present: %v", q)
	}
}

func TestGet_WrongEnvironmentNotFound(t *testing.T) {
	srv, _ := envServer(t, http.StatusNotFound, "")
	c := newWatchTestClient(t, srv, func(o *Options) { o.Environment = "qa" })
	var v map[string]any
	if err := c.Get(context.Background(), "app", &v); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestGetFormatted_ClientEnvironment(t *testing.T) {
	srv, got := envServer(t, http.StatusOK, "a: 1\n")
	c := newWatchTestClient(t, srv, func(o *Options) { o.Environment = "staging" })
	if _, err := c.GetFormatted(context.Background(), "app", FormatYAML); err != nil {
		t.Fatal(err)
	}
	q, _ := url.ParseQuery(lastQuery(t, got))
	if q.Get("format") != "yaml" || q.Get("env") != "staging" || q.Has("version") {
		t.Errorf("query = %v, want format=yaml env=staging and no version", q)
	}
}

func TestGetFormattedWithOptions(t *testing.T) {
	t.Run("env and version", func(t *testing.T) {
		srv, got := envServer(t, http.StatusOK, `{"a":1}`)
		c := newWatchTestClient(t, srv, func(o *Options) { o.Environment = "staging" })
		body, err := c.GetFormattedWithOptions(context.Background(), "app", FormatJSON,
			GetOptions{Environment: "prod", Version: 4})
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `{"a":1}` {
			t.Errorf("body = %q", body)
		}
		q, _ := url.ParseQuery(lastQuery(t, got))
		want := url.Values{"format": {"json"}, "env": {"prod"}, "version": {"4"}}
		if q.Encode() != want.Encode() {
			t.Errorf("query = %v, want %v", q, want)
		}
	})
	t.Run("empty options no env", func(t *testing.T) {
		srv, got := envServer(t, http.StatusOK, "A=1\n")
		c := newWatchTestClient(t, srv, nil)
		if _, err := c.GetFormattedWithOptions(context.Background(), "app", FormatEnv, GetOptions{}); err != nil {
			t.Fatal(err)
		}
		if q := lastQuery(t, got); q != "format=env" {
			t.Errorf("query = %q, want format=env", q)
		}
	})
	t.Run("env escaped", func(t *testing.T) {
		srv, got := envServer(t, http.StatusOK, "x")
		c := newWatchTestClient(t, srv, nil)
		_, err := c.GetFormattedWithOptions(context.Background(), "app", FormatJSON,
			GetOptions{Environment: "a&version=9"})
		if err != nil {
			t.Fatal(err)
		}
		q, _ := url.ParseQuery(lastQuery(t, got))
		if q.Get("env") != "a&version=9" || q.Has("version") {
			t.Errorf("query = %v", q)
		}
	})
	t.Run("not found", func(t *testing.T) {
		srv, _ := envServer(t, http.StatusNotFound, "")
		c := newWatchTestClient(t, srv, nil)
		_, err := c.GetFormattedWithOptions(context.Background(), "app", FormatJSON, GetOptions{Environment: "qa"})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("got %v, want ErrNotFound", err)
		}
	})
}
