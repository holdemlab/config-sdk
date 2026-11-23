package configsdk

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// GetOptions contains optional parameters for fetching a configuration.
type GetOptions struct {
	// Environment overrides Options.Environment for this call
	// (e.g. "production", "staging"). Empty uses the client environment.
	Environment string

	// Version requests a specific version. 0 means latest.
	// The encrypted endpoint (GetWithOptions) currently ignores it; use
	// GetFormattedWithOptions to fetch a specific version.
	Version int
}

// Get fetches a configuration by name, decrypts it, and unmarshals into dst.
// dst must be a pointer to a struct.
func (c *Client) Get(ctx context.Context, configName string, dst interface{}) error {
	return c.GetWithOptions(ctx, configName, dst, GetOptions{})
}

// GetWithOptions fetches a configuration with additional query parameters.
func (c *Client) GetWithOptions(ctx context.Context, configName string, dst interface{}, opts GetOptions) error {
	plaintext, err := c.getDecryptedBytes(ctx, configName, opts)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(plaintext, dst); err != nil {
		return fmt.Errorf("%w: %v", ErrUnmarshal, err)
	}

	return nil
}

// GetRaw fetches a configuration and returns it as map[string]interface{}.
func (c *Client) GetRaw(ctx context.Context, configName string) (map[string]interface{}, error) {
	plaintext, err := c.getDecryptedBytes(ctx, configName, GetOptions{})
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(plaintext, &result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnmarshal, err)
	}

	return result, nil
}

// GetBytes fetches a configuration and returns the decrypted JSON bytes.
func (c *Client) GetBytes(ctx context.Context, configName string) ([]byte, error) {
	return c.getDecryptedBytes(ctx, configName, GetOptions{})
}

// GetFormatted fetches a configuration in the specified format (json/yaml/env)
// without encryption, using the /formatted endpoint and the client environment.
func (c *Client) GetFormatted(ctx context.Context, configName string, format Format) ([]byte, error) {
	return c.GetFormattedWithOptions(ctx, configName, format, GetOptions{})
}

// GetFormattedWithOptions is like GetFormatted but also sends the per-call
// environment and version from opts.
func (c *Client) GetFormattedWithOptions(ctx context.Context, configName string, format Format, opts GetOptions) ([]byte, error) {
	path := "/api/v1/service/configs/" + url.PathEscape(configName) + "/formatted"

	query := c.query(opts)
	query.Set("format", string(format))
	path += "?" + query.Encode()

	body, _, err := c.doRequest(ctx, "GET", path)
	if err != nil {
		return nil, err
	}

	return body, nil
}

// getDecryptedBytes fetches the encrypted config from the server and decrypts it.
func (c *Client) getDecryptedBytes(ctx context.Context, configName string, opts GetOptions) ([]byte, error) {
	path := "/api/v1/service/configs/" + url.PathEscape(configName)

	if encoded := c.query(opts).Encode(); encoded != "" {
		path += "?" + encoded
	}

	body, _, err := c.doRequest(ctx, "GET", path)
	if err != nil {
		return nil, err
	}

	plaintext, err := decrypt(body, c.encryptionKey)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}

// query builds the env and version parameters; env is the per-call value or the client default.
func (c *Client) query(opts GetOptions) url.Values {
	query := url.Values{}
	if env := cmp.Or(opts.Environment, c.environment); env != "" {
		query.Set("env", env)
	}
	if opts.Version > 0 {
		query.Set("version", strconv.Itoa(opts.Version))
	}
	return query
}
