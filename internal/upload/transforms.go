package upload

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
	rpusend "github.com/klaskosk/report-portal-uploader/internal/send"
	"github.com/klaskosk/report-portal-uploader/internal/xunit"
)

const (
	xmlTestsuite = "testsuite"
	xmlTestcase  = "testcase"
)

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
	doc, err := xunit.ParseDocument(input)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing document: %w", err)
	}

	var warnings rpusend.WarningList

	sanitizeSystemOutErr(doc)
	applyLaneLabeling(doc, opts.Lane, opts.LaneProperty)
	moveAttributesToProperties(doc)
	normalizeTimeAttributes(doc, opts.SourcePath, &warnings)

	output, err := xunit.EncodeDocument(doc)
	if err != nil {
		return nil, warnings, fmt.Errorf("encoding document: %w", err)
	}

	return output, warnings, nil
}

// WorkdirName returns a unique internal filename for a source file record.
func WorkdirName(source SourceFile) string {
	shortID := strings.Split(uuid.NewString(), "-")[0]

	return fmt.Sprintf("%d_%s_%s", source.Index, shortID, filepath.Base(source.Path))
}

func sanitizeSystemOutErr(node *xunit.Node) {
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

func applyLaneLabeling(node *xunit.Node, lane, laneProperty string) {
	if lane == "" {
		return
	}

	applyLaneLabelingWalk(node, lane, laneProperty)
}

func applyLaneLabelingWalk(node *xunit.Node, lane, laneProperty string) {
	if node == nil || node.Skip {
		return
	}

	if node.Name.Local == xmlTestsuite {
		name := xunit.AttrValue(node.Attrs, "name")

		prefix := "[" + lane + "] "
		if name != "" && !strings.HasPrefix(name, prefix) {
			xunit.SetAttr(node, "name", prefix+name)
		}

		xunit.EnsureProperty(node, laneProperty, lane)
	}

	for _, child := range node.Children {
		applyLaneLabelingWalk(child, lane, laneProperty)
	}
}

func moveAttributesToProperties(node *xunit.Node) {
	if node == nil || node.Skip {
		return
	}

	moveAttributesToPropertiesWalk(node)

	for _, child := range node.Children {
		moveAttributesToProperties(child)
	}
}

func moveAttributesToPropertiesWalk(node *xunit.Node) {
	switch node.Name.Local {
	case xmlTestcase:
		if value := xunit.AttrValue(node.Attrs, "status"); value != "" {
			xunit.EnsureProperty(node, "status", value)
			node.Attrs = xunit.RemoveAttr(node.Attrs, "status")
		}
	case xmlTestsuite, "testsuites":
		if value := xunit.AttrValue(node.Attrs, "disabled"); value != "" {
			xunit.EnsureProperty(node, "disabled", value)
			node.Attrs = xunit.RemoveAttr(node.Attrs, "disabled")
		}
	}

	hoistResultChildren(node)
}

func hoistResultChildren(node *xunit.Node) {
	if node == nil {
		return
	}

	remaining := make([]*xunit.Node, 0, len(node.Children))
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

func hoistResultChild(parent, child *xunit.Node) bool {
	switch child.Name.Local {
	case "results_directory", "results_summary":
		value := resultChildValue(child)
		if value != "" {
			xunit.EnsureProperty(parent, child.Name.Local, value)
		}

		return true
	default:
		return false
	}
}

func resultChildValue(child *xunit.Node) string {
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

func normalizeTimeAttributes(node *xunit.Node, sourcePath string, warnings *rpusend.WarningList) {
	if node == nil || node.Skip {
		return
	}

	if node.Name.Local == xmlTestsuite || node.Name.Local == xmlTestcase {
		timeValue := xunit.AttrValue(node.Attrs, "time")
		if timeValue == "" {
			return
		}

		_, err := strconv.ParseFloat(strings.ReplaceAll(timeValue, ",", ""), 64)
		if err != nil {
			xunit.SetAttr(node, "time", "0.0")
			elementPath := xunit.ElementPath(node)
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
