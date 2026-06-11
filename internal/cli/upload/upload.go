package upload

import (
	"context"
	"fmt"
	"os"

	"github.com/klaskosk/report-portal-uploader/internal/cli/core"
	rpuupload "github.com/klaskosk/report-portal-uploader/internal/upload"
	"github.com/spf13/cobra"
)

// AddCommands registers the upload command on root.
func AddCommands(root *cobra.Command) {
	root.AddCommand(newUploadCommand())
}

func newUploadCommand() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "upload",
		Short: "Upload xUnit results to Report Portal using a TOML config file",
		RunE: core.WithPrintedErrors(func(cmd *cobra.Command, _ []string) error {
			printer := core.NewTestPrinter(
				core.EffectiveOutputFormat(cmd.Root()),
				cmd.OutOrStdout(),
				cmd.ErrOrStderr(),
			)

			token, _ := cmd.Root().Flags().GetString("token")
			if token == "" {
				token = os.Getenv("REPORTPORTAL_TOKEN")
			}

			httpTimeout, _ := cmd.Root().Flags().GetDuration("http-timeout")
			keepWorkdir, _ := cmd.Root().Flags().GetBool("keep-workdir")

			cfg := rpuupload.RuntimeConfig{
				ConfigPath:  configPath,
				Token:       token,
				HTTPTimeout: httpTimeout,
				KeepWorkDir: keepWorkdir,
			}

			logger := core.LoggerFromCmd(cmd)

			result, err := rpuupload.Run(context.Background(), cfg, logger)
			if err != nil {
				return core.PrintAndReturnError(printer, err)
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
		}),
	}

	cmd.Flags().StringVar(&configPath, "config", "", "Path to TOML config file, or - for stdin")
	_ = cmd.MarkFlagRequired("config")

	return cmd
}
