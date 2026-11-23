package configsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// List returns the list of configurations available for this service token.
// It calls GET /api/v1/service/configs.
func (c *Client) List(ctx context.Context) ([]ConfigInfo, error) {
	body, _, err := c.doRequest(ctx, "GET", "/api/v1/service/configs")
	if err != nil {
		return nil, err
	}

	var configs []ConfigInfo
	if trimmed := bytes.TrimSpace(body); len(trimmed) > 0 && trimmed[0] == '[' {
		// Bare array, tolerated alongside the {"data": [...]} envelope.
		if err := json.Unmarshal(trimmed, &configs); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
		}
		return configs, nil
	}

	var envelope struct {
		Data []ConfigInfo `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	if envelope.Data == nil {
		return nil, fmt.Errorf("%w: missing data", ErrInvalidResponse)
	}

	return envelope.Data, nil
}
