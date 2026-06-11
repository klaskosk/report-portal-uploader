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

type sendOptions struct {
	metadataPath   string
	resultsGlob    string
	attachmentsDir string
	templateVars   []string
	launchURLFile  string
}

func newSendCommand() *cobra.Command {
	opts := &sendOptions{}

	cmd := &cobra.Command{
		Use:   "send",
		Short: "Upload xUnit results to Report Portal using a metadata JSON file",
		RunE:  core.WithPrintedErrors(runSendCommand(opts)),
	}

	configureSendFlags(cmd, opts)

	return cmd
}

func configureSendFlags(cmd *cobra.Command, opts *sendOptions) {
	cmd.Flags().StringVar(&opts.metadataPath, "metadata", "", "Path to metadata JSON file")
	cmd.Flags().
		StringVar(&opts.resultsGlob, "results", "", "xUnit results glob (e.g. results/*.xml)")
	cmd.Flags().StringVar(&opts.attachmentsDir, "attachments", "", "Attachments directory")
	cmd.Flags().
		StringArrayVar(&opts.templateVars, "var", nil, "Metadata template variable (key=value)")
	cmd.Flags().
		StringVar(&opts.launchURLFile, "launch-url-file", "", "Write launch URL to this file")

	_ = cmd.MarkFlagRequired("metadata")
	_ = cmd.MarkFlagRequired("results")
}

func runSendCommand(opts *sendOptions) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		printer := core.NewTestPrinter(
			core.EffectiveOutputFormat(cmd.Root()),
			cmd.OutOrStdout(),
			cmd.ErrOrStderr(),
		)

		vars, err := parseTemplateVars(opts.templateVars)
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
			MetadataPath:   opts.metadataPath,
			ResultsGlob:    opts.resultsGlob,
			AttachmentsDir: opts.attachmentsDir,
			TemplateVars:   vars,
			Token:          token,
			HTTPTimeout:    httpTimeout,
			KeepWorkDir:    keepWorkdir,
			LaunchURLFile:  opts.launchURLFile,
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

		printerErr := printer.Print(result, nil, nil)
		if printerErr != nil {
			return core.PrintAndReturnError(printer, printerErr)
		}

		if core.EffectiveOutputFormat(cmd.Root()) != "json" && result.LaunchURL != "" {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), result.LaunchURL)
		}

		return nil
	}
}

func parseTemplateVars(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return map[string]string{}, nil
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
