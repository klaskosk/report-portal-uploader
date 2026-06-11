package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/klaskosk/report-portal-uploader/internal/cli/core"
	"github.com/klaskosk/report-portal-uploader/internal/cli/send"
	"github.com/klaskosk/report-portal-uploader/internal/cli/upload"
	"github.com/spf13/cobra"
)

// Execute runs the root command with arguments from os.Args.
func Execute() error {
	return ExecuteWithArgs(nil, os.Stderr)
}

// ExecuteWithArgs runs the root command with the given args and error output writer.
func ExecuteWithArgs(args []string, errOut io.Writer) error {
	cmd := newRootCmd()
	if args != nil {
		cmd.SetArgs(args)
	}

	err := cmd.Execute()
	if err == nil {
		return nil
	}

	if !core.IsPrintedError(err) {
		core.NewPrinterWithErrOut(core.EffectiveOutputFormat(cmd.Root()), errOut).PrintError(err)
	}

	return fmt.Errorf("executing command: %w", err)
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "rpu",
		Short:         "Upload test results to Report Portal",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if core.IsGroupCommand(cmd) {
				return nil
			}

			logger := core.NewLogger(core.ResolveVerbosity(cmd))
			cmd.SetContext(core.WithLogger(cmd.Context(), logger))

			_ = cmd.Root().PersistentFlags().Set("output", core.EffectiveOutputFormat(cmd.Root()))

			return nil
		},
	}

	flags := cmd.PersistentFlags()
	flags.BoolP("verbose", "v", false, "Enable debug logging")
	flags.Int("verbosity", 0, "Log verbosity (0=silent, 1=info, 2=debug, 3=trace)")
	flags.StringP("output", "o", "", "Output format (table, json)")
	flags.Bool("json", false, "Output in JSON format")
	flags.String("token", "", "Report Portal API token (env: REPORTPORTAL_TOKEN)")
	flags.Duration("http-timeout", 0, "HTTP client timeout (0 = none)")
	flags.Bool("keep-workdir", false, "Keep temporary work directory after upload")

	send.AddCommands(cmd)
	upload.AddCommands(cmd)

	return cmd
}
