package send

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/klaskosk/report-portal-uploader/internal/xunit"
)

const (
	xmlTestsuite = "testsuite"
	xmlTestcase  = "testcase"
	xmlProperty  = "property"
	xmlFailure   = "failure"
)

var (
	doNotWarnEmptyValue = map[string]struct{}{
		"line.separator":  {},
		"sun.cpu.isalist": {},
	}
	doNotWarnLongValue = map[string]struct{}{
		"java.class.path":          {},
		"surefire.test.class.path": {},
		"sun.java.command":         {},
	}
)

const (
	testItemTypeSuite = "TEST" // XML testsuite node
	testItemTypeCase  = "STEP" // XML testcase node
)

// TestItem is one testsuite or testcase node for post-import matching.
type TestItem struct {
	XMLName     string
	Name        string
	ClassName   string
	ID          string
	Type        string
	Index       int
	Properties  []LaunchAttribute
	Attachments []string
	Children    []*TestItem

	rpItemID   *int64
	rpItemUUID *string
}

// TestCaseIndex holds root test items built from prepared XML files.
type TestCaseIndex struct {
	Roots []*TestItem
}

// Steps returns all STEP nodes in document order.
func (index TestCaseIndex) Steps() []*TestItem {
	var steps []*TestItem

	var walk func([]*TestItem)

	walk = func(nodes []*TestItem) {
		for _, node := range nodes {
			if node.Type == testItemTypeCase {
				steps = append(steps, node)
			}

			walk(node.Children)
		}
	}

	walk(index.Roots)

	return steps
}

// PrepareXML transforms source XML and returns output bytes plus warnings.
func PrepareXML(input []byte, concatChar string) ([]byte, WarningList, error) {
	doc, err := xunit.ParseDocument(input)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing document: %w", err)
	}

	var warnings WarningList

	err = removeInvalidTestcases(doc, &warnings, true)
	if err != nil {
		return nil, warnings, err
	}

	normalizeTimestamps(doc)
	populateFailureBodies(doc)
	truncateProperties(doc, &warnings)

	if concatChar != "" {
		applyConcatChar(doc, concatChar)
	}

	for removeEmptySuites(doc) {
	}

	if !hasAnyTestcase(doc) {
		return nil, warnings, errors.New("no valid testcases found in XML")
	}

	output, err := xunit.EncodeDocument(doc)
	if err != nil {
		return nil, warnings, fmt.Errorf("encoding document: %w", err)
	}

	return output, warnings, nil
}

// BuildTestCaseIndex scans transformed XML files and resolves attachment paths.
func BuildTestCaseIndex(
	xmlPaths []string,
	attachmentsDir string,
	meta *Metadata,
) (TestCaseIndex, WarningList, error) {
	var warnings WarningList

	index := TestCaseIndex{}
	indexes := map[string]int{}

	for _, xmlPath := range xmlPaths {
		basename := strings.TrimSuffix(filepath.Base(xmlPath), filepath.Ext(xmlPath))

		// #nosec G304 -- xmlPath is a prepared artifact under workDir
		content, err := os.ReadFile(xmlPath)
		if err != nil {
			return index, warnings, fmt.Errorf("reading %q: %w", xmlPath, err)
		}

		roots, err := collectTestItemTree(content, basename, attachmentsDir, meta, indexes)
		if err != nil {
			return index, warnings, err
		}

		index.Roots = append(index.Roots, roots...)
	}

	return index, warnings, nil
}

func collectItemProperties(
	node *xunit.Node,
	item *TestItem,
	xmlBasename, attachmentsDir string,
	meta *Metadata,
) {
	var walk func(*xunit.Node)

	walk = func(current *xunit.Node) {
		if current == nil || current.Skip {
			return
		}

		if current.Name.Local == xmlTestsuite || current.Name.Local == xmlTestcase {
			return
		}

		if current.Name.Local == xmlProperty {
			propName := xunit.AttrValue(current.Attrs, "name")
			propValue := xunit.AttrValue(current.Attrs, "value")

			if PropertyNameMatches(meta.PropertyFilterCompiled, propName) {
				item.Properties = append(item.Properties, LaunchAttribute{
					Key:   propName,
					Value: propValue,
				})

				if propName == "attachment" {
					attach := resolveAttachmentPaths(propValue, "", "", xmlBasename, attachmentsDir)
					item.Attachments = append(item.Attachments, attach...)
				}
			}

			return
		}

		for _, child := range current.Children {
			walk(child)
		}
	}

	for _, child := range node.Children {
		walk(child)
	}
}

func collectTestItemTree(
	content []byte,
	xmlBasename, attachmentsDir string,
	meta *Metadata,
	indexes map[string]int,
) ([]*TestItem, error) {
	doc, err := xunit.ParseDocument(content)
	if err != nil {
		return nil, fmt.Errorf("parsing document: %w", err)
	}

	if indexes == nil {
		indexes = map[string]int{}
	}

	var roots []*TestItem

	for _, child := range doc.Children {
		if built := buildTestItem(
			child,
			"",
			xmlBasename,
			attachmentsDir,
			meta,
			indexes,
		); built != nil {
			roots = append(roots, built)
		}
	}

	return roots, nil
}

func buildTestItem(
	node *xunit.Node,
	parentName, xmlBasename, attachmentsDir string,
	meta *Metadata,
	indexes map[string]int,
) *TestItem {
	if node == nil || node.Skip || !isTestItemNode(node) {
		return nil
	}

	name := xunit.AttrValue(node.Attrs, "name")
	className := xunit.AttrValue(node.Attrs, "classname")
	testCaseID := xunit.AttrValue(node.Attrs, "id")
	itemType, childParentName := testItemTypeAndChildParent(node, parentName, name)

	item := newTestItem(
		xmlBasename,
		name,
		className,
		testCaseID,
		itemType,
		uniqueTestItemIndex(indexes, parentName, name),
		attachmentsDir,
	)

	for _, child := range node.Children {
		if built := buildTestItem(
			child,
			childParentName,
			xmlBasename,
			attachmentsDir,
			meta,
			indexes,
		); built != nil {
			item.Children = append(item.Children, built)
		}
	}

	collectItemProperties(node, item, xmlBasename, attachmentsDir, meta)
	appendTestCaseAttachments(item, node, name, className, testCaseID, xmlBasename, attachmentsDir)

	return item
}

func isTestItemNode(node *xunit.Node) bool {
	return node.Name.Local == xmlTestsuite || node.Name.Local == xmlTestcase
}

func testItemTypeAndChildParent(node *xunit.Node, parentName, name string) (string, string) {
	if node.Name.Local == xmlTestcase {
		return testItemTypeCase, parentName
	}

	return testItemTypeSuite, name
}

func uniqueTestItemIndex(indexes map[string]int, parentName, name string) int {
	uniqueName := name
	if parentName != "" {
		uniqueName = parentName + name
	}

	index := indexes[uniqueName]
	indexes[uniqueName] = index + 1

	return index
}

func newTestItem(
	xmlBasename, name, className, testCaseID, itemType string,
	index int,
	attachmentsDir string,
) *TestItem {
	return &TestItem{
		XMLName:   xmlBasename,
		Name:      name,
		ClassName: className,
		ID:        testCaseID,
		Type:      itemType,
		Index:     index,
		Attachments: resolveAttachmentPaths(
			name,
			className,
			testCaseID,
			xmlBasename,
			attachmentsDir,
		),
	}
}

func appendTestCaseAttachments(
	item *TestItem,
	node *xunit.Node,
	name, className, testCaseID, xmlBasename, attachmentsDir string,
) {
	if node.Name.Local != xmlTestcase {
		return
	}

	dirAttachments := resolveAttachmentPaths(
		name,
		className,
		testCaseID,
		xmlBasename,
		attachmentsDir,
	)
	item.Attachments = uniqueStrings(append(item.Attachments, dirAttachments...))
}

func resolveAttachmentPaths(
	name, className, testCaseID, xmlBasename, attachmentsDir string,
) []string {
	if attachmentsDir == "" {
		return nil
	}

	dirStr := name
	if dirStr == "" && className != "" {
		dirStr = className
	}

	hash := sha256.Sum256([]byte(dirStr))
	hashHex := hex.EncodeToString(hash[:])

	dirNames := []string{
		dirStr,
		hashHex,
		xmlBasename + "/" + dirStr,
		xmlBasename + "/" + hashHex,
	}

	if strings.Contains(dirStr, "/") || strings.Contains(dirStr, "\x00") {
		updated := strings.ReplaceAll(strings.ReplaceAll(dirStr, "/", "__"), "\x00", "__")
		if updated != "" {
			dirNames = append(dirNames, updated, xmlBasename+"/"+updated)
		}
	}

	if testCaseID != "" {
		dirNames = append(dirNames, testCaseID, xmlBasename+"/"+testCaseID)
	}

	return findAttachmentFiles(dirNames, attachmentsDir)
}

func findAttachmentFiles(dirNames []string, attachmentsDir string) []string {
	var files []string

	for _, dirName := range dirNames {
		basePath := filepath.Join(attachmentsDir, dirName)

		rel, relErr := filepath.Rel(attachmentsDir, basePath)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			continue
		}

		info, err := os.Stat(basePath) // #nosec G703 -- basePath validated under attachmentsDir
		if err != nil {
			continue
		}

		if info.IsDir() {
			entries, err := os.ReadDir(basePath)
			if err != nil {
				continue
			}

			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}

				files = append(files, filepath.Join(dirName, entry.Name()))
			}
		} else {
			files = append(files, dirName)
		}
	}

	return uniqueStrings(files)
}

func removeInvalidTestcases(node *xunit.Node, warnings *WarningList, isDocumentRoot bool) error {
	if node == nil || node.Skip {
		return nil
	}

	if node.Name.Local == xmlTestcase {
		id := xunit.AttrValue(node.Attrs, "id")

		name := xunit.AttrValue(node.Attrs, "name")
		if id == "" && name == "" {
			node.Skip = true

			warnings.Add("prepare", "Removing testcase because it has no id and name attribute")

			return nil
		}

		node.Attrs = xunit.RemoveAttr(node.Attrs, "status")
	}

	for _, child := range node.Children {
		err := removeInvalidTestcases(child, warnings, false)
		if err != nil {
			return err
		}
	}

	if isDocumentRoot && !containsValidTestcase(node) {
		return errors.New("no valid testcases found in file")
	}

	return nil
}

func containsValidTestcase(node *xunit.Node) bool {
	if node == nil || node.Skip {
		return false
	}

	if node.Name.Local == xmlTestcase {
		return true
	}

	return slices.ContainsFunc(node.Children, containsValidTestcase)
}

func hasAnyTestcase(node *xunit.Node) bool {
	return containsValidTestcase(node)
}

func normalizeTimestamps(node *xunit.Node) {
	if node == nil || node.Skip {
		return
	}

	if node.Name.Local == xmlTestsuite || node.Name.Local == xmlTestcase {
		filtered := node.Attrs[:0]
		for _, attr := range node.Attrs {
			switch attr.Name.Local {
			case "timestamp":
				if strings.TrimSpace(attr.Value) == "" {
					continue
				}
			case "time":
				attr.Value = strings.ReplaceAll(attr.Value, ",", "")
			}

			filtered = append(filtered, attr)
		}

		node.Attrs = filtered
	}

	for _, child := range node.Children {
		normalizeTimestamps(child)
	}
}

func populateFailureBodies(node *xunit.Node) {
	if node == nil || node.Skip {
		return
	}

	if node.Name.Local == xmlFailure && strings.TrimSpace(node.Chars) == "" {
		typeAttr := xunit.AttrValue(node.Attrs, "type")
		messageAttr := xunit.AttrValue(node.Attrs, "message")
		node.Chars = fmt.Sprintf("%s:\n\n%s", typeAttr, messageAttr)
	}

	for _, child := range node.Children {
		populateFailureBodies(child)
	}
}

func truncateProperties(node *xunit.Node, warnings *WarningList) {
	truncatePropertyNode(node, warnings)

	for _, child := range node.Children {
		truncateProperties(child, warnings)
	}
}

func truncatePropertyNode(node *xunit.Node, warnings *WarningList) {
	if node == nil || node.Skip || node.Name.Local != xmlProperty {
		return
	}

	name := strings.TrimSpace(xunit.AttrValue(node.Attrs, "name"))
	value := strings.TrimSpace(xunit.AttrValue(node.Attrs, "value"))

	if skipPropertyWithoutName(node, warnings, name) {
		return
	}

	if skipPropertyWithoutValue(node, warnings, name, value) {
		return
	}

	if skipPropertyWithLongName(node, warnings, name) {
		return
	}

	skipPropertyWithLongValue(node, warnings, name, value)
}

func skipPropertyWithoutName(node *xunit.Node, warnings *WarningList, name string) bool {
	if name != "" {
		return false
	}

	node.Skip = true

	warnings.Add("prepare", "Ignoring <property> with no/empty name attribute")

	return true
}

func skipPropertyWithoutValue(node *xunit.Node, warnings *WarningList, name, value string) bool {
	if value != "" {
		return false
	}

	node.Skip = true

	if _, ok := doNotWarnEmptyValue[name]; !ok {
		warnings.Add(
			"prepare",
			fmt.Sprintf("Ignoring <property> (%s) with no/empty value attribute", name),
		)
	}

	return true
}

func skipPropertyWithLongName(node *xunit.Node, warnings *WarningList, name string) bool {
	if len(name) <= 128 {
		return false
	}

	node.Skip = true

	warnings.Add(
		"prepare",
		fmt.Sprintf(
			"Ignoring <property> (%s) with name too long (%d)",
			abbreviate(name, 50),
			len(name),
		),
	)

	return true
}

func skipPropertyWithLongValue(node *xunit.Node, warnings *WarningList, name, value string) {
	if len(value) <= 128 {
		return
	}

	node.Skip = true

	if _, ok := doNotWarnLongValue[name]; !ok {
		warnings.Add(
			"prepare",
			fmt.Sprintf(
				"Ignoring <property> (%s) with value too long (%d)",
				name,
				len(value),
			),
		)
	}
}

func applyConcatChar(node *xunit.Node, concatChar string) {
	if node == nil || node.Skip {
		return
	}

	if node.Name.Local == xmlTestcase {
		className := xunit.AttrValue(node.Attrs, "classname")

		name := xunit.AttrValue(node.Attrs, "name")
		if className != "" {
			xunit.SetAttr(node, "name", className+concatChar+name)
		}
	}

	for _, child := range node.Children {
		applyConcatChar(child, concatChar)
	}
}

func removeEmptySuites(node *xunit.Node) bool {
	if node == nil || node.Skip {
		return false
	}

	changed := false

	for _, child := range node.Children {
		if removeEmptySuites(child) {
			changed = true
		}
	}

	if node.Name.Local == xmlTestsuite && !suiteHasTestcase(node) {
		node.Skip = true

		return true
	}

	return changed
}

func suiteHasTestcase(node *xunit.Node) bool {
	for _, child := range node.Children {
		if child.Skip {
			continue
		}

		if child.Name.Local == xmlTestcase {
			return true
		}

		if child.Name.Local == xmlTestsuite && suiteHasTestcase(child) {
			return true
		}
	}

	return false
}

func abbreviate(value string, maxLen int) string {
	if len(value) <= maxLen {
		return value
	}

	return value[:maxLen] + "..."
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))

	for _, value := range values {
		if value == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)
	}

	return out
}

// CopyAndPrepareResults copies XML inputs into workDir/results, transforms them, and builds the index.
func CopyAndPrepareResults(
	workDir string,
	sourcePaths []string,
	attachmentsDir string,
	meta *Metadata,
) ([]string, TestCaseIndex, WarningList, error) {
	sources := make([]SourceInput, 0, len(sourcePaths))
	for _, sourcePath := range sourcePaths {
		// #nosec G304 -- sourcePath is a CLI-supplied results file
		content, err := os.ReadFile(sourcePath)
		if err != nil {
			return nil, TestCaseIndex{}, nil, fmt.Errorf("reading %q: %w", sourcePath, err)
		}

		sources = append(sources, SourceInput{
			OriginalPath: sourcePath,
			WorkdirName:  filepath.Base(sourcePath),
			Content:      content,
		})
	}

	return CopyAndPrepareSourceInputs(workDir, sources, attachmentsDir, meta)
}

// CopyAndPrepareSourceInputs copies prepared source inputs into workDir/results,
// applies Layer 1 transforms, and builds the index.
func CopyAndPrepareSourceInputs(
	workDir string,
	sources []SourceInput,
	attachmentsDir string,
	meta *Metadata,
) ([]string, TestCaseIndex, WarningList, error) {
	resultsDir := filepath.Join(workDir, "results")

	err := os.MkdirAll(resultsDir, 0o750)
	if err != nil {
		return nil, TestCaseIndex{}, nil, fmt.Errorf("creating results dir: %w", err)
	}

	var warnings WarningList

	processedPaths := make([]string, 0, len(sources))

	for _, source := range sources {
		output, fileWarnings, err := PrepareXML(source.Content, meta.TestCaseConcatChar)

		warnings = append(warnings, fileWarnings...)
		if err != nil {
			warnings.Add(
				"prepare",
				fmt.Sprintf(
					"Ignoring file %s: %s",
					filepath.Base(source.OriginalPath),
					err.Error(),
				),
			)

			continue
		}

		destPath := filepath.Join(resultsDir, source.WorkdirName)

		err = os.WriteFile(destPath, output, 0o600)
		if err != nil {
			return nil, TestCaseIndex{}, warnings, fmt.Errorf("writing prepared XML: %w", err)
		}

		processedPaths = append(processedPaths, destPath)
	}

	if len(processedPaths) == 0 {
		return nil, TestCaseIndex{}, warnings, errors.New("no valid XML files after preparation")
	}

	index, indexWarnings, err := BuildTestCaseIndex(processedPaths, attachmentsDir, meta)
	warnings = append(warnings, indexWarnings...)

	return processedPaths, index, warnings, err
}
