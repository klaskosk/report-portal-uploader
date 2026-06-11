package upload

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseConfig_requiredFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name: "valid minimal config",
			input: `
[report_portal]
hostname = "reportportal.example.com"
project = "demo"

[launch]
name = "nightly"

[[sources]]
path = "results/*.xml"
`,
		},
		{
			name: "missing hostname",
			input: `[report_portal]
project = "demo"
[launch]
name = "x"
[[sources]]
path = "a.xml"
`,
			wantErr: "report_portal.hostname is required",
		},
		{
			name: "missing project",
			input: `[report_portal]
hostname = "h"
[launch]
name = "x"
[[sources]]
path = "a.xml"
`,
			wantErr: "report_portal.project is required",
		},
		{
			name: "missing launch name",
			input: `[report_portal]
hostname = "h"
project = "p"
[[sources]]
path = "a.xml"
`,
			wantErr: "launch.name is required",
		},
		{
			name: "missing sources",
			input: `[report_portal]
hostname = "h"
project = "p"
[launch]
name = "x"
`,
			wantErr: "at least one [[sources]] entry is required",
		},
		{
			name: "defaults applied",
			input: `
[report_portal]
hostname = "h"
project = "p"
[launch]
name = "x"
[[sources]]
path = "a.xml"
`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := ParseConfig(strings.NewReader(tc.input))
			if tc.wantErr != "" {
				require.Error(t, err, "expected parse error")
				require.Contains(t, err.Error(), tc.wantErr, "expected specific validation error")

				return
			}

			require.NoError(t, err, "expected config parse to succeed")
			require.Equal(t, defaultLaneProperty, cfg.Processing.LaneProperty, "expected default lane property")
			require.Equal(t, []string{defaultExcludePattern}, cfg.Processing.ExcludePatterns, "expected default exclude pattern")
		})
	}
}

func TestConfig_EffectivePropertyFilter(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Processing: ProcessingConfig{
			LaneProperty:   "suite-group",
			PropertyFilter: []string{"inventory-.*"},
		},
	}

	require.Equal(
		t,
		[]string{"suite-group", "inventory-.*"},
		cfg.EffectivePropertyFilter(),
		"expected lane property plus extras",
	)
}
