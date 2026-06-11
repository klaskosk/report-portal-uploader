package send_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klaskosk/report-portal-uploader/internal/send"
	"github.com/stretchr/testify/require"
)

func TestPrepareXML_goldenTransforms(t *testing.T) {
	t.Parallel()

	input := []byte(`<testsuites>
  <testsuite name="Suite" timestamp=" " time="1,234">
    <testcase classname="C" name="N" time="0">
      <failure type="T" message="M"/>
      <property name="long-name-` + strings.Repeat("x", 140) + `" value="v"/>
      <property name="ok" value="` + strings.Repeat("y", 140) + `"/>
    </testcase>
    <testsuite name="Empty"/>
  </testsuite>
</testsuites>`)

	output, warnings, err := send.PrepareXML(input, ".")
	require.NoError(t, err, "expected XML preparation to succeed")

	text := string(output)
	require.Contains(t, text, `name="C.N"`, "expected concat char applied after truncation order")
	require.Contains(t, text, "T:\n\nM", "expected failure body populated")
	require.Contains(t, text, `time="1234"`, "expected comma removed from time attribute")
	require.NotContains(t, text, `timestamp=" "`, "expected empty timestamp removed")
	require.NotContains(t, text, "Empty", "expected empty nested suite removed")
	require.NotEmpty(t, warnings, "expected truncation warnings")
}

func TestBuildTestCaseIndex_attachmentPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	attachments := filepath.Join(dir, "attachments")
	err := os.MkdirAll(filepath.Join(attachments, "C.N"), 0o750)
	require.NoError(t, err, "expected attachment dir")
	err = os.WriteFile(filepath.Join(attachments, "C.N", "log.txt"), []byte("x"), 0o600)
	require.NoError(t, err, "expected attachment file")

	xmlPath := filepath.Join(dir, "Sample.xml")
	err = os.WriteFile(
		xmlPath,
		[]byte(
			`<testsuites><testsuite name="S"><testcase classname="C" name="N"/></testsuite></testsuites>`,
		),
		0o600,
	)
	require.NoError(t, err, "expected xml fixture")

	meta, err := send.ParseMetadataJSON([]byte(`{
		"targets": {"reportportal": {
			"config": {"hostname": "rp.example.com", "project": "demo"},
			"processing": {"property_filter": ["attachment"], "testcase_concat_char": "."}
		}}
	}`))
	require.NoError(t, err, "expected metadata parse")

	prepared, _, err := send.PrepareXML(mustRead(t, xmlPath), ".")
	require.NoError(t, err, "expected prepare")
	err = os.WriteFile(xmlPath, prepared, 0o600)
	require.NoError(t, err, "expected prepared xml write")

	index, _, err := send.BuildTestCaseIndex([]string{xmlPath}, attachments, meta)
	require.NoError(t, err, "expected index build")

	steps := index.Steps()
	require.Len(t, steps, 1, "expected one testcase")
	require.Contains(
		t,
		steps[0].Attachments,
		filepath.Join("C.N", "log.txt"),
		"expected attachment path",
	)
	require.Equal(t, "C.N", steps[0].Name, "expected concat name in index")
}

func TestBuildTestCaseIndex_multiXML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	meta, err := send.ParseMetadataJSON([]byte(`{
		"targets": {"reportportal": {
			"config": {"hostname": "rp.example.com", "project": "demo"},
			"processing": {}
		}}
	}`))
	require.NoError(t, err, "expected metadata parse")

	paths := make([]string, 2)

	for i, name := range []string{"a.xml", "b.xml"} {
		path := filepath.Join(dir, name)
		err = os.WriteFile(
			path,
			[]byte(
				`<testsuites><testsuite name="S"><testcase name="same"/></testsuite></testsuites>`,
			),
			0o600,
		)
		require.NoError(t, err, "expected xml write")

		paths[i] = path
	}

	index, _, err := send.BuildTestCaseIndex(paths, "", meta)
	require.NoError(t, err, "expected index build")
	require.Len(t, index.Roots, 2, "expected two root suites")
	require.Equal(t, "S", index.Roots[0].Name, "expected first root suite name")
	require.Equal(t, "S", index.Roots[1].Name, "expected second root suite name")
	require.Equal(t, 0, index.Roots[0].Index, "expected first duplicate suite index")
	require.Equal(t, 1, index.Roots[1].Index, "expected second duplicate suite index")
	require.Equal(t, "a", index.Roots[0].XMLName, "expected first xml basename")
	require.Equal(t, "b", index.Roots[1].XMLName, "expected second xml basename")
}

func TestBuildTestCaseIndex_suiteDoesNotInheritTestcaseProperties(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "example.xml")
	err := os.WriteFile(xmlPath, []byte(`<testsuites><testsuite name="Example Ginkgo Suite">
		<testcase name="[It] first test"><property name="inventory-testcase-id" value="INV-1"/></testcase>
		<testcase name="[It] second test"><property name="inventory-testcase-id" value="INV-2"/></testcase>
	</testsuite></testsuites>`), 0o600)
	require.NoError(t, err, "expected xml fixture")

	meta, err := send.ParseMetadataJSON([]byte(`{
		"targets": {"reportportal": {
			"config": {"hostname": "rp.example.com", "project": "demo"},
			"processing": {"property_filter": ["inventory-testcase-id"]}
		}}
	}`))
	require.NoError(t, err, "expected metadata parse")

	index, _, err := send.BuildTestCaseIndex([]string{xmlPath}, "", meta)
	require.NoError(t, err, "expected index build")
	require.Len(t, index.Roots, 1, "expected one root suite")
	require.Empty(t, index.Roots[0].Properties, "expected suite to have no testcase properties")

	steps := index.Steps()
	require.Len(t, steps, 2, "expected two testcase steps")
	require.Equal(t, "INV-1", steps[0].Properties[0].Value, "expected first testcase property")
	require.Equal(t, "INV-2", steps[1].Properties[0].Value, "expected second testcase property")
}

func TestPrepareXML_preservesSystemErr(t *testing.T) {
	t.Parallel()

	input := []byte(`<testsuites><testsuite name="S"><testcase name="T">` +
		`<system-err>STEP: hello</system-err></testcase></testsuite></testsuites>`)

	output, _, err := send.PrepareXML(input, "")
	require.NoError(t, err, "expected XML preparation to succeed")
	require.Contains(t, string(output), "<system-err>", "expected system-err element preserved")
	require.Contains(t, string(output), "STEP: hello", "expected system-err body preserved")
}

func TestBuildTestCaseIndex_nestedProperties(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "props.xml")
	err := os.WriteFile(
		xmlPath,
		[]byte(`<testsuites><testsuite name="S"><testcase classname="C" name="N">`+
			`<properties><property name="ci-lane" value="main"/></properties>`+
			`</testcase></testsuite></testsuites>`),
		0o600,
	)
	require.NoError(t, err, "expected xml fixture")

	meta, err := send.ParseMetadataJSON([]byte(`{
		"targets": {"reportportal": {
			"config": {"hostname": "rp.example.com", "project": "demo"},
			"processing": {"property_filter": ["ci-lane"]}
		}}
	}`))
	require.NoError(t, err, "expected metadata parse")

	index, _, err := send.BuildTestCaseIndex([]string{xmlPath}, "", meta)
	require.NoError(t, err, "expected index build")

	steps := index.Steps()
	require.Len(t, steps, 1, "expected one testcase")
	require.Len(t, steps[0].Properties, 1, "expected property from nested properties element")
	require.Equal(t, "ci-lane", steps[0].Properties[0].Key, "expected ci-lane property")
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()

	// #nosec G304 -- test fixture path from t.TempDir or testdata
	data, err := os.ReadFile(path)
	require.NoError(t, err, "expected file read")

	return data
}
