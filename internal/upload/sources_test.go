package upload_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/klaskosk/report-portal-uploader/internal/upload"
	"github.com/stretchr/testify/require"
)

func TestExpandSources_literalAndGlob(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.xml")
	fileB := filepath.Join(dir, "b.xml")

	require.NoError(
		t,
		os.WriteFile(fileA, []byte("<testsuites/>"), 0o600),
		"expected fixture write",
	)
	require.NoError(
		t,
		os.WriteFile(fileB, []byte("<testsuites/>"), 0o600),
		"expected fixture write",
	)

	records, err := upload.ExpandSources([]upload.SourceConfig{
		{Path: fileA, Lane: "gm"},
		{Path: filepath.Join(dir, "*.xml"), Lane: "shared"},
	})
	require.NoError(t, err, "expected source expansion")
	require.Len(t, records, 3, "expected literal plus two glob matches as separate records")

	require.Equal(t, 0, records[0].Index, "expected first record index")
	require.Equal(t, "gm", records[0].Lane, "expected first lane")
	require.Equal(t, fileA, records[0].Path, "expected literal path")
}

func TestExpandSources_zeroMatchesError(t *testing.T) {
	t.Parallel()

	_, err := upload.ExpandSources([]upload.SourceConfig{{Path: "*.missing.xml"}})
	require.Error(t, err, "expected zero-match error")
	require.Contains(t, err.Error(), "matched no files", "expected glob miss message")
}

func TestExpandSources_duplicatePathTwoRecords(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "dup.xml")
	require.NoError(
		t,
		os.WriteFile(filePath, []byte("<testsuites/>"), 0o600),
		"expected fixture write",
	)

	records, err := upload.ExpandSources([]upload.SourceConfig{
		{Path: filePath, Lane: "lane-a"},
		{Path: filePath, Lane: "lane-b"},
	})
	require.NoError(t, err, "expected duplicate path expansion")
	require.Len(t, records, 2, "expected two records for duplicate path")
	require.Equal(t, "lane-a", records[0].Lane, "expected first lane")
	require.Equal(t, "lane-b", records[1].Lane, "expected second lane")
	require.Equal(t, filePath, records[0].Path, "expected same path")
	require.Equal(t, filePath, records[1].Path, "expected same path")
	require.NotEqual(t, records[0].Index, records[1].Index, "expected distinct source indexes")
}
