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

Reads `CONFIG_SERVICE_HOST`, `CONFIG_SERVICE_TOKEN`, `CONFIG_SERVICE_KEY`, and optionally `CONFIG_SERVICE_ALLOW_INSECURE_HTTP` (`true`/`1` permits a plain-http, non-loopback host) and `CONFIG_SERVICE_ENVIRONMENT`.

## Environments

Set `Options.Environment` (or `CONFIG_SERVICE_ENVIRONMENT`) to select the config environment for every call; it is sent as the `env` query parameter. Empty means the service default (`default`). `GetOptions.Environment` overrides it per call. A name that exists only in another environment returns `ErrNotFound`. `Watch` delivers only events of the client environment; use one client per environment.

## Watch for Changes

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

snap := configsdk.NewSnapshot[Config]()
go func() {
    if err := configsdk.WatchSnapshot(ctx, client, "my-service", snap); err != nil {
        log.Printf("watch stopped: %v", err)
    }
}()

current := snap.Load() // safe from any goroutine
```

`WatchSnapshot` subscribes to SSE events, re-fetches the config on each event and stores it in the `Snapshot` (no initial fetch). `WatchAndDecode` is deprecated and not concurrency-safe: reading `dst` while it runs is a data race.

Only events of the client environment are delivered; the initial `connected` event is ignored. `Watch` returns the error on 401/403/404 (call it again to keep waiting); other failures reconnect with exponential backoff capped at 30s. If no bytes (including `: ping` comments) arrive for `WatchIdleTimeout` (default 5m, negative disables) the stream is reconnected immediately.

For manual event handling:

```go
client.Watch(ctx, "my-service", func(event configsdk.ConfigChangeEvent) {
    fmt.Printf("Config %s %s in %s\n", event.ConfigName, event.Type, event.Environment)
})
```

## Restart on Change

`RestartOnChange` unifies the "restart the service when its config changes" pattern: it watches the config, and when the content really differs from the value the service started with (and the new config decrypts and decodes), it calls `restart` exactly once and returns `nil`.

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

var cfg Config
if err := client.Get(ctx, "billing", &cfg); err != nil {
    log.Fatal(err)
}

go func() {
    if err := configsdk.RestartOnChange(ctx, client, "billing", cfg, stop); err != nil && !errors.Is(err, context.Canceled) {
        log.Printf("config watch stopped: %v", err)
    }
}()

<-ctx.Done() // then shut down gracefully and exit; the orchestrator starts the process again
```

- The SDK only decides when to restart; `restart` (here `stop`) triggers the service's normal graceful shutdown.
- The process exits with code 0, so the orchestrator's restart policy must restart on a clean exit: Docker `restart: unless-stopped` or `always` (not `on-failure`), Kubernetes Deployments (default `restartPolicy: Always`). Otherwise exit with a non-zero code after shutdown.
- A check (fetch and compare) runs every time the watch stream connects or reconnects, including the first connection, and on every change event except `deleted`. Reconnect checks are rate-limited to one per 30s after a successful check: a reconnect inside that window postpones its check until the window ends, so a change made during an SSE gap is always noticed (at most ~30s later).
- Checks are debounced (2s plus up to 2s of random jitter, so replicas do not restart at the same instant); a burst of events causes one fetch.
- `current` must be decoded by `Get` into a zero value of `T` (`var cfg T; client.Get(ctx, name, &cfg)`) with the client's environment (`Options.Environment` / `CONFIG_SERVICE_ENVIRONMENT`), not by `GetWithOptions` with a per-call `Environment`: `RestartOnChange` always checks the client environment. Do not pre-fill the value before `Get`. A copy you modify must not share maps, slices or pointers with `current`: decode a second value with another `Get` (or deep-copy); a plain assignment shares them. If `current` was modified, the first check after the watch connects restarts the service, on every start. Values are compared with `reflect.DeepEqual`: unexported fields count, and a config that holds a NaN float or a non-nil func field never compares equal, so it has the same effect.
- A failed check never restarts the service; every failed attempt is reported via `Options.OnError`. Every error except `ErrNotFound`, `ErrUnauthorized`, `ErrForbidden`, `ErrDecryptionFailed`, `ErrUnmarshal` and `ErrResponseTooLarge` is retried automatically with exponential backoff (from `RetryDelay`, capped at 30s) until the fetch succeeds or a newer event or reconnect starts a fresh check; consecutive failures keep increasing the backoff, even across new events, until a fetch succeeds. The listed errors are not retried: the service keeps running and the next change event or reconnect triggers a new check.
- Returns `ctx.Err()` when `ctx` is cancelled, `context.Canceled` after `Client.Close()`, and the sentinel error (`ErrUnauthorized`/`ErrForbidden`/`ErrNotFound`) on 401/403/404, without calling `restart`.

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

// with environment and version
data, err = client.GetFormattedWithOptions(ctx, "my-service", configsdk.FormatJSON,
    configsdk.GetOptions{Environment: "prod", Version: 4})
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
| `GetWithOptions(ctx, name, dst, opts)` | Fetch with environment override; `Version` is ignored by the encrypted endpoint |
| `GetRaw(ctx, name)` | Returns `map[string]interface{}` |
| `GetBytes(ctx, name)` | Returns decrypted JSON bytes |
| `GetFormatted(ctx, name, format)` | Returns plaintext in json/yaml/env format |
| `GetFormattedWithOptions(ctx, name, format, opts)` | Like `GetFormatted` with per-call environment and version |
| `List(ctx)` | List available configurations (one entry per config across environments; names may repeat) |

### Watch

| Method | Description |
|--------|-------------|
| `Watch(ctx, name, callback)` | Subscribe to SSE change events |
| `WatchSnapshot(ctx, client, name, snap)` | Auto-update a `Snapshot[T]` on changes |
| `RestartOnChange(ctx, client, name, current, restart)` | Call `restart` once when the config content really changes |
| `WatchAndDecode(ctx, name, dst)` | Deprecated: auto-update struct (not concurrency-safe) |
| `Close()` | Close client and all SSE connections |

### Types

| Type | Description |
|------|-------------|
| `Options` | Client configuration (Host, ServiceToken, EncryptionKey, ...) |
| `GetOptions` | Query filters (Environment, Version) |
| `ConfigInfo` | Config metadata (Name, IsValid, ValidFrom, UpdatedAt) |
| `ConfigChangeEvent` | SSE event (ConfigName, Type, Environment, Version, ChangedBy, Timestamp; Version is the config version, 0 from config-service before v0.11.0) |
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
| `ErrInsecureHost` | Non-https `Host` that is not loopback and `AllowInsecureHTTP` is false |
| `ErrResponseTooLarge` | Response body over 10 MiB or SSE event over 1 MiB |

## Configuration Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Host` | `string` | *required* | Config Service base URL |
| `ServiceToken` | `string` | *required* | Service authentication token |
| `EncryptionKey` | `string` | *required* | 64 hex chars (AES-256 key) |
| `HTTPClient` | `*http.Client` | `http.DefaultClient` | Custom HTTP client (copied, never mutated; redirects are never followed) |
| `RequestTimeout` | `time.Duration` | `10s` | Per-request timeout |
| `RetryCount` | `int` | `3` | Retry attempts for 5xx/network errors |
| `RetryDelay` | `time.Duration` | `1s` | Base retry delay (exponential backoff) |
| `Logger` | `*slog.Logger` | `nil` (disabled) | Structured logger |
| `OnError` | `func(error)` | `nil` | Error callback for watch mode |
| `OnChange` | `func(string)` | `nil` | Change callback for watch mode |
| `AllowInsecureHTTP` | `bool` | `false` | Allow plain-http non-loopback `Host` |
| `WatchIdleTimeout` | `time.Duration` | `5m` | Reconnect watch after this idle time; negative disables |
| `Environment` | `string` | `""` (service default) | Config environment sent as `env` |

## Security Notes

- `https` is enforced; plain `http` is allowed only for loopback hosts or with `AllowInsecureHTTP`.
- Redirects are never followed, so the service token cannot be forwarded to another host.
- Size limits: 10 MiB per response, 1 MiB per SSE event.
- Encryption uses no AAD: ciphertexts are not bound to config name or version, so a party that can tamper with responses can substitute another ciphertext encrypted under the same key (fixing this needs a server change).
- Events can be missed across watch reconnects (no replay).

## Upgrading

- Non-https non-loopback `Host` is now rejected (`ErrInsecureHost`); set `AllowInsecureHTTP` to opt out.
- Redirects are no longer followed; a 3xx yields `ErrInvalidResponse`. Your `HTTPClient` is copied and its `CheckRedirect` overridden.
- `Watch` now returns on 401/403/404 instead of retrying forever.
- Watch reconnects use exponential backoff (cap 30s) and an idle timeout (default 5m).
- Minimum Go version is 1.26.
- The environment is now sent as `env` (previously `environment`, which the service ignored).
- `Watch` filters events by environment and ignores `connected`; `List` reads the `{"data": [...]}` envelope (bare arrays still accepted).

## Examples

See the [examples/](examples/) directory:

- [basic](examples/basic/) — fetch and print configuration
- [watch](examples/watch/) — subscribe to real-time changes
- [formatted](examples/formatted/) — export as JSON/YAML/ENV

## Requirements

- Go 1.26+
- No external dependencies

## License

MIT — see [LICENSE](LICENSE).
