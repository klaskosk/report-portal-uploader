package send

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseMetadataJSON_disabled(t *testing.T) {
	t.Parallel()

	meta, err := ParseMetadataJSON([]byte(`{
		"targets": {
			"reportportal": {
				"disabled": true,
				"config": {"hostname": "rp.example.com", "project": "demo"},
				"processing": {}
			}
		}
	}`))
	require.NoError(t, err, "expected disabled metadata to parse")
	require.True(t, meta.Disabled, "expected disabled flag")
}

func TestParseMetadataJSON_injectsLaunchAttributes(t *testing.T) {
	t.Parallel()

	meta, err := ParseMetadataJSON([]byte(`{
		"targets": {
			"reportportal": {
				"config": {"hostname": "rp.example.com", "project": "demo"},
				"processing": {
					"launch": {"name": "", "description": "desc", "attributes": [{"key": "tool", "value": "test"}]}
				}
			}
		}
	}`))
	require.NoError(t, err, "expected metadata parse to succeed")

	require.Equal(t, defaultLaunchName, meta.LaunchName, "expected default launch name")
	require.Equal(t, "https://rp.example.com/", meta.HostnameURL, "expected hostname URL")

	keys := map[string]LaunchAttribute{}
	for _, attr := range meta.LaunchAttributes {
		keys[attr.Key] = attr
	}

	require.Contains(t, keys, launchRequestIDAttrKey, "expected launch request ID attribute")
	require.Equal(t, "true", keys["skippedIsNotIssue"].Value, "expected skippedIsNotIssue=true")
	require.True(t, keys["skippedIsNotIssue"].System, "expected skippedIsNotIssue system flag")
}

func TestPropertyNameMatches_fullString(t *testing.T) {
	t.Parallel()

	compiled, err := compilePropertyFilters([]string{`inventory-.*`, "ci-lane", "attachment"})
	require.NoError(t, err, "expected patterns to compile")

	require.True(t, PropertyNameMatches(compiled, "inventory-testcase-id"), "expected inventory property to match")
	require.False(t, PropertyNameMatches(compiled, "not-inventory-testcase-id"), "expected non-matching property")
	require.True(t, PropertyNameMatches(compiled, "ci-lane"), "expected exact ci-lane match")
	require.False(t, PropertyNameMatches(compiled, "prefix-ci-lane"), "expected full-string match failure")
}

func TestPropertyNameMatches_nullAndEmpty(t *testing.T) {
	t.Parallel()

	require.False(t, PropertyNameMatches(nil, "anything"), "null filter should match nothing")
	require.False(t, PropertyNameMatches([]*regexp.Regexp{}, "anything"), "empty filter should match nothing")
}

func TestSubstituteMetadataVars(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	err := os.WriteFile(path, []byte(`{"value":"{{.NAME}}"}`), 0o644)
	require.NoError(t, err, "expected metadata template write")

	processed, err := substituteMetadataVars(path, map[string]string{"NAME": "replaced"})
	require.NoError(t, err, "expected metadata substitution")
	defer os.Remove(processed)

	content, err := os.ReadFile(processed)
	require.NoError(t, err, "expected processed metadata read")
	require.Contains(t, string(content), "replaced", "expected substituted value")
}

func TestLaunchUIURL(t *testing.T) {
	t.Parallel()

	meta := &Metadata{
		HostnameURL: "https://rp.example.com/",
		Project:     "demo_project",
	}

	require.Equal(
		t,
		"https://rp.example.com/ui/#demo_project/launches/all/42",
		LaunchUIURL(meta, 42),
		"expected slash between host and ui path",
	)
}

func TestExpandResultsGlob(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "a.xml"), []byte("<testsuite/>"), 0o644)
	require.NoError(t, err, "expected fixture write")

	matches, err := ExpandResultsGlob(filepath.Join(dir, "*.xml"))
	require.NoError(t, err, "expected glob expansion")
	require.Len(t, matches, 1, "expected one XML match")
}
