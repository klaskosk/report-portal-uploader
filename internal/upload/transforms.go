package upload

import (
	"bytes"
	"encoding/xml"
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

type xunitNode struct {
	Name     xml.Name
	Attrs    []xml.Attr
	Children []*xunitNode
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
func ApplyLayer2Transforms(input []byte, opts TransformOptions) ([]byte, rpusend.WarningList, error) {
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

func sanitizeSystemOutErr(node *xunitNode) {
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

func applyLaneLabeling(node *xunitNode, lane, laneProperty string) {
	if lane == "" {
		return
	}

	applyLaneLabelingWalk(node, lane, laneProperty)
}

func applyLaneLabelingWalk(node *xunitNode, lane, laneProperty string) {
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

func moveAttributesToProperties(node *xunitNode) {
	if node == nil || node.Skip {
		return
	}

	moveAttributesToPropertiesWalk(node)

	for _, child := range node.Children {
		moveAttributesToProperties(child)
	}
}

func moveAttributesToPropertiesWalk(node *xunitNode) {
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

func hoistResultChildren(node *xunitNode) {
	if node == nil {
		return
	}

	remaining := make([]*xunitNode, 0, len(node.Children))
	for _, child := range node.Children {
		if child == nil || child.Skip {
			continue
		}

		switch child.Name.Local {
		case "results_directory", "results_summary":
			value := strings.TrimSpace(child.Chars)
			if value == "" {
				for _, grandchild := range child.Children {
					if grandchild != nil && !grandchild.Skip {
						value = strings.TrimSpace(grandchild.Chars)
						if value != "" {
							break
						}
					}
				}
			}

			if value != "" {
				ensureProperty(node, child.Name.Local, value)
			}

			continue
		}

		remaining = append(remaining, child)
	}

	node.Children = remaining
}

func normalizeTimeAttributes(node *xunitNode, sourcePath string, warnings *rpusend.WarningList) {
	if node == nil || node.Skip {
		return
	}

	if node.Name.Local == xmlTestsuite || node.Name.Local == xmlTestcase {
		timeValue := attrValue(node.Attrs, "time")
		if timeValue == "" {
			return
		}

		if _, err := strconv.ParseFloat(strings.ReplaceAll(timeValue, ",", ""), 64); err != nil {
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

func elementPathForNode(node *xunitNode) string {
	if node == nil {
		return ""
	}

	name := attrValue(node.Attrs, "name")
	if name == "" {
		return node.Name.Local
	}

	return node.Name.Local + "[@name=" + name + "]"
}

func ensureProperty(node *xunitNode, name, value string) {
	properties := findOrCreateProperties(node)
	for _, child := range properties.Children {
		if child != nil && !child.Skip && child.Name.Local == xmlProperty {
			if attrValue(child.Attrs, "name") == name {
				setAttr(child, "value", value)

				return
			}
		}
	}

	properties.Children = append(properties.Children, &xunitNode{
		Name: xml.Name{Local: xmlProperty},
		Attrs: []xml.Attr{
			{Name: xml.Name{Local: "name"}, Value: name},
			{Name: xml.Name{Local: "value"}, Value: value},
		},
	})
}

func findOrCreateProperties(node *xunitNode) *xunitNode {
	for _, child := range node.Children {
		if child != nil && !child.Skip && child.Name.Local == "properties" {
			return child
		}
	}

	properties := &xunitNode{Name: xml.Name{Local: "properties"}}
	node.Children = append(node.Children, properties)

	return properties
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
