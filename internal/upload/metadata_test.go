package upload

import (
	"testing"

	rpusend "github.com/klaskosk/report-portal-uploader/internal/send"
	"github.com/stretchr/testify/require"
)

func TestBuildMetadata_customLanePropertyInFilter(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		ReportPortal: ReportPortalConfig{Hostname: "rp.example.com", Project: "demo"},
		Launch: LaunchConfig{
			Name:       "launch",
			Attributes: map[string]string{"profile": "core"},
		},
		Sources: []SourceConfig{{Path: "a.xml"}},
		Processing: ProcessingConfig{
			LaneProperty: "suite-group",
		},
	}

	meta, err := BuildMetadata(cfg)
	require.NoError(t, err, "expected metadata build")
	require.Equal(t, []string{"suite-group"}, meta.PropertyFilter, "expected lane property in filter")
	require.True(t, rpusend.PropertyNameMatches(meta.PropertyFilterCompiled, "suite-group"), "expected compiled filter match")
}
