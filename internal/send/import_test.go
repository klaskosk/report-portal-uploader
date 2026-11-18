package send_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/klaskosk/report-portal-cli/pkg/types"
	"github.com/klaskosk/report-portal-uploader/internal/send"
	"github.com/stretchr/testify/require"
)

type fakeImportAPI struct {
	importResponse map[string]any
	importErr      error
	latest         *types.PageLaunchViewModel
	launch         *types.LaunchViewModel
}

func (fake *fakeImportAPI) ImportJUnit(
	_ context.Context,
	_, _ string,
	_ *types.LaunchImportRQ,
) (map[string]any, error) {
	return fake.importResponse, fake.importErr
}

func (fake *fakeImportAPI) GetLatestByName(
	_ context.Context,
	_, _ string,
) (*types.PageLaunchViewModel, error) {
	return fake.latest, nil
}

func (fake *fakeImportAPI) GetLaunchByUUID(
	_ context.Context,
	_, _ string,
) (*types.LaunchViewModel, error) {
	return fake.launch, nil
}

func (fake *fakeImportAPI) UpdateLaunch(
	_ context.Context,
	_ string,
	_ int64,
	_ types.UpdateLaunchRQ,
) error {
	return nil
}

func TestRunImport_successPath(t *testing.T) {
	t.Parallel()

	meta := &send.Metadata{
		HostnameURL: "https://rp.example.com/",
		Project:     "demo",
		LaunchName:  "launch",
		RequestID:   "req-1",
		LaunchAttributes: []send.LaunchAttribute{
			{Key: send.LaunchRequestIDAttrKey, Value: "req-1"},
		},
	}

	api := &fakeImportAPI{
		importResponse: map[string]any{
			"data": map[string]any{"id": "uuid-1"},
		},
		launch: &types.LaunchViewModel{Id: 42, Uuid: "uuid-1"},
	}

	result, _, err := send.RunImport(context.Background(), api, meta, "results.zip")
	require.NoError(t, err, "expected import success")
	require.Equal(t, int64(42), result.LaunchID, "expected launch id")
	require.Equal(t, "uuid-1", result.LaunchUUID, "expected launch uuid")
}

func TestRunImport_504RecoveryByRequestID(t *testing.T) {
	t.Parallel()

	meta := &send.Metadata{
		HostnameURL: "https://rp.example.com/",
		Project:     "demo",
		LaunchName:  "launch",
		RequestID:   "req-504",
	}

	key := send.LaunchRequestIDAttrKey
	value := "req-504"
	now := time.Now()
	launches := []types.LaunchViewModel{{
		Id:   99,
		Uuid: "recovered-uuid",
		Attributes: &[]types.ItemAttributeResource{{
			Key:   &key,
			Value: value,
		}},
		LastModified: &now,
	}}

	api := &fakeImportAPI{
		importErr: &send.GatewayTimeoutError{Err: errors.New("HTTP 504 Gateway Timeout")},
		latest:    &types.PageLaunchViewModel{Content: &launches},
		launch:    &types.LaunchViewModel{Id: 99, Uuid: "recovered-uuid"},
	}

	result, _, err := send.RunImport(context.Background(), api, meta, "results.zip")
	require.NoError(t, err, "expected 504 recovery")
	require.Equal(t, "recovered-uuid", result.LaunchUUID, "expected recovered uuid")
}

func TestRunImport_504RecoveryTimeWindowFallback(t *testing.T) {
	t.Parallel()

	meta := &send.Metadata{
		HostnameURL: "https://rp.example.com/",
		Project:     "demo",
		LaunchName:  "launch",
		RequestID:   "req-504",
	}

	now := time.Now()
	launches := []types.LaunchViewModel{{
		Id:           77,
		Uuid:         "recent-uuid",
		LastModified: &now,
	}}

	api := &fakeImportAPI{
		importResponse: map[string]any{"data": map[string]any{"id": ""}},
		latest:         &types.PageLaunchViewModel{Content: &launches},
		launch:         &types.LaunchViewModel{Id: 77, Uuid: "recent-uuid"},
	}

	result, _, err := send.RunImport(context.Background(), api, meta, "results.zip")
	require.NoError(t, err, "expected time-window recovery")
	require.Equal(t, "recent-uuid", result.LaunchUUID, "expected recent launch uuid")
}

func TestRunImport_504RecoveryRejectsStaleLaunch(t *testing.T) {
	t.Parallel()

	meta := &send.Metadata{
		HostnameURL: "https://rp.example.com/",
		Project:     "demo",
		LaunchName:  "launch",
		RequestID:   "req-504",
	}

	stale := time.Now().Add(-2 * time.Minute)
	launches := []types.LaunchViewModel{{
		Id:           77,
		Uuid:         "stale-uuid",
		LastModified: &stale,
	}}

	api := &fakeImportAPI{
		importErr: &send.GatewayTimeoutError{Err: errors.New("HTTP 504 Gateway Timeout")},
		latest:    &types.PageLaunchViewModel{Content: &launches},
	}

	_, _, err := send.RunImport(context.Background(), api, meta, "results.zip")
	require.Error(t, err, "expected stale launch rejection")
}
