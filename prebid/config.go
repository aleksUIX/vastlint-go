package prebid

import (
	"encoding/json"
	"fmt"
)

type config struct {
	Enabled       bool `json:"enabled"`
	RejectRevenue bool `json:"reject_revenue"`
}

func parseConfig(raw json.RawMessage) (config, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return config{}, nil
	}
	var cfg config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return config{}, fmt.Errorf("failed to parse openadtech.vastlint config: %s", err)
	}
	return cfg, nil
}
