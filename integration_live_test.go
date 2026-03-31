//go:build integration

package configsdk

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveE2E runs integration tests against a real Config Service.
// Requires environment variables:
//   - CONFIG_SERVICE_HOST
//   - CONFIG_SERVICE_TOKEN
//   - CONFIG_SERVICE_KEY
//   - CONFIG_TEST_CONFIG_NAME (name of an existing config to fetch)
//
// Run: go test -tags=integration -run TestLiveE2E ./...
func TestLiveE2E(t *testing.T) {
	host := os.Getenv("CONFIG_SERVICE_HOST")
	token := os.Getenv("CONFIG_SERVICE_TOKEN")
	key := os.Getenv("CONFIG_SERVICE_KEY")
	configName := os.Getenv("CONFIG_TEST_CONFIG_NAME")

	if host == "" || token == "" || key == "" || configName == "" {
		t.Skip("skipping: CONFIG_SERVICE_HOST, CONFIG_SERVICE_TOKEN, CONFIG_SERVICE_KEY, CONFIG_TEST_CONFIG_NAME must be set")
	}

	client, err := New(Options{
		Host:           host,
		ServiceToken:   token,
		EncryptionKey:  key,
		RequestTimeout: 10 * time.Second,
		RetryCount:     2,
		RetryDelay:     time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Step 1: List configs
	configs, err := client.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	t.Logf("Found %d configs", len(configs))

	found := false
	for _, c := range configs {
		if c.Name == configName {
			found = true
			t.Logf("Config %q: IsValid=%v, UpdatedAt=%v", c.Name, c.IsValid, c.UpdatedAt)
		}
	}
	if !found {
		t.Fatalf("config %q not found in list", configName)
	}

	// Step 2: Get config → decrypt → verify non-empty
	raw, err := client.GetRaw(ctx, configName)
	if err != nil {
		t.Fatalf("GetRaw: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("GetRaw returned empty config")
	}
	t.Logf("GetRaw: %d keys", len(raw))

	// Step 3: GetBytes
	bytes, err := client.GetBytes(ctx, configName)
	if err != nil {
		t.Fatalf("GetBytes: %v", err)
	}
	if len(bytes) == 0 {
		t.Fatal("GetBytes returned empty")
	}

	// Step 4: Get into struct (map)
	var m map[string]interface{}
	if err := client.Get(ctx, configName, &m); err != nil {
		t.Fatalf("Get: %v", err)
	}
	t.Logf("Get returned %d fields", len(m))

	// Step 5: GetFormatted (JSON)
	jsonBytes, err := client.GetFormatted(ctx, configName, FormatJSON)
	if err != nil {
		t.Fatalf("GetFormatted JSON: %v", err)
	}
	if len(jsonBytes) == 0 {
		t.Fatal("GetFormatted returned empty")
	}
	t.Logf("GetFormatted JSON: %d bytes", len(jsonBytes))
}
