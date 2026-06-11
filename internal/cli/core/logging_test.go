package core_test

import (
	"log/slog"
	"testing"

	"github.com/klaskosk/report-portal-uploader/internal/cli/core"
	"github.com/spf13/cobra"
)

func TestVerbosityToLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		verbosity int
		want      slog.Level
	}{
		{verbosity: -1, want: slog.Level(1<<63 - 1)},
		{verbosity: core.VerbositySilent, want: slog.Level(1<<63 - 1)},
		{verbosity: core.VerbosityInfo, want: slog.LevelInfo},
		{verbosity: core.VerbosityDebug, want: slog.LevelDebug},
		{verbosity: core.VerbosityTrace, want: core.LevelTrace},
		{verbosity: 99, want: core.LevelTrace},
	}

	for _, tc := range tests {
		t.Run("", func(t *testing.T) {
			t.Parallel()

			if got := core.VerbosityToLevel(tc.verbosity); got != tc.want {
				t.Errorf("VerbosityToLevel(%d) = %v, want %v", tc.verbosity, got, tc.want)
			}
		})
	}
}

func TestResolveVerbosity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "default silent", args: nil, want: core.VerbositySilent},
		{name: "verbose flag", args: []string{"-v"}, want: core.VerbosityDebug},
		{name: "verbosity info", args: []string{"--verbosity", "1"}, want: core.VerbosityInfo},
		{name: "verbosity trace", args: []string{"--verbosity", "3"}, want: core.VerbosityTrace},
		{
			name: "verbosity overrides verbose",
			args: []string{"-v", "--verbosity", "1"},
			want: core.VerbosityInfo,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			root := &cobra.Command{Use: "rpu"}
			root.PersistentFlags().BoolP("verbose", "v", false, "")
			root.PersistentFlags().Int("verbosity", 0, "")

			err := root.ParseFlags(testCase.args)
			if err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}

			if got := core.ResolveVerbosity(root); got != testCase.want {
				t.Fatalf("core.ResolveVerbosity() = %d, want %d", got, testCase.want)
			}
		})
	}
}
