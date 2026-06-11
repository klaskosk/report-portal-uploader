package upload

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	rpusend "github.com/klaskosk/report-portal-uploader/internal/send"
)

// RuntimeConfig holds CLI/runtime options for the upload command.
type RuntimeConfig struct {
	ConfigPath    string
	Token         string
	HTTPTimeout   time.Duration
	KeepWorkDir   bool
	LaunchURLFile string
}

// Run executes the Layer 2 upload pipeline: config → transforms → shared send pipeline.
func Run(ctx context.Context, runtime RuntimeConfig, logger *slog.Logger) (rpusend.Result, error) {
	cfg, err := ParseConfigFile(runtime.ConfigPath)
	if err != nil {
		return rpusend.Result{}, err
	}

	meta, err := BuildMetadata(cfg)
	if err != nil {
		return rpusend.Result{}, err
	}

	sources, err := ExpandSources(cfg.Sources)
	if err != nil {
		return rpusend.Result{}, err
	}

	inputs, warnings, err := prepareUploadInputs(cfg, sources)
	if err != nil {
		return rpusend.Result{}, err
	}

	if len(inputs) == 0 {
		return rpusend.Result{}, errors.New("no valid XML files after preparation")
	}

	launchURLFile := runtime.LaunchURLFile
	if launchURLFile == "" {
		launchURLFile = cfg.Output.LaunchURLFile
	}

	pipelineCfg := rpusend.PipelineConfig{
		AttachmentsDir: cfg.Attachments.Directory,
		Token:          runtime.Token,
		HTTPTimeout:    runtime.HTTPTimeout,
		KeepWorkDir:    runtime.KeepWorkDir,
		LaunchURLFile:  launchURLFile,
	}

	result, runWarnings, err := rpusend.RunWithMetadata(ctx, meta, pipelineCfg, inputs, logger)
	warnings = append(warnings, runWarnings...)

	if err != nil {
		return rpusend.Result{}, fmt.Errorf("running upload pipeline: %w", err)
	}

	if len(warnings) > 0 {
		result.Warnings = append(result.Warnings, warnings.Messages()...)
	}

	return result, nil
}

func prepareUploadInputs(
	cfg *Config,
	sources []SourceFile,
) ([]rpusend.SourceInput, rpusend.WarningList, error) {
	var warnings rpusend.WarningList

	inputs := make([]rpusend.SourceInput, 0, len(sources))

	for _, source := range sources {
		basename := filepath.Base(source.Path)
		if ShouldExclude(basename, cfg.Processing.ExcludePatterns) {
			warnings.Add("transform", "Skipping excluded file "+basename)

			continue
		}

		content, readErr := os.ReadFile(source.Path)
		if readErr != nil {
			return nil, warnings, fmt.Errorf("reading %q: %w", source.Path, readErr)
		}

		transformed, transformWarnings, transformErr := ApplyLayer2Transforms(
			content,
			TransformOptions{
				Lane:            source.Lane,
				LaneProperty:    cfg.Processing.LaneProperty,
				ExcludePatterns: cfg.Processing.ExcludePatterns,
				SourcePath:      source.Path,
			},
		)

		warnings = append(warnings, transformWarnings...)
		if transformErr != nil {
			warnings.Add(
				"transform",
				fmt.Sprintf("Ignoring file %s: %s", basename, transformErr.Error()),
			)

			continue
		}

		inputs = append(inputs, rpusend.SourceInput{
			OriginalPath: source.OriginalPath,
			WorkdirName:  WorkdirName(source),
			Content:      transformed,
		})
	}

	return inputs, warnings, nil
}
