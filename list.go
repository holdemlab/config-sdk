package configsdk

import (
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
	if err := json.Unmarshal(body, &configs); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}

	return configs, nil
}
