package send_test

import (
	"context"
	"testing"
	"time"

	"github.com/klaskosk/report-portal-cli/pkg/types"
	"github.com/klaskosk/report-portal-uploader/internal/send"
	"github.com/stretchr/testify/require"
)

type delayedPostImportAPI struct {
	calls int
	items []types.TestItemResource
}

func (api *delayedPostImportAPI) ListLaunchItems(
	_ context.Context,
	_ string,
	_ int64,
) ([]types.TestItemResource, error) {
	api.calls++

	if api.calls < 3 {
		return api.items[:1], nil
	}

	return api.items, nil
}

func (api *delayedPostImportAPI) ListChildItems(
	_ context.Context,
	_ string,
	_, _ int64,
) ([]types.TestItemResource, error) {
	return nil, nil
}

func (api *delayedPostImportAPI) UpdateItemAttributes(
	_ context.Context,
	_ string,
	_ int64,
	_ []send.LaunchAttribute,
) error {
	return nil
}

func (api *delayedPostImportAPI) CreateAttachmentLogs(
	_ context.Context,
	_, _, _ string,
	_ []string,
	_ string,
) error {
	return nil
}

func TestWaitForLaunchItems_waitsForExpectedCount(t *testing.T) {
	t.Parallel()

	suiteID := int64(1)
	stepID := int64(2)
	suiteName := "Example Ginkgo Suite"
	stepName := "tc"
	suiteType := "SUITE"
	stepType := "TEST"

	api := &delayedPostImportAPI{items: []types.TestItemResource{
		{Id: &suiteID, Name: &suiteName, Type: &suiteType},
		{Id: &stepID, Parent: &suiteID, Name: &stepName, Type: &stepType},
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	items, err := send.WaitForLaunchItems(ctx, api, "demo", 1, 1)
	require.NoError(t, err, "expected wait to succeed")
	require.GreaterOrEqual(t, api.calls, 5, "expected polling until launch items stabilize")
	require.Len(t, items, 2, "expected full launch item list")
}
