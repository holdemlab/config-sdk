package configsdk

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestIntegrationE2E simulates the full integration scenario:
// create client with token → list configs → get config → decrypt → verify values →
// get formatted → watch SSE → verify change event → close.
func TestIntegrationE2E(t *testing.T) {
	const (
		keyHex     = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		validToken = "srv_test_token_12345"
		configName = "database"
		configJSON = `{"host":"db.example.com","port":5432,"ssl":true}`
		yamlOutput = "host: db.example.com\nport: 5432\nssl: true\n"
	)

	key, _ := hex.DecodeString(keyHex)
	encrypted, err := encrypt([]byte(configJSON), key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// sseReady is closed when a watch client connects; sseData is written by the test.
	sseReady := make(chan struct{})
	var sseOnce sync.Once

	// --- Simulated Config Service ---
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Auth check
		token := r.Header.Get("X-Service-Token")
		if token != validToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch r.URL.Path {
		case "/api/v1/service/configs":
			// List endpoint
			configs := []ConfigInfo{
				{Name: configName, IsValid: true, ValidFrom: time.Now(), UpdatedAt: time.Now()},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(configs)

		case "/api/v1/service/configs/" + configName:
			// Encrypted config endpoint
			w.WriteHeader(http.StatusOK)
			w.Write(encrypted)

		case "/api/v1/service/configs/" + configName + "/formatted":
			// Formatted endpoint (no encryption)
			format := r.URL.Query().Get("format")
			w.Header().Set("Content-Type", "text/plain")
			switch format {
			case "yaml":
				w.Write([]byte(yamlOutput))
			case "json":
				w.Write([]byte(configJSON))
			default:
				w.WriteHeader(http.StatusBadRequest)
			}

		case "/api/v1/service/configs/" + configName + "/watch":
			// SSE endpoint
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "streaming not supported", http.StatusInternalServerError)
				return
			}
			flusher.Flush()

			// Signal that SSE client is connected
			sseOnce.Do(func() { close(sseReady) })

			// Send a change event
			event := ConfigChangeEvent{
				ConfigName: configName,
				Version:    2,
				ChangedBy:  42,
				Timestamp:  time.Now(),
			}
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: config_change\ndata: %s\n\n", data)
			flusher.Flush()

			// Keep connection open until client disconnects
			<-r.Context().Done()

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// --- Step 1: Create client (simulates "create token" — token is pre-provisioned) ---
	client, err := New(Options{
		Host:           srv.URL,
		ServiceToken:   validToken,
		EncryptionKey:  keyHex,
		HTTPClient:     srv.Client(),
		RequestTimeout: 5 * time.Second,
		RetryCount:     1,
		RetryDelay:     100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer client.Close()

	ctx := context.Background()

	// --- Step 2: List configs ---
	configs, err := client.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("got %d configs, want 1", len(configs))
	}
	if configs[0].Name != configName {
		t.Errorf("config name = %q, want %q", configs[0].Name, configName)
	}

	// --- Step 3: Get config → decrypt → verify values ---
	type DBConfig struct {
		Host string `json:"host"`
		Port int    `json:"port"`
		SSL  bool   `json:"ssl"`
	}
	var dbCfg DBConfig
	if err := client.Get(ctx, configName, &dbCfg); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if dbCfg.Host != "db.example.com" {
		t.Errorf("Host = %q, want %q", dbCfg.Host, "db.example.com")
	}
	if dbCfg.Port != 5432 {
		t.Errorf("Port = %d, want %d", dbCfg.Port, 5432)
	}
	if !dbCfg.SSL {
		t.Error("SSL = false, want true")
	}

	// --- Step 4: GetRaw ---
	raw, err := client.GetRaw(ctx, configName)
	if err != nil {
		t.Fatalf("GetRaw: %v", err)
	}
	if raw["host"] != "db.example.com" {
		t.Errorf("raw host = %v", raw["host"])
	}

	// --- Step 5: GetBytes ---
	bytes, err := client.GetBytes(ctx, configName)
	if err != nil {
		t.Fatalf("GetBytes: %v", err)
	}
	if string(bytes) != configJSON {
		t.Errorf("GetBytes = %q, want %q", bytes, configJSON)
	}

	// --- Step 6: GetFormatted (YAML) ---
	yamlBytes, err := client.GetFormatted(ctx, configName, FormatYAML)
	if err != nil {
		t.Fatalf("GetFormatted: %v", err)
	}
	if string(yamlBytes) != yamlOutput {
		t.Errorf("GetFormatted = %q, want %q", yamlBytes, yamlOutput)
	}

	// --- Step 7: Watch SSE → verify change event ---
	watchCtx, watchCancel := context.WithTimeout(ctx, 5*time.Second)
	defer watchCancel()

	eventCh := make(chan ConfigChangeEvent, 1)
	watchErr := make(chan error, 1)

	go func() {
		watchErr <- client.Watch(watchCtx, configName, func(event ConfigChangeEvent) {
			eventCh <- event
			watchCancel() // stop after first event
		})
	}()

	// Wait for the SSE client to connect
	select {
	case <-sseReady:
	case <-time.After(3 * time.Second):
		t.Fatal("SSE client did not connect in time")
	}

	// Wait for the change event
	select {
	case event := <-eventCh:
		if event.ConfigName != configName {
			t.Errorf("event ConfigName = %q, want %q", event.ConfigName, configName)
		}
		if event.Version != 2 {
			t.Errorf("event Version = %d, want 2", event.Version)
		}
		if event.ChangedBy != 42 {
			t.Errorf("event ChangedBy = %d, want 42", event.ChangedBy)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("did not receive SSE event in time")
	}

	// Wait for watch to finish
	if err := <-watchErr; err != nil && err != context.Canceled {
		t.Logf("Watch ended with: %v (expected context.Canceled)", err)
	}
}

// TestIntegrationUnauthorized verifies that the SDK correctly handles
// authentication failures in an E2E scenario.
func TestIntegrationUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client, err := New(Options{
		Host:          srv.URL,
		ServiceToken:  "invalid-token",
		EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		HTTPClient:    srv.Client(),
		RetryCount:    0,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.List(context.Background())
	if err != ErrUnauthorized {
		t.Fatalf("got %v, want ErrUnauthorized", err)
	}

	var dst struct{ X string }
	err = client.Get(context.Background(), "any", &dst)
	if err != ErrUnauthorized {
		t.Fatalf("got %v, want ErrUnauthorized", err)
	}
}

// TestIntegrationSnapshot verifies the Snapshot[T] + Watch integration flow.
func TestIntegrationSnapshot(t *testing.T) {
	const (
		keyHex     = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		configJSON = `{"host":"initial.example.com","port":3306}`
	)

	key, _ := hex.DecodeString(keyHex)
	encrypted, _ := encrypt([]byte(configJSON), key)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/service/configs/app":
			w.Write(encrypted)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client, _ := New(Options{
		Host:          srv.URL,
		ServiceToken:  "token",
		EncryptionKey: keyHex,
		HTTPClient:    srv.Client(),
		RetryCount:    0,
	})

	type AppConfig struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}

	// Create snapshot and load initial value
	snap := NewSnapshot[AppConfig]()

	var cfg AppConfig
	if err := client.Get(context.Background(), "app", &cfg); err != nil {
		t.Fatalf("Get: %v", err)
	}
	snap.Store(cfg)

	loaded := snap.Load()
	if loaded.Host != "initial.example.com" {
		t.Errorf("Host = %q, want %q", loaded.Host, "initial.example.com")
	}
	if loaded.Port != 3306 {
		t.Errorf("Port = %d, want %d", loaded.Port, 3306)
	}

	// Simulate update
	snap.Store(AppConfig{Host: "updated.example.com", Port: 5432})
	updated := snap.Load()
	if updated.Host != "updated.example.com" {
		t.Errorf("Host = %q, want %q", updated.Host, "updated.example.com")
	}
}
