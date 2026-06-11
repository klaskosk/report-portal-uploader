package send

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	LaunchURL  string   `json:"launchUrl"`
	LaunchID   int64    `json:"launchId"`
	LaunchUUID string   `json:"launchUuid"`
	Warnings   []string `json:"warnings,omitempty"`
	Disabled   bool     `json:"disabled,omitempty"`
	Message    string   `json:"message,omitempty"`
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

	warnings := metadataFeatureWarnings(meta)

	err = validateSendInputs(cfg)
	if err != nil {
		return Result{}, err
	}

	sources, err := loadSourceInputs(cfg.ResultsGlob)
	if err != nil {
		return Result{}, err
	}

	pipelineCfg := PipelineConfig{
		AttachmentsDir: cfg.AttachmentsDir,
		Token:          cfg.Token,
		HTTPTimeout:    cfg.HTTPTimeout,
		KeepWorkDir:    cfg.KeepWorkDir,
		LaunchURLFile:  cfg.LaunchURLFile,
	}

	result, _, err := RunWithMetadata(ctx, meta, pipelineCfg, sources, logger)
	if len(warnings) > 0 {
		result.Warnings = append(warnings.Messages(), result.Warnings...)
	}

	return result, err
}

func metadataFeatureWarnings(meta *Metadata) WarningList {
	warnings := make(WarningList, 0, 2)

	if meta.ApplyTFA {
		warnings.Add("metadata", "apply_tfa is enabled but TFA is not implemented in Layer 1")
	}

	if meta.ApplyLLMAnalysis {
		warnings.Add(
			"metadata",
			"apply_llm_analysis is enabled but LLM analysis is not implemented in Layer 1",
		)
	}

	return warnings
}

func validateSendInputs(cfg Config) error {
	if cfg.ResultsGlob == "" {
		return errors.New("--results is required")
	}

	if cfg.AttachmentsDir != "" && cfg.ResultsGlob == "" {
		return errors.New("--attachments cannot be used without --results")
	}

	return nil
}

func loadSourceInputs(resultsGlob string) ([]SourceInput, error) {
	sourcePaths, err := ExpandResultsGlob(resultsGlob)
	if err != nil {
		return nil, err
	}

	sources := make([]SourceInput, 0, len(sourcePaths))
	for _, sourcePath := range sourcePaths {
		readErr := validateCLIPath(sourcePath)
		if readErr != nil {
			return nil, fmt.Errorf("reading %q: %w", sourcePath, readErr)
		}

		content, readErr := os.ReadFile(sourcePath) //nolint:gosec // path validated above
		if readErr != nil {
			return nil, fmt.Errorf("reading %q: %w", sourcePath, readErr)
		}

		sources = append(sources, SourceInput{
			OriginalPath: sourcePath,
			WorkdirName:  filepath.Base(sourcePath),
			Content:      content,
		})
	}

	return sources, nil
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
		return Result{}, warnings, errors.New(
			"no API token configured (use --token or REPORTPORTAL_TOKEN)",
		)
	}

	if len(sources) == 0 {
		return Result{}, warnings, errors.New("no source files to upload")
	}

	workDir, cleanup, err := createWorkDir(cfg.KeepWorkDir, logger)
	if err != nil {
		return Result{}, warnings, err
	}

	if cleanup != nil {
		defer cleanup()
	}

	result, warnings, err := runPreparedPipeline(ctx, meta, cfg, sources, workDir, warnings)

	return result, warnings, err
}

func createWorkDir(keepWorkDir bool, logger *slog.Logger) (string, func(), error) {
	workDir, err := os.MkdirTemp("", "rpu-workdir-*")
	if err != nil {
		return "", nil, fmt.Errorf("creating work directory: %w", err)
	}

	if keepWorkDir {
		logger.Info("keeping work directory", "path", workDir)

		return workDir, nil, nil
	}

	return workDir, func() { _ = os.RemoveAll(workDir) }, nil
}

func runPreparedPipeline(
	ctx context.Context,
	meta *Metadata,
	cfg PipelineConfig,
	sources []SourceInput,
	workDir string,
	warnings WarningList,
) (Result, WarningList, error) {
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

	err = writeLaunchURLFile(cfg.LaunchURLFile, importResult.LaunchURL)
	if err != nil {
		return Result{}, warnings, err
	}

	return Result{
		LaunchURL:  importResult.LaunchURL,
		LaunchID:   importResult.LaunchID,
		LaunchUUID: importResult.LaunchUUID,
		Warnings:   warnings.Messages(),
	}, warnings, nil
}

func writeLaunchURLFile(path, launchURL string) error {
	if path == "" {
		return nil
	}

	err := validateCLIPath(path)
	if err != nil {
		return fmt.Errorf("writing launch URL file: %w", err)
	}

	err = os.WriteFile(path, []byte(launchURL+"\n"), 0o600)
	if err != nil {
		return fmt.Errorf("writing launch URL file: %w", err)
	}

	return nil
}

// WritePreparedArtifacts copies prepared XML to destDir for debugging when keep-workdir is set.
func WritePreparedArtifacts(workDir, destDir string) error {
	src := filepath.Join(workDir, "results")

	err := filepath.WalkDir(src, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}

		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return fmt.Errorf("rel path for %q: %w", path, relErr)
		}

		target := filepath.Join(destDir, rel)

		mkdirErr := os.MkdirAll(filepath.Dir(target), 0o750)
		if mkdirErr != nil {
			return fmt.Errorf("creating artifact directory: %w", mkdirErr)
		}

		data, readErr := os.ReadFile(path) //nolint:gosec // path is under walked src root
		if readErr != nil {
			return fmt.Errorf("reading artifact %q: %w", path, readErr)
		}

		if !strings.HasPrefix(target, destDir+string(os.PathSeparator)) && target != destDir {
			return fmt.Errorf("artifact path escapes destination: %q", target)
		}

		// #nosec G703 -- target path validated under destDir
		writeErr := os.WriteFile(target, data, 0o600)
		if writeErr != nil {
			return fmt.Errorf("writing artifact %q: %w", target, writeErr)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("walking prepared artifacts: %w", err)
	}

	return nil
}
