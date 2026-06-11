package upload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShouldExclude_defaultPattern(t *testing.T) {
	t.Parallel()

	require.True(t, ShouldExclude("foo_report_testrun_bar.xml", []string{defaultExcludePattern}), "expected exclude match")
	require.False(t, ShouldExclude("suite.xml", []string{defaultExcludePattern}), "expected non-excluded file")
}

func TestApplyLayer2Transforms_laneLabeling(t *testing.T) {
	t.Parallel()

	input := mustReadFixture(t, "lane_input.xml")
	output, warnings, err := ApplyLayer2Transforms(input, TransformOptions{
		Lane:         "gm",
		LaneProperty: "ci-lane",
		SourcePath:   "lane_input.xml",
	})
	require.NoError(t, err, "expected lane transform")
	require.Empty(t, warnings, "expected no warnings")

	text := string(output)
	require.Contains(t, text, `[gm] Suite`, "expected lane prefix on testsuite name")
	require.Contains(t, text, `name="ci-lane"`, "expected lane property name")
	require.Contains(t, text, `value="gm"`, "expected lane property value")
}

func TestApplyLayer2Transforms_emptyLane(t *testing.T) {
	t.Parallel()

	input := mustReadFixture(t, "lane_input.xml")
	output, _, err := ApplyLayer2Transforms(input, TransformOptions{SourcePath: "lane_input.xml"})
	require.NoError(t, err, "expected empty lane transform")

	text := string(output)
	require.Contains(t, text, `name="Suite"`, "expected original suite name")
	require.NotContains(t, text, "ci-lane", "expected no lane property")
}

func TestApplyLayer2Transforms_customLaneProperty(t *testing.T) {
	t.Parallel()

	input := mustReadFixture(t, "lane_input.xml")
	output, _, err := ApplyLayer2Transforms(input, TransformOptions{
		Lane:         "ntpfailover",
		LaneProperty: "suite-group",
		SourcePath:   "lane_input.xml",
	})
	require.NoError(t, err, "expected custom lane property transform")
	require.Contains(t, string(output), `name="suite-group"`, "expected custom property name")
}

func TestApplyLayer2Transforms_sanitizeSystemOut(t *testing.T) {
	t.Parallel()

	input := mustReadFixture(t, "sanitize_input.xml")
	output, _, err := ApplyLayer2Transforms(input, TransformOptions{SourcePath: "sanitize_input.xml"})
	require.NoError(t, err, "expected sanitize transform")

	text := string(output)
	require.NotContains(t, text, "&lt;", "expected entity decoded")
	require.NotContains(t, text, "&gt;", "expected entity decoded")
	require.NotContains(t, text, "<tag>", "expected angle brackets stripped from body")
}

func TestApplyLayer2Transforms_attrToProperty(t *testing.T) {
	t.Parallel()

	input := mustReadFixture(t, "attr_input.xml")
	output, _, err := ApplyLayer2Transforms(input, TransformOptions{SourcePath: "attr_input.xml"})
	require.NoError(t, err, "expected attr to property transform")

	text := string(output)
	require.Contains(t, text, `name="status"`, "expected status property")
	require.Contains(t, text, `value="failed"`, "expected status value")
	require.Contains(t, text, `name="disabled"`, "expected disabled property")
	require.Contains(t, text, `name="results_directory"`, "expected hoisted results_directory")
	require.Contains(t, text, `name="results_summary"`, "expected hoisted results_summary")
	require.NotContains(t, text, `status="failed"`, "expected status attribute removed")
}

func TestApplyLayer2Transforms_invalidTime(t *testing.T) {
	t.Parallel()

	input := mustReadFixture(t, "simple.xml")
	_, warnings, err := ApplyLayer2Transforms(input, TransformOptions{SourcePath: "simple.xml"})
	require.NoError(t, err, "expected time normalization")
	require.NotEmpty(t, warnings, "expected invalid time warning")
	require.Contains(t, warnings.Messages()[0], "normalized to 0.0", "expected normalization warning")
}

func TestApplyLayer2Transforms_combinedChain(t *testing.T) {
	t.Parallel()

	input := []byte(`<testsuites>
  <testsuite name="Suite" time="bad">
    <testcase name="N" status="passed">
      <system-out>&lt;x&gt;</system-out>
    </testcase>
  </testsuite>
</testsuites>`)

	output, warnings, err := ApplyLayer2Transforms(input, TransformOptions{
		Lane:         "gm",
		LaneProperty: "ci-lane",
		SourcePath:   "combined.xml",
	})
	require.NoError(t, err, "expected combined transform chain")
	require.NotEmpty(t, warnings, "expected time warning in combined chain")

	text := string(output)
	require.Contains(t, text, `[gm] Suite`, "expected lane prefix")
	require.Contains(t, text, `name="status"`, "expected status property")
	require.Contains(t, text, `time="0.0"`, "expected normalized time")
	require.NotContains(t, text, "&lt;", "expected sanitized system-out")
}

func TestWorkdirName_uniquePerRecord(t *testing.T) {
	t.Parallel()

	nameA := WorkdirName(SourceFile{Index: 0, Path: "/tmp/a.xml"})
	nameB := WorkdirName(SourceFile{Index: 1, Path: "/tmp/a.xml"})
	require.NotEqual(t, nameA, nameB, "expected unique workdir names for duplicate basenames")
	require.True(t, strings.HasPrefix(nameA, "0_"), "expected source index prefix")
	require.True(t, strings.HasSuffix(nameA, "_a.xml"), "expected original basename suffix")
}

func mustReadFixture(t *testing.T, name string) []byte {
	t.Helper()

	path := filepath.Join("..", "..", "test", "testdata", "upload", name)
	data, err := os.ReadFile(path)
	require.NoError(t, err, "expected fixture %s", name)

	return data
}
