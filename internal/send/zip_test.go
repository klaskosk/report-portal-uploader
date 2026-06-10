package send

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildResultsZip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "result.xml")
	err := os.WriteFile(xmlPath, []byte("<testsuite/>"), 0o644)
	require.NoError(t, err, "expected xml fixture")

	zipPath := filepath.Join(dir, "out.zip")
	err = BuildResultsZip([]string{xmlPath}, zipPath)
	require.NoError(t, err, "expected zip build")

	reader, err := zip.OpenReader(zipPath)
	require.NoError(t, err, "expected zip open")
	defer reader.Close()

	require.Len(t, reader.File, 1, "expected one zip entry")
	require.Equal(t, "result.xml", reader.File[0].Name, "expected basename entry name")
}
