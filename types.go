package configsdk

import "time"

// ConfigInfo contains metadata about a configuration.
type ConfigInfo struct {
	// Name is the configuration name.
	Name string `json:"name"`
	// IsValid indicates whether the configuration is currently valid.
	IsValid bool `json:"is_valid"`
	// ValidFrom is the time from which the configuration is valid.
	ValidFrom time.Time `json:"valid_from"`
	// UpdatedAt is the last modification time.
	UpdatedAt time.Time `json:"updated_at"`
}

// Format represents the output format for GetFormatted.
type Format string

const (
	// FormatJSON requests JSON output.
	FormatJSON Format = "json"
	// FormatYAML requests YAML output.
	FormatYAML Format = "yaml"
	// FormatEnv requests ENV (dotenv) output.
	FormatEnv Format = "env"
)

// WatchCallback is called on each configuration change event.
type WatchCallback func(event ConfigChangeEvent)

// ConfigChangeEvent represents a configuration change event received via SSE.
type ConfigChangeEvent struct {
	// ConfigName is the name of the changed configuration.
	ConfigName string `json:"config_name"`
	// Version is the new configuration version number.
	Version int `json:"version"`
	// ChangedBy is the ID of the user who made the change.
	ChangedBy int `json:"changed_by"`
	// Timestamp is when the change occurred.
	Timestamp time.Time `json:"timestamp"`
}
