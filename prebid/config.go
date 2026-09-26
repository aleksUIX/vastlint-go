package prebid

import (
	"encoding/json"
	"fmt"
	"time"
)

const defaultFetchTimeout = 50 * time.Millisecond

type config struct {
	Enabled        bool `json:"enabled"`
	RejectRevenue  bool `json:"reject_revenue"`
	FetchTimeoutMs int  `json:"fetch_timeout_ms"`
}

func (c config) fetchTimeout() time.Duration {
	if c.FetchTimeoutMs <= 0 {
		return defaultFetchTimeout
	}
	return time.Duration(c.FetchTimeoutMs) * time.Millisecond
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
