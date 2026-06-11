package send_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/klaskosk/report-portal-uploader/internal/send"
	"github.com/stretchr/testify/require"
)

func TestBuildResultsZip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "result.xml")
	err := os.WriteFile(xmlPath, []byte("<testsuite/>"), 0o600)
	require.NoError(t, err, "expected xml fixture")

	zipPath := filepath.Join(dir, "out.zip")
	err = send.BuildResultsZip([]string{xmlPath}, zipPath)
	require.NoError(t, err, "expected zip build")

	reader, err := zip.OpenReader(zipPath)
	require.NoError(t, err, "expected zip open")

	defer func() { _ = reader.Close() }()

	require.Len(t, reader.File, 1, "expected one zip entry")
	require.Equal(t, "result.xml", reader.File[0].Name, "expected basename entry name")
}
