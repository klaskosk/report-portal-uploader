package send

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindAttachmentFiles_rejectsPathTraversal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	attachmentsDir := filepath.Join(dir, "attachments")
	outsideDir := filepath.Join(dir, "outside")

	err := os.MkdirAll(attachmentsDir, 0o750)
	require.NoError(t, err, "expected attachments dir")
	err = os.MkdirAll(outsideDir, 0o750)
	require.NoError(t, err, "expected outside dir")
	err = os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("x"), 0o600)
	require.NoError(t, err, "expected outside file")

	files := findAttachmentFiles([]string{"../../outside"}, attachmentsDir)
	require.Empty(t, files, "expected path traversal dir name to collect no files")
}

func TestFindAttachmentFiles_collectsFilesUnderAttachmentsDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	attachmentsDir := filepath.Join(dir, "attachments")
	innerDir := filepath.Join(attachmentsDir, "C.N")

	err := os.MkdirAll(innerDir, 0o750)
	require.NoError(t, err, "expected inner attachment dir")
	err = os.WriteFile(filepath.Join(innerDir, "log.txt"), []byte("x"), 0o600)
	require.NoError(t, err, "expected attachment file")

	files := findAttachmentFiles([]string{"C.N"}, attachmentsDir)
	require.Equal(
		t,
		[]string{filepath.Join("C.N", "log.txt")},
		files,
		"expected files under attachments dir",
	)
}
