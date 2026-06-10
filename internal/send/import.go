package send

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/klaskosk/report-portal-cli/pkg/client"
	"github.com/klaskosk/report-portal-cli/pkg/types"
)

// GatewayTimeoutError indicates the import HTTP call returned 504 while the launch may exist.
type GatewayTimeoutError struct {
	Err error
}

func (e *GatewayTimeoutError) Error() string {
	return fmt.Sprintf("gateway timeout during import: %v", e.Err)
}

func (e *GatewayTimeoutError) Unwrap() error {
	return e.Err
}

// ImportResult holds resolved launch identifiers after import and update.
type ImportResult struct {
	LaunchUUID string
	LaunchID   int64
	LaunchURL  string
}

// RPImportAPI abstracts Report Portal import operations for testing.
type RPImportAPI interface {
	ImportJUnit(
		ctx context.Context,
		project, zipPath string,
		launchImportRQ *types.LaunchImportRQ,
	) (map[string]any, error)
	GetLatestByName(ctx context.Context, project, launchName string) (*types.PageLaunchResource, error)
	GetLaunchByUUID(ctx context.Context, project, launchUUID string) (*types.LaunchResource, error)
	UpdateLaunch(ctx context.Context, project string, launchID int64, request types.UpdateLaunchRQ) error
}

// ClientImportAPI adapts report-portal-cli client to RPImportAPI.
type ClientImportAPI struct {
	Client *client.Client
}

// ImportJUnit uploads a zip via the junit plugin.
func (api *ClientImportAPI) ImportJUnit(
	ctx context.Context,
	project, zipPath string,
	launchImportRQ *types.LaunchImportRQ,
) (map[string]any, error) {
	file, err := os.Open(zipPath)
	if err != nil {
		return nil, fmt.Errorf("opening zip: %w", err)
	}
	defer file.Close()

	return api.Client.Plugins().Import(
		ctx,
		project,
		"junit",
		filepathBase(zipPath),
		file,
		launchImportRQ,
	)
}

// GetLatestByName returns latest launches filtered by name.
func (api *ClientImportAPI) GetLatestByName(
	ctx context.Context,
	project, launchName string,
) (*types.PageLaunchResource, error) {
	scope := api.Client.Project(project)

	return scope.Launches().GetLatest(ctx, client.WithLaunchName(launchName))
}

// GetLaunchByUUID fetches a launch by UUID.
func (api *ClientImportAPI) GetLaunchByUUID(
	ctx context.Context,
	project, launchUUID string,
) (*types.LaunchResource, error) {
	scope := api.Client.Project(project)

	return scope.Launches().GetByUUID(ctx, launchUUID)
}

// UpdateLaunch updates launch metadata after import.
func (api *ClientImportAPI) UpdateLaunch(
	ctx context.Context,
	project string,
	launchID int64,
	request types.UpdateLaunchRQ,
) error {
	scope := api.Client.Project(project)

	_, err := scope.Launches().Update(ctx, launchID, request)
	if err != nil {
		return fmt.Errorf("updating launch: %w", err)
	}

	return nil
}

func filepathBase(path string) string {
	parts := strings.Split(path, string(os.PathSeparator))

	return parts[len(parts)-1]
}

// RunImport zips XMLs, imports via junit plugin, recovers from 504, and updates launch metadata.
func RunImport(
	ctx context.Context,
	api RPImportAPI,
	meta *Metadata,
	zipPath string,
) (ImportResult, WarningList, error) {
	var warnings WarningList

	response, err := api.ImportJUnit(ctx, meta.Project, zipPath, meta.BuildLaunchImportRQ())

	launchUUID, parseErr := parseImportLaunchUUID(response)
	needsRecovery := isGatewayTimeout(err) || parseErr != nil || launchUUID == ""
	if err != nil && !isGatewayTimeout(err) {
		return ImportResult{}, warnings, fmt.Errorf("importing results: %w", err)
	}

	if needsRecovery {
		recovered, recoverErr := recoverLaunchAfterTimeout(ctx, api, meta)
		if recoverErr != nil {
			return ImportResult{}, warnings, recoverErr
		}

		launchUUID = recovered
	}

	launch, err := api.GetLaunchByUUID(ctx, meta.Project, launchUUID)
	if err != nil {
		return ImportResult{}, warnings, fmt.Errorf("fetching launch by uuid: %w", err)
	}

	err = api.UpdateLaunch(ctx, meta.Project, launch.Id, meta.BuildUpdateLaunchRQ())
	if err != nil {
		return ImportResult{}, warnings, err
	}

	return ImportResult{
		LaunchUUID: launch.Uuid,
		LaunchID:   launch.Id,
		LaunchURL:  LaunchUIURL(meta, launch.Id),
	}, warnings, nil
}

func parseImportLaunchUUID(response map[string]any) (string, error) {
	if response == nil {
		return "", fmt.Errorf("empty import response")
	}

	data, ok := response["data"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("import response missing data object")
	}

	idValue, ok := data["id"]
	if !ok {
		return "", fmt.Errorf("import response missing data.id")
	}

	switch typed := idValue.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return "", fmt.Errorf("import response data.id is empty")
		}

		return typed, nil
	default:
		return "", fmt.Errorf("import response data.id has unexpected type %T", idValue)
	}
}

func isGatewayTimeout(err error) bool {
	if err == nil {
		return false
	}

	var gatewayErr *GatewayTimeoutError

	return errors.As(err, &gatewayErr) || strings.Contains(strings.ToLower(err.Error()), "504")
}

func recoverLaunchAfterTimeout(ctx context.Context, api RPImportAPI, meta *Metadata) (string, error) {
	page, err := api.GetLatestByName(ctx, meta.Project, meta.LaunchName)
	if err != nil {
		return "", fmt.Errorf("recovering launch after timeout: %w", err)
	}

	if page == nil || page.Content == nil || len(*page.Content) == 0 {
		return "", fmt.Errorf("recovering launch after timeout: no latest launch found for name %q", meta.LaunchName)
	}

	latest := (*page.Content)[0]

	for _, attr := range derefAttributes(latest.Attributes) {
		if attr.Key != nil && *attr.Key == launchRequestIDAttrKey {
			if attr.Value == meta.RequestID {
				return latest.Uuid, nil
			}

			return "", fmt.Errorf(
				"recovering launch after timeout: request ID on latest launch does not match",
			)
		}
	}

	if latest.LastModified != nil {
		elapsed := time.Since(*latest.LastModified)
		if elapsed < 0 {
			elapsed = -elapsed
		}

		if elapsed > 50*time.Second {
			return "", fmt.Errorf("recovering launch after timeout: latest launch is older than 50 seconds")
		}
	}

	return latest.Uuid, nil
}

func derefAttributes(attrs *[]types.ItemAttributeResource) []types.ItemAttributeResource {
	if attrs == nil {
		return nil
	}

	return *attrs
}

// BuildImportZip creates the junit import zip in workDir.
func BuildImportZip(workDir string, xmlPaths []string) (string, error) {
	zipPath := filepathJoin(workDir, "results.zip")

	err := BuildResultsZip(xmlPaths, zipPath)
	if err != nil {
		return "", err
	}

	return zipPath, nil
}

func filepathJoin(elem ...string) string {
	return strings.Join(elem, string(os.PathSeparator))
}

// ImportZipReader is used by tests to avoid filesystem zip creation.
type ImportZipReader struct {
	Filename string
	Reader   io.Reader
}
