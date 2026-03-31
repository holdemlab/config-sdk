package configsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// GetOptions contains optional parameters for fetching a configuration.
type GetOptions struct {
	// Environment filters by environment (e.g. "production", "staging").
	Environment string

	// Version requests a specific version. 0 means latest.
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
// without encryption, using the /formatted endpoint.
// It supports ETag-based conditional requests via the returned ETag value.
func (c *Client) GetFormatted(ctx context.Context, configName string, format Format) ([]byte, error) {
	path := "/api/v1/service/configs/" + url.PathEscape(configName) + "/formatted"

	query := url.Values{}
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

	query := url.Values{}
	if opts.Environment != "" {
		query.Set("environment", opts.Environment)
	}
	if opts.Version > 0 {
		query.Set("version", strconv.Itoa(opts.Version))
	}
	if encoded := query.Encode(); encoded != "" {
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
