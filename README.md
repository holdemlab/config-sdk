# config-sdk

Go SDK for Config Service — fetch, decrypt, and watch service configurations.

**Zero dependencies** — only Go standard library.

## Install

```bash
go get github.com/holdemlab/config-sdk
```

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"

    configsdk "github.com/holdemlab/config-sdk"
)

type MyConfig struct {
    Database struct {
        Host string `json:"host"`
        Port int    `json:"port"`
    } `json:"database"`
}

func main() {
    client, err := configsdk.New(configsdk.Options{
        Host:          os.Getenv("CONFIG_SERVICE_HOST"),
        ServiceToken:  os.Getenv("CONFIG_SERVICE_TOKEN"),
        EncryptionKey: os.Getenv("CONFIG_SERVICE_KEY"),
    })
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    var cfg MyConfig
    if err := client.Get(context.Background(), "my-service", &cfg); err != nil {
        log.Fatal(err)
    }
    fmt.Printf("DB: %s:%d\n", cfg.Database.Host, cfg.Database.Port)
}
```

Or initialize from environment variables:

```go
client, err := configsdk.NewFromEnv()
```

Reads `CONFIG_SERVICE_HOST`, `CONFIG_SERVICE_TOKEN`, `CONFIG_SERVICE_KEY`.

## Watch for Changes

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

go func() {
    if err := client.WatchAndDecode(ctx, "my-service", &cfg); err != nil {
        log.Printf("watch stopped: %v", err)
    }
}()
```

`WatchAndDecode` subscribes to SSE events and automatically re-fetches and updates the struct. Thread-safe via `sync.RWMutex`.

For manual event handling:

```go
client.Watch(ctx, "my-service", func(event configsdk.ConfigChangeEvent) {
    fmt.Printf("Config %s updated to version %d\n", event.ConfigName, event.Version)
})
```

## Thread-Safe Snapshot

```go
snap := configsdk.NewSnapshot[MyConfig]()
snap.Store(cfg)

// Safe to read from any goroutine
current := snap.Load()
```

## Formatted Output

Fetch config as JSON, YAML, or ENV (plaintext, no encryption):

```go
data, err := client.GetFormatted(ctx, "my-service", configsdk.FormatYAML)
```

## API Reference

### Client Creation

| Function | Description |
|----------|-------------|
| `New(opts Options) (*Client, error)` | Create client with explicit options |
| `NewFromEnv() (*Client, error)` | Create client from environment variables |

### Configuration Retrieval

| Method | Description |
|--------|-------------|
| `Get(ctx, name, dst)` | Fetch, decrypt, unmarshal into struct |
| `GetWithOptions(ctx, name, dst, opts)` | Fetch with environment/version filters |
| `GetRaw(ctx, name)` | Returns `map[string]interface{}` |
| `GetBytes(ctx, name)` | Returns decrypted JSON bytes |
| `GetFormatted(ctx, name, format)` | Returns plaintext in json/yaml/env format |
| `List(ctx)` | List available configurations |

### Watch

| Method | Description |
|--------|-------------|
| `Watch(ctx, name, callback)` | Subscribe to SSE change events |
| `WatchAndDecode(ctx, name, dst)` | Auto-update struct on changes |
| `Close()` | Close client and all SSE connections |

### Types

| Type | Description |
|------|-------------|
| `Options` | Client configuration (Host, ServiceToken, EncryptionKey, ...) |
| `GetOptions` | Query filters (Environment, Version) |
| `ConfigInfo` | Config metadata (Name, IsValid, ValidFrom, UpdatedAt) |
| `ConfigChangeEvent` | SSE event (ConfigName, Version, ChangedBy, Timestamp) |
| `WatchCallback` | `func(ConfigChangeEvent)` |
| `Format` | Output format: `FormatJSON`, `FormatYAML`, `FormatEnv` |
| `Snapshot[T]` | Generic thread-safe config wrapper |

### Errors

| Error | Trigger |
|-------|---------|
| `ErrUnauthorized` | HTTP 401 |
| `ErrForbidden` | HTTP 403 |
| `ErrNotFound` | HTTP 404 |
| `ErrDecryptionFailed` | AES-GCM decryption failure |
| `ErrInvalidResponse` | Unexpected server response |
| `ErrConnectionFailed` | Network/5xx errors (after retries) |
| `ErrUnmarshal` | JSON deserialization failure |

## Configuration Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Host` | `string` | *required* | Config Service base URL |
| `ServiceToken` | `string` | *required* | Service authentication token |
| `EncryptionKey` | `string` | *required* | 64 hex chars (AES-256 key) |
| `HTTPClient` | `*http.Client` | `http.DefaultClient` | Custom HTTP client |
| `RequestTimeout` | `time.Duration` | `10s` | Per-request timeout |
| `RetryCount` | `int` | `3` | Retry attempts for 5xx/network errors |
| `RetryDelay` | `time.Duration` | `1s` | Base retry delay (exponential backoff) |
| `Logger` | `*slog.Logger` | `nil` (disabled) | Structured logger |
| `OnError` | `func(error)` | `nil` | Error callback for watch mode |
| `OnChange` | `func(string)` | `nil` | Change callback for watch mode |

## Examples

See the [examples/](examples/) directory:

- [basic](examples/basic/) — fetch and print configuration
- [watch](examples/watch/) — subscribe to real-time changes
- [formatted](examples/formatted/) — export as JSON/YAML/ENV

## Requirements

- Go 1.21+
- No external dependencies

## License

MIT — see [LICENSE](LICENSE).
