package prebid

import (
	"encoding/json"

	vastlint "github.com/aleksUIX/vastlint-go"
	"github.com/prebid/prebid-server/v4/modules/moduledeps"
)

// Module validates VAST video adm in-process and counts revenue-impact findings.
type Module struct {
	cfg      config
	validate func(xml string) ([]finding, error)
	record   recorder
}

// Builder is the Prebid Server module entry point.
// The module stays off unless the host sets hooks.modules.openadtech.vastlint.enabled.
func Builder(raw json.RawMessage, _ moduledeps.ModuleDeps) (interface{}, error) {
	cfg, err := parseConfig(raw)
	if err != nil {
		return nil, err
	}
	return Module{cfg: cfg, validate: validateXML}, nil
}

func validateXML(xml string) ([]finding, error) {
	result, err := vastlint.Validate(xml)
	if err != nil {
		return nil, err
	}
	out := make([]finding, 0, len(result.Issues))
	for _, issue := range result.Issues {
		out = append(out, finding{ID: issue.ID})
	}
	return out, nil
}
