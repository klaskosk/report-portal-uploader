package upload

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SourceFile is one expanded XML source ready for Layer 2 transforms.
type SourceFile struct {
	Index        int
	Path         string
	OriginalPath string
	Lane         string
}

// ExpandSources expands each [[sources]] path with filepath.Glob.
func ExpandSources(sources []SourceConfig) ([]SourceFile, error) {
	var records []SourceFile

	index := 0

	for sourceIndex, source := range sources {
		matches, err := filepath.Glob(strings.TrimSpace(source.Path))
		if err != nil {
			return nil, fmt.Errorf("sources[%d].path glob %q: %w", sourceIndex, source.Path, err)
		}

		if len(matches) == 0 {
			return nil, fmt.Errorf(
				"sources[%d].path %q matched no files (Go glob: * and ? only; no ** or brace expansion)",
				sourceIndex,
				source.Path,
			)
		}

		for _, match := range matches {
			info, statErr := os.Stat(match)
			if statErr != nil {
				return nil, fmt.Errorf("stat source file %q: %w", match, statErr)
			}

			if info.IsDir() {
				continue
			}

			absPath, absErr := filepath.Abs(match)
			if absErr != nil {
				return nil, fmt.Errorf("resolving absolute path for %q: %w", match, absErr)
			}

			records = append(records, SourceFile{
				Index:        index,
				Path:         absPath,
				OriginalPath: absPath,
				Lane:         source.Lane,
			})
			index++
		}
	}

	if len(records) == 0 {
		return nil, errors.New("no source files matched [[sources]] entries")
	}

	return records, nil
}
