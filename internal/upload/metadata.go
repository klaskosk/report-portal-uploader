package upload

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	rpusend "github.com/klaskosk/report-portal-uploader/internal/send"
)

// BuildMetadata converts upload config into the shared send.Metadata value.
func BuildMetadata(cfg *Config) (*rpusend.Metadata, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	compiled, err := rpusend.CompilePropertyFilters(cfg.EffectivePropertyFilter())
	if err != nil {
		return nil, err
	}

	if err := rpusend.ValidateConcatChar(cfg.Processing.TestCaseConcatChar); err != nil {
		return nil, err
	}

	attrs := make([]rpusend.LaunchAttribute, 0, len(cfg.Launch.Attributes))
	for key, value := range cfg.Launch.Attributes {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}

		attrs = append(attrs, rpusend.LaunchAttribute{Key: key, Value: value})
	}

	requestID := uuid.NewString()
	attrs = rpusend.InjectLaunchAttributes(attrs, requestID)

	return &rpusend.Metadata{
		Hostname:               cfg.ReportPortal.Hostname,
		HostnameURL:            rpusend.HostnameURL(cfg.ReportPortal.Hostname),
		Project:                cfg.ReportPortal.Project,
		PropertyFilter:         cfg.EffectivePropertyFilter(),
		PropertyFilterCompiled: compiled,
		TestCaseConcatChar:     cfg.Processing.TestCaseConcatChar,
		LaunchName:             cfg.Launch.Name,
		LaunchDescription:      cfg.Launch.Description,
		LaunchAttributes:       attrs,
		RequestID:              requestID,
	}, nil
}
