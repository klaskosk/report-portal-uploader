package send

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	xmlTestsuite = "testsuite"
	xmlTestcase  = "testcase"
	xmlProperty  = "property"
	xmlFailure   = "failure"
)

var (
	doNotWarnEmptyValue = map[string]struct{}{
		"line.separator": {},
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

type xunitNode struct {
	Name     xml.Name
	Attrs    []xml.Attr
	Children []*xunitNode
	Chars    string
	Skip     bool
}

// PrepareXML transforms source XML and returns output bytes plus warnings.
func PrepareXML(input []byte, concatChar string) ([]byte, WarningList, error) {
	doc, err := parseXUnitDocument(input)
	if err != nil {
		return nil, nil, err
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

	removed := removeEmptySuites(doc)
	if removed {
		removed = removeEmptySuites(doc)
	}

	if !hasAnyTestcase(doc) {
		return nil, warnings, fmt.Errorf("no valid testcases found in XML")
	}

	output, err := encodeXUnitDocument(doc)
	if err != nil {
		return nil, warnings, err
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
	node *xunitNode,
	item *TestItem,
	xmlBasename, attachmentsDir string,
	meta *Metadata,
) {
	var walk func(*xunitNode)
	walk = func(current *xunitNode) {
		if current == nil || current.Skip {
			return
		}

		if current.Name.Local == xmlTestsuite || current.Name.Local == xmlTestcase {
			return
		}

		if current.Name.Local == xmlProperty {
			propName := attrValue(current.Attrs, "name")
			propValue := attrValue(current.Attrs, "value")

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
	doc, err := parseXUnitDocument(content)
	if err != nil {
		return nil, err
	}

	if indexes == nil {
		indexes = map[string]int{}
	}

	var buildItem func(node *xunitNode, parentName string) *TestItem
	buildItem = func(node *xunitNode, parentName string) *TestItem {
		if node == nil || node.Skip {
			return nil
		}

		switch node.Name.Local {
		case xmlTestsuite, xmlTestcase:
		default:
			return nil
		}

		name := attrValue(node.Attrs, "name")
		className := attrValue(node.Attrs, "classname")
		testCaseID := attrValue(node.Attrs, "id")

		itemType := testItemTypeSuite
		childParentName := parentName
		if node.Name.Local == xmlTestcase {
			itemType = testItemTypeCase
		} else {
			childParentName = name
		}

		uniqueName := name
		if parentName != "" {
			uniqueName = parentName + name
		}

		index := indexes[uniqueName]
		indexes[uniqueName] = index + 1

		item := &TestItem{
			XMLName:     xmlBasename,
			Name:        name,
			ClassName:   className,
			ID:          testCaseID,
			Type:        itemType,
			Index:       index,
			Attachments: resolveAttachmentPaths(name, className, testCaseID, xmlBasename, attachmentsDir),
		}

		for _, child := range node.Children {
			switch child.Name.Local {
			case xmlTestsuite, xmlTestcase:
				if built := buildItem(child, childParentName); built != nil {
					item.Children = append(item.Children, built)
				}
			}
		}

		collectItemProperties(node, item, xmlBasename, attachmentsDir, meta)

		if node.Name.Local == xmlTestcase {
			dirAttachments := resolveAttachmentPaths(name, className, testCaseID, xmlBasename, attachmentsDir)
			item.Attachments = uniqueStrings(append(item.Attachments, dirAttachments...))
		}

		return item
	}

	var roots []*TestItem
	for _, child := range doc.Children {
		if built := buildItem(child, ""); built != nil {
			roots = append(roots, built)
		}
	}

	return roots, nil
}

func resolveAttachmentPaths(name, className, testCaseID, xmlBasename, attachmentsDir string) []string {
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
		info, err := os.Stat(basePath)
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

func parseXUnitDocument(input []byte) (*xunitNode, error) {
	decoder := xml.NewDecoder(bytes.NewReader(input))

	var root *xunitNode
	var stack []*xunitNode

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("parsing XML: %w", err)
		}

		switch element := token.(type) {
		case xml.StartElement:
			node := &xunitNode{Name: element.Name, Attrs: append([]xml.Attr{}, element.Attr...)}
			if len(stack) == 0 {
				root = node
			} else {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, node)
			}

			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].Chars += string(element)
			}
		}
	}

	if root == nil {
		return nil, fmt.Errorf("empty XML document")
	}

	return root, nil
}

func encodeXUnitDocument(root *xunitNode) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(xml.Header)

	encoder := xml.NewEncoder(&buf)
	encoder.Indent("", "  ")

	err := writeNode(encoder, root)
	if err != nil {
		return nil, err
	}

	err = encoder.Flush()
	if err != nil {
		return nil, fmt.Errorf("flushing XML encoder: %w", err)
	}

	return buf.Bytes(), nil
}

func writeNode(encoder *xml.Encoder, node *xunitNode) error {
	if node.Skip {
		return nil
	}

	start := xml.StartElement{Name: node.Name, Attr: node.Attrs}
	err := encoder.EncodeToken(start)
	if err != nil {
		return err
	}

	chars := strings.TrimSpace(node.Chars)
	if chars != "" {
		err = encoder.EncodeToken(xml.CharData(chars))
		if err != nil {
			return err
		}
	}

	for _, child := range node.Children {
		err = writeNode(encoder, child)
		if err != nil {
			return err
		}
	}

	return encoder.EncodeToken(start.End())
}

func removeInvalidTestcases(node *xunitNode, warnings *WarningList, isDocumentRoot bool) error {
	if node == nil || node.Skip {
		return nil
	}

	if node.Name.Local == xmlTestcase {
		id := attrValue(node.Attrs, "id")
		name := attrValue(node.Attrs, "name")
		if id == "" && name == "" {
			node.Skip = true
			warnings.Add("prepare", "Removing testcase because it has no id and name attribute")

			return nil
		}

		node.Attrs = removeAttr(node.Attrs, "status")
	}

	for _, child := range node.Children {
		err := removeInvalidTestcases(child, warnings, false)
		if err != nil {
			return err
		}
	}

	if isDocumentRoot && !containsValidTestcase(node) {
		return fmt.Errorf("no valid testcases found in file")
	}

	return nil
}

func containsValidTestcase(node *xunitNode) bool {
	if node == nil || node.Skip {
		return false
	}

	if node.Name.Local == xmlTestcase {
		return true
	}

	for _, child := range node.Children {
		if containsValidTestcase(child) {
			return true
		}
	}

	return false
}

func hasAnyTestcase(node *xunitNode) bool {
	return containsValidTestcase(node)
}

func isContainerRoot(node *xunitNode) bool {
	switch node.Name.Local {
	case "testsuites", xmlTestsuite:
		return true
	default:
		return false
	}
}

func normalizeTimestamps(node *xunitNode) {
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

func populateFailureBodies(node *xunitNode) {
	if node == nil || node.Skip {
		return
	}

	if node.Name.Local == xmlFailure && strings.TrimSpace(node.Chars) == "" {
		typeAttr := attrValue(node.Attrs, "type")
		messageAttr := attrValue(node.Attrs, "message")
		node.Chars = fmt.Sprintf("%s:\n\n%s", typeAttr, messageAttr)
	}

	for _, child := range node.Children {
		populateFailureBodies(child)
	}
}

func truncateProperties(node *xunitNode, warnings *WarningList) {
	truncatePropertyNode(node, warnings)

	for _, child := range node.Children {
		truncateProperties(child, warnings)
	}
}

func truncatePropertyNode(node *xunitNode, warnings *WarningList) {
	if node == nil || node.Skip {
		return
	}

	if node.Name.Local == xmlProperty {
		name := strings.TrimSpace(attrValue(node.Attrs, "name"))
		value := strings.TrimSpace(attrValue(node.Attrs, "value"))

		if name == "" {
			node.Skip = true
			warnings.Add("prepare", "Ignoring <property> with no/empty name attribute")

			return
		}

		if value == "" {
			node.Skip = true
			if _, ok := doNotWarnEmptyValue[name]; !ok {
				warnings.Add("prepare", fmt.Sprintf("Ignoring <property> (%s) with no/empty value attribute", name))
			}

			return
		}

		if len(name) > 128 {
			node.Skip = true
			warnings.Add("prepare", fmt.Sprintf("Ignoring <property> (%s) with name too long (%d)", abbreviate(name, 50), len(name)))

			return
		}

		if len(value) > 128 {
			node.Skip = true
			if _, ok := doNotWarnLongValue[name]; !ok {
				warnings.Add("prepare", fmt.Sprintf("Ignoring <property> (%s) with value too long (%d)", name, len(value)))
			}

			return
		}
	}
}

func applyConcatChar(node *xunitNode, concatChar string) {
	if node == nil || node.Skip {
		return
	}

	if node.Name.Local == xmlTestcase {
		className := attrValue(node.Attrs, "classname")
		name := attrValue(node.Attrs, "name")
		if className != "" {
			setAttr(node, "name", className+concatChar+name)
		}
	}

	for _, child := range node.Children {
		applyConcatChar(child, concatChar)
	}
}

func removeEmptySuites(node *xunitNode) bool {
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

func suiteHasTestcase(node *xunitNode) bool {
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

func attrValue(attrs []xml.Attr, name string) string {
	for _, attr := range attrs {
		if attr.Name.Local == name {
			return attr.Value
		}
	}

	return ""
}

func setAttr(node *xunitNode, name, value string) {
	for i, attr := range node.Attrs {
		if attr.Name.Local == name {
			node.Attrs[i].Value = value

			return
		}
	}

	node.Attrs = append(node.Attrs, xml.Attr{Name: xml.Name{Local: name}, Value: value})
}

func removeAttr(attrs []xml.Attr, name string) []xml.Attr {
	out := make([]xml.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if attr.Name.Local != name {
			out = append(out, attr)
		}
	}

	return out
}

func abbreviate(value string, max int) string {
	if len(value) <= max {
		return value
	}

	return value[:max] + "..."
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
	resultsDir := filepath.Join(workDir, "results")
	err := os.MkdirAll(resultsDir, 0o755)
	if err != nil {
		return nil, TestCaseIndex{}, nil, fmt.Errorf("creating results dir: %w", err)
	}

	var warnings WarningList
	processedPaths := make([]string, 0, len(sourcePaths))

	for _, sourcePath := range sourcePaths {
		input, err := os.ReadFile(sourcePath)
		if err != nil {
			return nil, TestCaseIndex{}, warnings, fmt.Errorf("reading %q: %w", sourcePath, err)
		}

		output, fileWarnings, err := PrepareXML(input, meta.TestCaseConcatChar)
		warnings = append(warnings, fileWarnings...)
		if err != nil {
			warnings.Add("prepare", fmt.Sprintf("Ignoring file %s: %s", filepath.Base(sourcePath), err.Error()))

			continue
		}

		destPath := filepath.Join(resultsDir, filepath.Base(sourcePath))
		err = os.WriteFile(destPath, output, 0o644)
		if err != nil {
			return nil, TestCaseIndex{}, warnings, fmt.Errorf("writing prepared XML: %w", err)
		}

		processedPaths = append(processedPaths, destPath)
	}

	if len(processedPaths) == 0 {
		return nil, TestCaseIndex{}, warnings, fmt.Errorf("no valid XML files after preparation")
	}

	index, indexWarnings, err := BuildTestCaseIndex(processedPaths, attachmentsDir, meta)
	warnings = append(warnings, indexWarnings...)

	return processedPaths, index, warnings, err
}
