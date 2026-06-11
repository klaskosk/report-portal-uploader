package upload_test

import (
	"testing"

	rpusend "github.com/klaskosk/report-portal-uploader/internal/send"
	"github.com/klaskosk/report-portal-uploader/internal/upload"
	"github.com/stretchr/testify/require"
)

func TestBuildMetadata_customLanePropertyInFilter(t *testing.T) {
	t.Parallel()

	cfg := &upload.Config{
		ReportPortal: upload.ReportPortalConfig{Hostname: "rp.example.com", Project: "demo"},
		Launch: upload.LaunchConfig{
			Name:       "launch",
			Attributes: map[string]string{"profile": "core"},
		},
		Sources: []upload.SourceConfig{{Path: "a.xml"}},
		Processing: upload.ProcessingConfig{
			LaneProperty: "suite-group",
		},
	}

	meta, err := upload.BuildMetadata(cfg)
	require.NoError(t, err, "expected metadata build")
	require.Equal(
		t,
		[]string{"suite-group"},
		meta.PropertyFilter,
		"expected lane property in filter",
	)
	require.True(
		t,
		rpusend.PropertyNameMatches(meta.PropertyFilterCompiled, "suite-group"),
		"expected compiled filter match",
	)
}
