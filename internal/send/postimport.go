package send

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/klaskosk/report-portal-cli/pkg/client"
	"github.com/klaskosk/report-portal-cli/pkg/client/log"
	"github.com/klaskosk/report-portal-cli/pkg/types"
)

const postImportConcurrency = 10

// RPPostImportAPI abstracts post-import item and log operations.
type RPPostImportAPI interface {
	ListLaunchItems(ctx context.Context, project string, launchID int64) ([]types.TestItemResource, error)
	ListChildItems(ctx context.Context, project string, launchID, parentID int64) ([]types.TestItemResource, error)
	UpdateItemAttributes(
		ctx context.Context,
		project string,
		itemID int64,
		attributes []LaunchAttribute,
	) error
	CreateAttachmentLogs(
		ctx context.Context,
		project string,
		launchUUID, itemUUID string,
		attachments []string,
		attachmentsDir string,
	) error
}

// ClientPostImportAPI adapts report-portal-cli client to RPPostImportAPI.
type ClientPostImportAPI struct {
	Client *client.Client
}

// ListLaunchItems fetches all items in a launch with pagination.
func (api *ClientPostImportAPI) ListLaunchItems(
	ctx context.Context,
	project string,
	launchID int64,
) ([]types.TestItemResource, error) {
	scope := api.Client.Project(project)
	var all []types.TestItemResource
	page := int32(0)
	pageSize := int32(300)

	for {
		result, err := scope.Items().List(
			ctx,
			client.WithItemLaunchID(launchID),
			client.WithItemPageNumber(page),
			client.WithItemPageSize(pageSize),
		)
		if err != nil {
			return nil, fmt.Errorf("listing launch items: %w", err)
		}

		if result == nil || result.Content == nil {
			break
		}

		all = append(all, *result.Content...)

		if result.Page == nil || result.Page.HasNext == nil || !*result.Page.HasNext {
			break
		}

		page++
	}

	return all, nil
}

// ListChildItems fetches direct children of a parent item filtered by parentId.
func (api *ClientPostImportAPI) ListChildItems(
	ctx context.Context,
	project string,
	launchID, parentID int64,
) ([]types.TestItemResource, error) {
	scope := api.Client.Project(project)
	var all []types.TestItemResource
	page := int32(0)
	pageSize := int32(300)

	for {
		result, err := scope.Items().List(
			ctx,
			client.WithItemLaunchID(launchID),
			client.WithItemParentID(parentID),
			client.WithItemPageNumber(page),
			client.WithItemPageSize(pageSize),
		)
		if err != nil {
			return nil, fmt.Errorf("listing child items for parent %d: %w", parentID, err)
		}

		if result == nil || result.Content == nil {
			break
		}

		all = append(all, *result.Content...)

		if result.Page == nil || result.Page.HasNext == nil || !*result.Page.HasNext {
			break
		}

		page++
	}

	return all, nil
}

// UpdateItemAttributes updates a single item with custom attributes.
func (api *ClientPostImportAPI) UpdateItemAttributes(
	ctx context.Context,
	project string,
	itemID int64,
	attributes []LaunchAttribute,
) error {
	if len(attributes) == 0 {
		return nil
	}

	attrs := make([]types.ItemAttributeResource, len(attributes))
	for i, attribute := range attributes {
		key := attribute.Key
		attrs[i] = types.ItemAttributeResource{
			Key:   &key,
			Value: attribute.Value,
		}
	}

	scope := api.Client.Project(project)

	_, err := scope.Items().Update(ctx, itemID, types.UpdateTestItemRQ{Attributes: &attrs})
	if err != nil {
		return fmt.Errorf("updating item %d: %w", itemID, err)
	}

	return nil
}

// CreateAttachmentLogs uploads attachments as ERROR-level logs on the item.
func (api *ClientPostImportAPI) CreateAttachmentLogs(
	ctx context.Context,
	project, launchUUID, itemUUID string,
	attachments []string,
	attachmentsDir string,
) error {
	if len(attachments) == 0 || attachmentsDir == "" {
		return nil
	}

	entries := make([]log.BatchLogEntry, 0, len(attachments))
	for _, relativePath := range attachments {
		data, err := os.ReadFile(filepath.Join(attachmentsDir, relativePath))
		if err != nil {
			return fmt.Errorf("reading attachment %q: %w", relativePath, err)
		}

		entries = append(entries, log.BatchLogEntry{
			LaunchUUID:     launchUUID,
			ItemUUID:       itemUUID,
			Level:          "ERROR",
			Message:        filepath.Base(relativePath),
			AttachmentName: filepath.Base(relativePath),
			AttachmentData: data,
		})
	}

	scope := api.Client.Project(project)

	_, err := scope.Logs().CreateBatch(ctx, entries...)
	if err != nil {
		return fmt.Errorf("creating attachment logs: %w", err)
	}

	return nil
}

// RunPostImport applies item attributes and attachments from the TestCaseIndex.
func RunPostImport(
	ctx context.Context,
	api RPPostImportAPI,
	meta *Metadata,
	importResult ImportResult,
	index TestCaseIndex,
	attachmentsDir string,
) (WarningList, error) {
	if meta.DisableTestItemUpdater {
		return nil, nil
	}

	items, err := waitForLaunchItems(ctx, api, meta.Project, importResult.LaunchID, len(index.Steps()))
	if err != nil {
		return nil, err
	}

	byName := buildItemsByName(items)

	var warnings WarningList

	resolveParentItemIDs(index.Roots, byName, &warnings)
	err = resolveChildItemIDs(ctx, api, meta.Project, importResult.LaunchID, index.Roots)
	if err != nil {
		return warnings, err
	}

	work := collectUpdateWork(index.Roots, &warnings)

	sem := make(chan struct{}, postImportConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, task := range work {
		wg.Add(1)

		go func(task *TestItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if task.rpItemID == nil || task.rpItemUUID == nil {
				mu.Lock()
				warnings.Add("postprocess", fmt.Sprintf("Matched item for %s %q lacks id/uuid", task.Type, task.Name))
				mu.Unlock()

				return
			}

			err := api.UpdateItemAttributes(ctx, meta.Project, *task.rpItemID, task.Properties)
			if err != nil {
				mu.Lock()
				warnings.Add("postprocess", err.Error())
				mu.Unlock()

				return
			}

			err = api.CreateAttachmentLogs(
				ctx,
				meta.Project,
				importResult.LaunchUUID,
				*task.rpItemUUID,
				task.Attachments,
				attachmentsDir,
			)
			if err != nil {
				mu.Lock()
				warnings.Add("postprocess", err.Error())
				mu.Unlock()
			}
		}(task)
	}

	wg.Wait()

	return warnings, nil
}

func waitForLaunchItems(
	ctx context.Context,
	api RPPostImportAPI,
	project string,
	launchID int64,
	expectedTestItems int,
) ([]types.TestItemResource, error) {
	deadline := time.Now().Add(2 * time.Minute)
	target := expectedTestItems
	if target == 0 {
		target = 1
	}

	var lastCount int
	stablePolls := 0

	for {
		items, err := api.ListLaunchItems(ctx, project, launchID)
		if err != nil {
			return nil, err
		}

		testCount := countLaunchTestItems(items)
		if testCount >= target && testCount == lastCount {
			stablePolls++
			if stablePolls >= 2 {
				return items, nil
			}
		} else {
			stablePolls = 0
		}

		lastCount = testCount

		if time.Now().After(deadline) {
			return items, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func countLaunchTestItems(items []types.TestItemResource) int {
	count := 0
	for _, item := range items {
		if item.Type != nil && (*item.Type == "TEST" || *item.Type == "STEP") {
			count++
		}
	}

	return count
}

func buildItemsByName(items []types.TestItemResource) map[string][]types.TestItemResource {
	byName := make(map[string][]types.TestItemResource)

	for _, item := range items {
		if item.Name != nil {
			byName[*item.Name] = append(byName[*item.Name], item)
		}
	}

	return byName
}

// resolveParentItemIDs resolves TEST nodes by launch-wide name with index disambiguation.
func resolveParentItemIDs(roots []*TestItem, byName map[string][]types.TestItemResource, warnings *WarningList) {
	var walk func([]*TestItem)
	walk = func(nodes []*TestItem) {
		for _, item := range nodes {
			if item.Type == testItemTypeSuite {
				assignItemByName(item, byName, warnings)
			}

			walk(item.Children)
		}
	}

	walk(roots)
}

// resolveChildItemIDs fetches children of each resolved TEST node by parentId and matches by exact name.
func resolveChildItemIDs(
	ctx context.Context,
	api RPPostImportAPI,
	project string,
	launchID int64,
	roots []*TestItem,
) error {
	var walk func([]*TestItem) error
	walk = func(nodes []*TestItem) error {
		for _, item := range nodes {
			if item.Type == testItemTypeSuite && item.rpItemID != nil {
				rpChildren, err := api.ListChildItems(ctx, project, launchID, *item.rpItemID)
				if err != nil {
					return err
				}

				for _, jsonChild := range item.Children {
					rpChild := findRPItemByName(rpChildren, jsonChild.Name)
					if rpChild == nil {
						continue
					}

					jsonChild.rpItemID = rpChild.Id
					jsonChild.rpItemUUID = rpChild.Uuid
				}
			}

			if err := walk(item.Children); err != nil {
				return err
			}
		}

		return nil
	}

	return walk(roots)
}

func assignItemByName(item *TestItem, byName map[string][]types.TestItemResource, warnings *WarningList) {
	matches := byName[item.Name]
	if len(matches) == 0 {
		if itemNeedsUpdate(item) {
			warnings.Add("postprocess", fmt.Sprintf("No Report Portal item matched %s %q", item.Type, item.Name))
		}

		return
	}

	chosen := matches[0]
	if item.Index < len(matches) {
		chosen = matches[item.Index]
	}

	item.rpItemID = chosen.Id
	item.rpItemUUID = chosen.Uuid
}

func findRPItemByName(items []types.TestItemResource, name string) *types.TestItemResource {
	for i := range items {
		if items[i].Name != nil && *items[i].Name == name {
			return &items[i]
		}
	}

	return nil
}

func itemNeedsUpdate(item *TestItem) bool {
	return len(item.Properties) > 0 || len(item.Attachments) > 0
}

func collectUpdateWork(roots []*TestItem, warnings *WarningList) []*TestItem {
	var work []*TestItem

	var walk func([]*TestItem)
	walk = func(nodes []*TestItem) {
		for _, item := range nodes {
			if itemNeedsUpdate(item) {
				if item.rpItemID == nil || item.rpItemUUID == nil {
					warnings.Add("postprocess", fmt.Sprintf("No Report Portal item matched %s %q", item.Type, item.Name))
				} else {
					work = append(work, item)
				}
			}

			walk(item.Children)
		}
	}

	walk(roots)

	return work
}
