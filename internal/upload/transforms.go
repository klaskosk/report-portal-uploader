package upload

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
	rpusend "github.com/klaskosk/report-portal-uploader/internal/send"
)

const (
	xmlTestsuite = "testsuite"
	xmlTestcase  = "testcase"
	xmlProperty  = "property"
)

type xUnitNode struct {
	Name     xml.Name
	Attrs    []xml.Attr
	Children []*xUnitNode
	Chars    string
	Skip     bool
}

// TransformOptions configures the Layer 2 transform chain for one source file.
type TransformOptions struct {
	Lane            string
	LaneProperty    string
	ExcludePatterns []string
	SourcePath      string
}

// ShouldExclude reports whether basename matches any exclude pattern.
func ShouldExclude(basename string, patterns []string) bool {
	for _, pattern := range patterns {
		matched, err := filepath.Match(pattern, basename)
		if err == nil && matched {
			return true
		}
	}

	return false
}

// ApplyLayer2Transforms runs the Layer 2 XML transform chain on input bytes.
func ApplyLayer2Transforms(
	input []byte,
	opts TransformOptions,
) ([]byte, rpusend.WarningList, error) {
	doc, err := parseXUnitDocument(input)
	if err != nil {
		return nil, nil, err
	}

	var warnings rpusend.WarningList

	sanitizeSystemOutErr(doc)
	applyLaneLabeling(doc, opts.Lane, opts.LaneProperty)
	moveAttributesToProperties(doc)
	normalizeTimeAttributes(doc, opts.SourcePath, &warnings)

	output, err := encodeXUnitDocument(doc)
	if err != nil {
		return nil, warnings, err
	}

	return output, warnings, nil
}

// WorkdirName returns a unique internal filename for a source file record.
func WorkdirName(source SourceFile) string {
	shortID := strings.Split(uuid.NewString(), "-")[0]

	return fmt.Sprintf("%d_%s_%s", source.Index, shortID, filepath.Base(source.Path))
}

func sanitizeSystemOutErr(node *xUnitNode) {
	if node == nil || node.Skip {
		return
	}

	if node.Name.Local == "system-out" || node.Name.Local == "system-err" {
		node.Chars = sanitizeSystemText(node.Chars)
	}

	for _, child := range node.Children {
		sanitizeSystemOutErr(child)
	}
}

func sanitizeSystemText(text string) string {
	replacer := strings.NewReplacer(
		"&gt;", ">",
		"&lt;", "<",
		"&#xA;", "\n",
		"&#xa;", "\n",
	)
	text = replacer.Replace(text)

	for {
		updated := strings.ReplaceAll(text, "<<<<", "<<")

		updated = strings.ReplaceAll(updated, ">>>>", ">>")
		if updated == text {
			break
		}

		text = updated
	}

	var builder strings.Builder

	for _, r := range text {
		if r == '<' || r == '>' {
			continue
		}

		builder.WriteRune(r)
	}

	return builder.String()
}

func applyLaneLabeling(node *xUnitNode, lane, laneProperty string) {
	if lane == "" {
		return
	}

	applyLaneLabelingWalk(node, lane, laneProperty)
}

func applyLaneLabelingWalk(node *xUnitNode, lane, laneProperty string) {
	if node == nil || node.Skip {
		return
	}

	if node.Name.Local == xmlTestsuite {
		name := attrValue(node.Attrs, "name")

		prefix := "[" + lane + "] "
		if name != "" && !strings.HasPrefix(name, prefix) {
			setAttr(node, "name", prefix+name)
		}

		ensureProperty(node, laneProperty, lane)
	}

	for _, child := range node.Children {
		applyLaneLabelingWalk(child, lane, laneProperty)
	}
}

func moveAttributesToProperties(node *xUnitNode) {
	if node == nil || node.Skip {
		return
	}

	moveAttributesToPropertiesWalk(node)

	for _, child := range node.Children {
		moveAttributesToProperties(child)
	}
}

func moveAttributesToPropertiesWalk(node *xUnitNode) {
	switch node.Name.Local {
	case xmlTestcase:
		if value := attrValue(node.Attrs, "status"); value != "" {
			ensureProperty(node, "status", value)
			node.Attrs = removeAttr(node.Attrs, "status")
		}
	case xmlTestsuite, "testsuites":
		if value := attrValue(node.Attrs, "disabled"); value != "" {
			ensureProperty(node, "disabled", value)
			node.Attrs = removeAttr(node.Attrs, "disabled")
		}
	}

	hoistResultChildren(node)
}

func hoistResultChildren(node *xUnitNode) {
	if node == nil {
		return
	}

	remaining := make([]*xUnitNode, 0, len(node.Children))
	for _, child := range node.Children {
		if child == nil || child.Skip {
			continue
		}

		if hoistResultChild(node, child) {
			continue
		}

		remaining = append(remaining, child)
	}

	node.Children = remaining
}

func hoistResultChild(parent, child *xUnitNode) bool {
	switch child.Name.Local {
	case "results_directory", "results_summary":
		value := resultChildValue(child)
		if value != "" {
			ensureProperty(parent, child.Name.Local, value)
		}

		return true
	default:
		return false
	}
}

func resultChildValue(child *xUnitNode) string {
	value := strings.TrimSpace(child.Chars)
	if value != "" {
		return value
	}

	for _, grandchild := range child.Children {
		if grandchild == nil || grandchild.Skip {
			continue
		}

		value = strings.TrimSpace(grandchild.Chars)
		if value != "" {
			return value
		}
	}

	return ""
}

func normalizeTimeAttributes(node *xUnitNode, sourcePath string, warnings *rpusend.WarningList) {
	if node == nil || node.Skip {
		return
	}

	if node.Name.Local == xmlTestsuite || node.Name.Local == xmlTestcase {
		timeValue := attrValue(node.Attrs, "time")
		if timeValue == "" {
			return
		}

		_, err := strconv.ParseFloat(strings.ReplaceAll(timeValue, ",", ""), 64)
		if err != nil {
			setAttr(node, "time", "0.0")
			elementPath := elementPathForNode(node)
			warnings.Add(
				"transform",
				fmt.Sprintf(
					"Invalid time attribute %q in %s (%s); normalized to 0.0",
					timeValue,
					filepath.Base(sourcePath),
					elementPath,
				),
			)
		}
	}

	for _, child := range node.Children {
		normalizeTimeAttributes(child, sourcePath, warnings)
	}
}

func elementPathForNode(node *xUnitNode) string {
	if node == nil {
		return ""
	}

	name := attrValue(node.Attrs, "name")
	if name == "" {
		return node.Name.Local
	}

	return node.Name.Local + "[@name=" + name + "]"
}

func ensureProperty(node *xUnitNode, name, value string) {
	properties := findOrCreateProperties(node)
	for _, child := range properties.Children {
		if child != nil && !child.Skip && child.Name.Local == xmlProperty {
			if attrValue(child.Attrs, "name") == name {
				setAttr(child, "value", value)

				return
			}
		}
	}

	properties.Children = append(properties.Children, &xUnitNode{
		Name: xml.Name{Local: xmlProperty},
		Attrs: []xml.Attr{
			{Name: xml.Name{Local: "name"}, Value: name},
			{Name: xml.Name{Local: "value"}, Value: value},
		},
	})
}

func findOrCreateProperties(node *xUnitNode) *xUnitNode {
	for _, child := range node.Children {
		if child != nil && !child.Skip && child.Name.Local == "properties" {
			return child
		}
	}

	properties := &xUnitNode{Name: xml.Name{Local: "properties"}}
	node.Children = append(node.Children, properties)

	return properties
}

func parseXUnitDocument(input []byte) (*xUnitNode, error) {
	decoder := xml.NewDecoder(bytes.NewReader(input))

	var (
		root  *xUnitNode
		stack []*xUnitNode
	)

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("parsing XML: %w", err)
		}

		root, stack = appendXUnitToken(root, stack, token)
	}

	if root == nil {
		return nil, errors.New("empty XML document")
	}

	return root, nil
}

func appendXUnitToken(
	root *xUnitNode,
	stack []*xUnitNode,
	token xml.Token,
) (*xUnitNode, []*xUnitNode) {
	switch element := token.(type) {
	case xml.StartElement:
		node := &xUnitNode{Name: element.Name, Attrs: append([]xml.Attr{}, element.Attr...)}
		if len(stack) == 0 {
			root = node
		} else {
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, node)
		}

		return root, append(stack, node)
	case xml.EndElement:
		if len(stack) > 0 {
			stack = stack[:len(stack)-1]
		}
	case xml.CharData:
		if len(stack) > 0 {
			stack[len(stack)-1].Chars += string(element)
		}
	}

	return root, stack
}

func encodeXUnitDocument(root *xUnitNode) ([]byte, error) {
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

func writeNode(encoder *xml.Encoder, node *xUnitNode) error {
	if node.Skip {
		return nil
	}

	start := xml.StartElement{Name: node.Name, Attr: node.Attrs}

	err := encoder.EncodeToken(start)
	if err != nil {
		return fmt.Errorf("encoding XML start element: %w", err)
	}

	chars := strings.TrimSpace(node.Chars)
	if chars != "" {
		err = encoder.EncodeToken(xml.CharData(chars))
		if err != nil {
			return fmt.Errorf("encoding XML character data: %w", err)
		}
	}

	for _, child := range node.Children {
		err = writeNode(encoder, child)
		if err != nil {
			return err
		}
	}

	err = encoder.EncodeToken(start.End())
	if err != nil {
		return fmt.Errorf("encoding XML end element: %w", err)
	}

	return nil
}

func attrValue(attrs []xml.Attr, name string) string {
	for _, attr := range attrs {
		if attr.Name.Local == name {
			return attr.Value
		}
	}

	return ""
}

func setAttr(node *xUnitNode, name, value string) {
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
