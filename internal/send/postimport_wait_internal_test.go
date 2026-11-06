package send

import (
	"context"
	"testing"
	"time"

	"github.com/klaskosk/report-portal-cli/pkg/types"
	"github.com/stretchr/testify/require"
)

type insufficientPostImportAPI struct {
	items []types.TestItemResource
}

func (api *insufficientPostImportAPI) ListLaunchItems(
	_ context.Context,
	_ string,
	_ int64,
) ([]types.TestItemResource, error) {
	return api.items, nil
}

func (api *insufficientPostImportAPI) ListChildItems(
	_ context.Context,
	_ string,
	_, _ int64,
) ([]types.TestItemResource, error) {
	return nil, nil
}

func (api *insufficientPostImportAPI) UpdateItemAttributes(
	_ context.Context,
	_ string,
	_ int64,
	_ []LaunchAttribute,
) error {
	return nil
}

func (api *insufficientPostImportAPI) CreateAttachmentLogs(
	_ context.Context,
	_, _, _ string,
	_ []string,
	_ string,
) error {
	return nil
}

func swapWaitForLaunchItemsTiming(timeout, pollInterval time.Duration) func() {
	waitForLaunchItemsTimingMu.Lock()
	oldTimeout := waitForLaunchItemsTimeout
	oldPoll := waitForLaunchItemsPollInterval
	waitForLaunchItemsTimeout = timeout
	waitForLaunchItemsPollInterval = pollInterval
	waitForLaunchItemsTimingMu.Unlock()

	return func() {
		waitForLaunchItemsTimingMu.Lock()
		waitForLaunchItemsTimeout = oldTimeout
		waitForLaunchItemsPollInterval = oldPoll
		waitForLaunchItemsTimingMu.Unlock()
	}
}

func TestWaitForLaunchItems_timesOutWhenItemsNeverStabilize(t *testing.T) {
	t.Parallel()

	restore := swapWaitForLaunchItemsTiming(50*time.Millisecond, 10*time.Millisecond)
	defer restore()

	testID := int64(1)
	testName := "tc"
	testType := "TEST"

	api := &insufficientPostImportAPI{items: []types.TestItemResource{
		{Id: &testID, Name: &testName, Type: &testType},
	}}

	items, err := WaitForLaunchItems(context.Background(), api, "demo", 1, 5)
	require.Error(t, err, "expected timeout error")
	require.Nil(t, items, "expected no items on timeout")
	require.Contains(t, err.Error(), "timed out", "expected timeout message")
}
