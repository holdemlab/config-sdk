package configsdk

import "errors"

// Sentinel errors returned by the client.
var (
	// ErrUnauthorized indicates an invalid or expired service token (HTTP 401).
	ErrUnauthorized = errors.New("configsdk: unauthorized (invalid or expired token)")

	// ErrForbidden indicates the token lacks required permissions (HTTP 403).
	ErrForbidden = errors.New("configsdk: forbidden (insufficient permissions)")

	// ErrNotFound indicates the configuration was not found (HTTP 404).
	ErrNotFound = errors.New("configsdk: config not found")

	// ErrDecryptionFailed indicates that decryption failed (check encryption key).
	ErrDecryptionFailed = errors.New("configsdk: decryption failed (check encryption key)")

	// ErrInvalidResponse indicates the server returned an unexpected response.
	ErrInvalidResponse = errors.New("configsdk: invalid server response")

	// ErrConnectionFailed indicates a failure to connect to Config Service (HTTP 5xx or network error).
	ErrConnectionFailed = errors.New("configsdk: connection failed")

	// ErrUnmarshal indicates a failure to deserialize JSON into the target struct.
	ErrUnmarshal = errors.New("configsdk: failed to unmarshal config into target struct")
)
