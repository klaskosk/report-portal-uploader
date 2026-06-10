package send

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/klaskosk/report-portal-cli/pkg/client"
)

// Config holds runtime options for the upload pipeline.
type Config struct {
	MetadataPath   string
	ResultsGlob    string
	AttachmentsDir string
	TemplateVars   map[string]string
	Token          string
	HTTPTimeout    time.Duration
	KeepWorkDir    bool
	LaunchURLFile  string
}

// Result is the structured outcome of a successful upload.
type Result struct {
	LaunchURL string        `json:"launchUrl"`
	LaunchID  int64         `json:"launchId"`
	LaunchUUID string       `json:"launchUuid"`
	Warnings  []string      `json:"warnings,omitempty"`
	Disabled  bool          `json:"disabled,omitempty"`
	Message   string        `json:"message,omitempty"`
}

// Run executes prepare → import → postprocess.
func Run(ctx context.Context, cfg Config, logger *slog.Logger) (Result, error) {
	meta, err := ParseMetadataFile(cfg.MetadataPath, cfg.TemplateVars)
	if err != nil {
		return Result{}, err
	}

	if meta.Disabled {
		return Result{
			Disabled: true,
			Message:  "Report Portal target disabled in metadata; skipping upload",
		}, nil
	}

	var warnings WarningList

	if meta.ApplyTFA {
		warnings.Add("metadata", "apply_tfa is enabled but TFA is not implemented in Layer 1")
	}

	if meta.ApplyLLMAnalysis {
		warnings.Add("metadata", "apply_llm_analysis is enabled but LLM analysis is not implemented in Layer 1")
	}

	if cfg.Token == "" {
		return Result{}, fmt.Errorf("no API token configured (use --token or REPORTPORTAL_TOKEN)")
	}

	sourcePaths, err := ExpandResultsGlob(cfg.ResultsGlob)
	if err != nil {
		return Result{}, err
	}

	if cfg.AttachmentsDir != "" && cfg.ResultsGlob == "" {
		return Result{}, fmt.Errorf("--attachments cannot be used without --results")
	}

	workDir, err := os.MkdirTemp("", "rpu-workdir-*")
	if err != nil {
		return Result{}, fmt.Errorf("creating work directory: %w", err)
	}

	if cfg.KeepWorkDir {
		logger.Info("keeping work directory", "path", workDir)
	} else {
		defer os.RemoveAll(workDir)
	}

	processedPaths, index, prepareWarnings, err := CopyAndPrepareResults(
		workDir,
		sourcePaths,
		cfg.AttachmentsDir,
		meta,
	)
	warnings = append(warnings, prepareWarnings...)
	if err != nil {
		return Result{}, err
	}

	zipPath, err := BuildImportZip(workDir, processedPaths)
	if err != nil {
		return Result{}, err
	}

	httpClient := &http.Client{Timeout: cfg.HTTPTimeout}
	rpClient, err := client.New(meta.HostnameURL, cfg.Token, client.WithHTTPClient(httpClient))
	if err != nil {
		return Result{}, fmt.Errorf("creating Report Portal client: %w", err)
	}

	importAPI := &ClientImportAPI{Client: rpClient}
	importResult, importWarnings, err := RunImport(ctx, importAPI, meta, zipPath)
	warnings = append(warnings, importWarnings...)
	if err != nil {
		return Result{}, err
	}

	postAPI := &ClientPostImportAPI{Client: rpClient}
	postWarnings, err := RunPostImport(ctx, postAPI, meta, importResult, index, cfg.AttachmentsDir)
	warnings = append(warnings, postWarnings...)
	if err != nil {
		return Result{}, err
	}

	if cfg.LaunchURLFile != "" {
		err = os.WriteFile(cfg.LaunchURLFile, []byte(importResult.LaunchURL+"\n"), 0o644)
		if err != nil {
			return Result{}, fmt.Errorf("writing launch URL file: %w", err)
		}
	}

	return Result{
		LaunchURL:  importResult.LaunchURL,
		LaunchID:   importResult.LaunchID,
		LaunchUUID: importResult.LaunchUUID,
		Warnings:   warnings.Messages(),
	}, nil
}

// WritePreparedArtifacts copies prepared XML to destDir for debugging when keep-workdir is set.
func WritePreparedArtifacts(workDir, destDir string) error {
	src := filepath.Join(workDir, "results")

	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		target := filepath.Join(destDir, rel)
		err = os.MkdirAll(filepath.Dir(target), 0o755)
		if err != nil {
			return err
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		return os.WriteFile(target, data, 0o644)
	})
}
