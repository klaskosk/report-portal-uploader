package send_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/klaskosk/report-portal-uploader/internal/send"
	"github.com/stretchr/testify/require"
)

func TestParseMetadataJSON_disabled(t *testing.T) {
	t.Parallel()

	meta, err := send.ParseMetadataJSON([]byte(`{
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

	meta, err := send.ParseMetadataJSON([]byte(`{
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

	require.Equal(t, send.DefaultLaunchName, meta.LaunchName, "expected default launch name")
	require.Equal(t, "https://rp.example.com/", meta.HostnameURL, "expected hostname URL")

	keys := map[string]send.LaunchAttribute{}
	for _, attr := range meta.LaunchAttributes {
		keys[attr.Key] = attr
	}

	require.Contains(t, keys, send.LaunchRequestIDAttrKey, "expected launch request ID attribute")
	require.Equal(t, "true", keys["skippedIsNotIssue"].Value, "expected skippedIsNotIssue=true")
	require.True(t, keys["skippedIsNotIssue"].System, "expected skippedIsNotIssue system flag")
}

func TestPropertyNameMatches_fullString(t *testing.T) {
	t.Parallel()

	compiled, err := send.CompilePropertyFilters([]string{`inventory-.*`, "ci-lane", "attachment"})
	require.NoError(t, err, "expected patterns to compile")

	require.True(
		t,
		send.PropertyNameMatches(compiled, "inventory-testcase-id"),
		"expected inventory property to match",
	)
	require.False(
		t,
		send.PropertyNameMatches(compiled, "not-inventory-testcase-id"),
		"expected non-matching property",
	)
	require.True(t, send.PropertyNameMatches(compiled, "ci-lane"), "expected exact ci-lane match")
	require.False(
		t,
		send.PropertyNameMatches(compiled, "prefix-ci-lane"),
		"expected full-string match failure",
	)
}

func TestPropertyNameMatches_nullAndEmpty(t *testing.T) {
	t.Parallel()

	require.False(t, send.PropertyNameMatches(nil, "anything"), "null filter should match nothing")
	require.False(
		t,
		send.PropertyNameMatches([]*regexp.Regexp{}, "anything"),
		"empty filter should match nothing",
	)
}

func TestParseMetadataFile_substitutesTemplateVars(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	err := os.WriteFile(path, []byte(`{
		"targets": {
			"reportportal": {
				"config": {"hostname": "{{.HOST}}", "project": "demo"},
				"processing": {"launch": {"name": "{{.NAME}}"}}
			}
		}
	}`), 0o600)
	require.NoError(t, err, "expected metadata template write")

	meta, err := send.ParseMetadataFile(path, map[string]string{
		"HOST": "rp.example.com",
		"NAME": "replaced",
	})
	require.NoError(t, err, "expected metadata substitution")
	require.Equal(t, "replaced", meta.LaunchName, "expected substituted launch name")
	require.Equal(
		t,
		"https://rp.example.com/",
		meta.HostnameURL,
		"expected substituted hostname URL",
	)
}

func TestLaunchUIURL(t *testing.T) {
	t.Parallel()

	meta := &send.Metadata{
		HostnameURL: "https://rp.example.com/",
		Project:     "demo_project",
	}

	require.Equal(
		t,
		"https://rp.example.com/ui/#demo_project/launches/all/42",
		send.LaunchUIURL(meta, 42),
		"expected slash between host and ui path",
	)
}

func TestExpandResultsGlob(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "a.xml"), []byte("<testsuite/>"), 0o600)
	require.NoError(t, err, "expected fixture write")

	matches, err := send.ExpandResultsGlob(filepath.Join(dir, "*.xml"))
	require.NoError(t, err, "expected glob expansion")
	require.Len(t, matches, 1, "expected one XML match")
}
