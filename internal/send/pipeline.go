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

// Config holds runtime options for the send command upload pipeline.
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

// PipelineConfig holds shared runtime options for upload pipelines.
type PipelineConfig struct {
	AttachmentsDir string
	Token          string
	HTTPTimeout    time.Duration
	KeepWorkDir    bool
	LaunchURLFile  string
}

// SourceInput is one prepared XML source before Layer 1 transforms.
type SourceInput struct {
	OriginalPath string
	WorkdirName  string
	Content      []byte
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

// Run executes prepare → import → postprocess for the send command.
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

	if cfg.ResultsGlob == "" {
		return Result{}, fmt.Errorf("--results is required")
	}

	if cfg.AttachmentsDir != "" && cfg.ResultsGlob == "" {
		return Result{}, fmt.Errorf("--attachments cannot be used without --results")
	}

	sourcePaths, err := ExpandResultsGlob(cfg.ResultsGlob)
	if err != nil {
		return Result{}, err
	}

	sources := make([]SourceInput, 0, len(sourcePaths))
	for _, sourcePath := range sourcePaths {
		content, readErr := os.ReadFile(sourcePath)
		if readErr != nil {
			return Result{}, fmt.Errorf("reading %q: %w", sourcePath, readErr)
		}

		sources = append(sources, SourceInput{
			OriginalPath: sourcePath,
			WorkdirName:  filepath.Base(sourcePath),
			Content:      content,
		})
	}

	pipelineCfg := PipelineConfig{
		AttachmentsDir: cfg.AttachmentsDir,
		Token:          cfg.Token,
		HTTPTimeout:    cfg.HTTPTimeout,
		KeepWorkDir:    cfg.KeepWorkDir,
		LaunchURLFile:  cfg.LaunchURLFile,
	}

	result, runWarnings, err := RunWithMetadata(ctx, meta, pipelineCfg, sources, logger)
	warnings = append(warnings, runWarnings...)

	return result, err
}

// RunWithMetadata executes prepare → import → postprocess using a prepared Metadata value.
func RunWithMetadata(
	ctx context.Context,
	meta *Metadata,
	cfg PipelineConfig,
	sources []SourceInput,
	logger *slog.Logger,
) (Result, WarningList, error) {
	var warnings WarningList

	if cfg.Token == "" {
		return Result{}, warnings, fmt.Errorf("no API token configured (use --token or REPORTPORTAL_TOKEN)")
	}

	if len(sources) == 0 {
		return Result{}, warnings, fmt.Errorf("no source files to upload")
	}

	workDir, err := os.MkdirTemp("", "rpu-workdir-*")
	if err != nil {
		return Result{}, warnings, fmt.Errorf("creating work directory: %w", err)
	}

	if cfg.KeepWorkDir {
		logger.Info("keeping work directory", "path", workDir)
	} else {
		defer os.RemoveAll(workDir)
	}

	processedPaths, index, prepareWarnings, err := CopyAndPrepareSourceInputs(
		workDir,
		sources,
		cfg.AttachmentsDir,
		meta,
	)
	warnings = append(warnings, prepareWarnings...)
	if err != nil {
		return Result{}, warnings, err
	}

	zipPath, err := BuildImportZip(workDir, processedPaths)
	if err != nil {
		return Result{}, warnings, err
	}

	httpClient := &http.Client{Timeout: cfg.HTTPTimeout}
	rpClient, err := client.New(meta.HostnameURL, cfg.Token, client.WithHTTPClient(httpClient))
	if err != nil {
		return Result{}, warnings, fmt.Errorf("creating Report Portal client: %w", err)
	}

	importAPI := &ClientImportAPI{Client: rpClient}
	importResult, importWarnings, err := RunImport(ctx, importAPI, meta, zipPath)
	warnings = append(warnings, importWarnings...)
	if err != nil {
		return Result{}, warnings, err
	}

	postAPI := &ClientPostImportAPI{Client: rpClient}
	postWarnings, err := RunPostImport(ctx, postAPI, meta, importResult, index, cfg.AttachmentsDir)
	warnings = append(warnings, postWarnings...)
	if err != nil {
		return Result{}, warnings, err
	}

	if cfg.LaunchURLFile != "" {
		err = os.WriteFile(cfg.LaunchURLFile, []byte(importResult.LaunchURL+"\n"), 0o644)
		if err != nil {
			return Result{}, warnings, fmt.Errorf("writing launch URL file: %w", err)
		}
	}

	return Result{
		LaunchURL:  importResult.LaunchURL,
		LaunchID:   importResult.LaunchID,
		LaunchUUID: importResult.LaunchUUID,
		Warnings:   warnings.Messages(),
	}, warnings, nil
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
