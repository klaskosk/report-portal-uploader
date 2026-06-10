package send

import (
	"context"
	"testing"

	"github.com/klaskosk/report-portal-cli/pkg/types"
	"github.com/stretchr/testify/require"
)

type fakePostImportAPI struct {
	items       []types.TestItemResource
	updated     []int64
	attachments []string
}

func (fake *fakePostImportAPI) ListLaunchItems(
	_ context.Context,
	_ string,
	_ int64,
) ([]types.TestItemResource, error) {
	out := make([]types.TestItemResource, len(fake.items))
	for i, item := range fake.items {
		copy := item
		copy.Parent = nil
		out[i] = copy
	}

	return out, nil
}

func (fake *fakePostImportAPI) ListChildItems(
	_ context.Context,
	_ string,
	_, parentID int64,
) ([]types.TestItemResource, error) {
	var out []types.TestItemResource
	for _, item := range fake.items {
		if item.Parent != nil && *item.Parent == parentID {
			out = append(out, item)
		}
	}

	return out, nil
}

func (fake *fakePostImportAPI) UpdateItemAttributes(
	_ context.Context,
	_ string,
	itemID int64,
	_ []LaunchAttribute,
) error {
	fake.updated = append(fake.updated, itemID)

	return nil
}

func (fake *fakePostImportAPI) CreateAttachmentLogs(
	_ context.Context,
	_, _, _ string,
	attachments []string,
	_ string,
) error {
	fake.attachments = append(fake.attachments, attachments...)

	return nil
}

func TestRunPostImport_matchesNestedTree(t *testing.T) {
	t.Parallel()

	rootID := int64(1)
	suiteID := int64(2)
	itemID := int64(3)
	itemUUID := "item-uuid"
	rootName := "Sample"
	suiteName := "Outer"
	itemName := "tc"
	itemType := "TEST"

	meta := &Metadata{Project: "demo"}
	importResult := ImportResult{LaunchID: 10, LaunchUUID: "launch-uuid"}

	index := TestCaseIndex{Roots: []*TestItem{{
		Name: "Sample",
		Type: testItemTypeSuite,
		Children: []*TestItem{{
			Name: "Outer",
			Type: testItemTypeSuite,
			Children: []*TestItem{{
				Name:       "tc",
				Type:       testItemTypeCase,
				Properties: []LaunchAttribute{{Key: "ci-lane", Value: "main"}},
				Attachments: []string{"a/log.txt"},
			}},
		}},
	}}}

	api := &fakePostImportAPI{items: []types.TestItemResource{
		{Id: &rootID, Name: &rootName, Type: strPtr("SUITE")},
		{Id: &suiteID, Parent: &rootID, Name: &suiteName, Type: strPtr("SUITE")},
		{Id: &itemID, Parent: &suiteID, Name: &itemName, Uuid: &itemUUID, Type: &itemType},
	}}

	warnings, err := RunPostImport(context.Background(), api, meta, importResult, index, "")
	require.NoError(t, err, "expected post-import success")
	require.Empty(t, warnings, "expected no matching warnings")
	require.Equal(t, []int64{3}, api.updated, "expected item update")
}

func TestRunPostImport_matchesGinkgoStyleFlatSuite(t *testing.T) {
	t.Parallel()

	suiteID := int64(10)
	stepID := int64(11)
	stepUUID := "step-uuid"
	suiteName := "Example Ginkgo Suite"
	stepName := "[It] verifies stability under load [stability, 10001, test_id:10001]"
	stepType := "TEST"

	meta := &Metadata{Project: "demo"}
	importResult := ImportResult{LaunchID: 20, LaunchUUID: "launch-uuid"}

	index := TestCaseIndex{Roots: []*TestItem{{
		Name: "Example Ginkgo Suite",
		Type: testItemTypeSuite,
		Children: []*TestItem{{
			Name:       stepName,
			Type:       testItemTypeCase,
			Properties: []LaunchAttribute{{Key: "inventory-testcase-id", Value: "10001"}},
		}},
	}}}

	api := &fakePostImportAPI{items: []types.TestItemResource{
		{Id: &suiteID, Name: &suiteName, Type: strPtr("SUITE")},
		{Id: &stepID, Parent: &suiteID, Name: &stepName, Uuid: &stepUUID, Type: &stepType},
	}}

	warnings, err := RunPostImport(context.Background(), api, meta, importResult, index, "")
	require.NoError(t, err, "expected post-import success")
	require.Empty(t, warnings, "expected no matching warnings")
	require.Equal(t, []int64{11}, api.updated, "expected step item update")
}

func TestRunPostImport_duplicateSuiteNamesUseIndex(t *testing.T) {
	t.Parallel()

	firstSuiteID := int64(1)
	secondSuiteID := int64(2)
	firstStepID := int64(3)
	secondStepID := int64(4)
	firstStepUUID := "first-step-uuid"
	secondStepUUID := "second-step-uuid"
	suiteName := "S"
	stepName := "same"
	stepType := "TEST"

	meta := &Metadata{Project: "demo"}
	importResult := ImportResult{LaunchID: 30, LaunchUUID: "launch-uuid"}

	index := TestCaseIndex{Roots: []*TestItem{
		{
			Name:  "S",
			Type:  testItemTypeSuite,
			Index: 0,
			Children: []*TestItem{{
				Name:       "same",
				Type:       testItemTypeCase,
				Properties: []LaunchAttribute{{Key: "ci-lane", Value: "first"}},
			}},
		},
		{
			Name:  "S",
			Type:  testItemTypeSuite,
			Index: 1,
			Children: []*TestItem{{
				Name:       "same",
				Type:       testItemTypeCase,
				Properties: []LaunchAttribute{{Key: "ci-lane", Value: "second"}},
			}},
		},
	}}

	api := &fakePostImportAPI{items: []types.TestItemResource{
		{Id: &firstSuiteID, Name: &suiteName, Type: strPtr("SUITE")},
		{Id: &firstStepID, Parent: &firstSuiteID, Name: &stepName, Uuid: &firstStepUUID, Type: &stepType},
		{Id: &secondSuiteID, Name: &suiteName, Type: strPtr("SUITE")},
		{Id: &secondStepID, Parent: &secondSuiteID, Name: &stepName, Uuid: &secondStepUUID, Type: &stepType},
	}}

	warnings, err := RunPostImport(context.Background(), api, meta, importResult, index, "")
	require.NoError(t, err, "expected post-import success")
	require.Empty(t, warnings, "expected no matching warnings")
	require.ElementsMatch(t, []int64{3, 4}, api.updated, "expected both suite steps updated")
}

func TestRunPostImport_matchesChildrenWithoutParentInListResponse(t *testing.T) {
	t.Parallel()

	suiteID := int64(10)
	stepID := int64(11)
	stepUUID := "step-uuid"
	suiteName := "Example Ginkgo Suite"
	stepName := "[It] verifies stability under load [stability, 10001, test_id:10001]"
	stepType := "TEST"

	meta := &Metadata{Project: "demo"}
	importResult := ImportResult{LaunchID: 40, LaunchUUID: "launch-uuid"}

	index := TestCaseIndex{Roots: []*TestItem{{
		Name: "Example Ginkgo Suite",
		Type: testItemTypeSuite,
		Children: []*TestItem{{
			Name:       stepName,
			Type:       testItemTypeCase,
			Properties: []LaunchAttribute{{Key: "inventory-testcase-id", Value: "INV-10001"}},
		}},
	}}}

	api := &fakePostImportAPI{items: []types.TestItemResource{
		{Id: &suiteID, Name: &suiteName, Type: strPtr("SUITE")},
		{Id: &stepID, Parent: &suiteID, Name: &stepName, Uuid: &stepUUID, Type: &stepType},
	}}

	warnings, err := RunPostImport(context.Background(), api, meta, importResult, index, "")
	require.NoError(t, err, "expected post-import success")
	require.Empty(t, warnings, "expected no matching warnings")
	require.Equal(t, []int64{11}, api.updated, "expected step item update")
}

func TestRunPostImport_skippedWhenDisabled(t *testing.T) {
	t.Parallel()

	meta := &Metadata{DisableTestItemUpdater: true}
	api := &fakePostImportAPI{}

	warnings, err := RunPostImport(context.Background(), api, meta, ImportResult{}, TestCaseIndex{}, "")
	require.NoError(t, err, "expected disabled updater to no-op")
	require.Nil(t, warnings, "expected no warnings")
	require.Empty(t, api.updated, "expected no updates")
}

func strPtr(value string) *string {
	return &value
}
