package send

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// BuildResultsZip creates a zip archive containing the given XML files using basenames as entry names.
func BuildResultsZip(xmlPaths []string, destPath string) error {
	zipFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("creating zip file: %w", err)
	}
	defer zipFile.Close()

	writer := zip.NewWriter(zipFile)
	defer writer.Close()

	for _, xmlPath := range xmlPaths {
		err = addFileToZip(writer, xmlPath)
		if err != nil {
			return err
		}
	}

	err = writer.Close()
	if err != nil {
		return fmt.Errorf("closing zip writer: %w", err)
	}

	return nil
}

func addFileToZip(writer *zip.Writer, path string) error {
	source, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening %q for zip: %w", path, err)
	}
	defer source.Close()

	info, err := source.Stat()
	if err != nil {
		return fmt.Errorf("stat %q for zip: %w", path, err)
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return fmt.Errorf("zip header for %q: %w", path, err)
	}

	header.Name = filepath.Base(path)
	header.Method = zip.Deflate

	entry, err := writer.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("creating zip entry for %q: %w", path, err)
	}

	_, err = io.Copy(entry, source)
	if err != nil {
		return fmt.Errorf("writing zip entry for %q: %w", path, err)
	}

	return nil
}
