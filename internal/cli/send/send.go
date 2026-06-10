package send

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/klaskosk/report-portal-uploader/internal/cli/core"
	rpusend "github.com/klaskosk/report-portal-uploader/internal/send"
	"github.com/spf13/cobra"
)

// AddCommands registers the send command on root.
func AddCommands(root *cobra.Command) {
	root.AddCommand(newSendCommand())
}

func newSendCommand() *cobra.Command {
	var (
		metadataPath   string
		resultsGlob    string
		attachmentsDir string
		templateVars   []string
		launchURLFile  string
	)

	cmd := &cobra.Command{
		Use:   "send",
		Short: "Upload xUnit results to Report Portal using a metadata JSON file",
		RunE: core.WithPrintedErrors(func(cmd *cobra.Command, _ []string) error {
			printer := core.NewTestPrinter(
				core.EffectiveOutputFormat(cmd.Root()),
				cmd.OutOrStdout(),
				cmd.ErrOrStderr(),
			)

			vars, err := parseTemplateVars(templateVars)
			if err != nil {
				return core.PrintAndReturnError(printer, err)
			}

			token, _ := cmd.Root().Flags().GetString("token")
			if token == "" {
				token = os.Getenv("REPORTPORTAL_TOKEN")
			}

			httpTimeout, _ := cmd.Root().Flags().GetDuration("http-timeout")
			keepWorkdir, _ := cmd.Root().Flags().GetBool("keep-workdir")

			cfg := rpusend.Config{
				MetadataPath:   metadataPath,
				ResultsGlob:    resultsGlob,
				AttachmentsDir: attachmentsDir,
				TemplateVars:   vars,
				Token:          token,
				HTTPTimeout:    httpTimeout,
				KeepWorkDir:    keepWorkdir,
				LaunchURLFile:  launchURLFile,
			}

			logger := core.LoggerFromCmd(cmd)
			result, err := rpusend.Run(context.Background(), cfg, logger)
			if err != nil {
				return core.PrintAndReturnError(printer, err)
			}

			if result.Disabled {
				return printer.PrintMessage(result.Message)
			}

			for _, warning := range result.Warnings {
				printer.PrintWarning(warning)
			}

			if printerErr := printer.Print(result, nil, nil); printerErr != nil {
				return core.PrintAndReturnError(printer, printerErr)
			}

			if core.EffectiveOutputFormat(cmd.Root()) != "json" && result.LaunchURL != "" {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), result.LaunchURL)
			}

			return nil
		}),
	}

	cmd.Flags().StringVar(&metadataPath, "metadata", "", "Path to metadata JSON file")
	cmd.Flags().StringVar(&resultsGlob, "results", "", "xUnit results glob (e.g. results/*.xml)")
	cmd.Flags().StringVar(&attachmentsDir, "attachments", "", "Attachments directory")
	cmd.Flags().StringArrayVar(&templateVars, "var", nil, "Metadata template variable (key=value)")
	cmd.Flags().StringVar(&launchURLFile, "launch-url-file", "", "Write launch URL to this file")

	_ = cmd.MarkFlagRequired("metadata")
	_ = cmd.MarkFlagRequired("results")

	cmd.SetFlagErrorFunc(migrationFlagErrorFunc)

	return cmd
}

func migrationFlagErrorFunc(_ *cobra.Command, err error) error {
	if hint := migrationFlagHint(err.Error()); hint != "" {
		return fmt.Errorf("%s; %s", err.Error(), hint)
	}

	return err
}

func migrationFlagHint(errMsg string) string {
	for flag, hint := range removedFlagHints {
		if strings.Contains(errMsg, flag) {
			return hint
		}
	}

	return ""
}

var removedFlagHints = map[string]string{
	"--wait":     "remove --wait (rpu send is synchronous and completes before exit)",
	"--url":      "use metadata hostname and --token or REPORTPORTAL_TOKEN",
	"--username": "use --token or REPORTPORTAL_TOKEN",
	"--password": "use --token or REPORTPORTAL_TOKEN",
}

func parseTemplateVars(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}

	vars := make(map[string]string, len(values))
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		if !ok {
			key, val, ok = strings.Cut(value, ":")
		}

		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("invalid --var %q (expected key=value)", value)
		}

		vars[key] = val
	}

	return vars, nil
}
