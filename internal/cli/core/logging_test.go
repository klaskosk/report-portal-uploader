package core

import (
	"log/slog"
	"testing"

	"github.com/spf13/cobra"
)

func TestVerbosityToLevel(t *testing.T) {
	tests := []struct {
		verbosity int
		want      slog.Level
	}{
		{verbosity: -1, want: slog.Level(1<<63 - 1)},
		{verbosity: VerbositySilent, want: slog.Level(1<<63 - 1)},
		{verbosity: VerbosityInfo, want: slog.LevelInfo},
		{verbosity: VerbosityDebug, want: slog.LevelDebug},
		{verbosity: VerbosityTrace, want: LevelTrace},
		{verbosity: 99, want: LevelTrace},
	}

	for _, tc := range tests {
		if got := verbosityToLevel(tc.verbosity); got != tc.want {
			t.Errorf("verbosityToLevel(%d) = %v, want %v", tc.verbosity, got, tc.want)
		}
	}
}

func TestResolveVerbosity(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "default silent", args: nil, want: VerbositySilent},
		{name: "verbose flag", args: []string{"-v"}, want: VerbosityDebug},
		{name: "verbosity info", args: []string{"--verbosity", "1"}, want: VerbosityInfo},
		{name: "verbosity trace", args: []string{"--verbosity", "3"}, want: VerbosityTrace},
		{name: "verbosity overrides verbose", args: []string{"-v", "--verbosity", "1"}, want: VerbosityInfo},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := &cobra.Command{Use: "rpu"}
			root.PersistentFlags().BoolP("verbose", "v", false, "")
			root.PersistentFlags().Int("verbosity", 0, "")

			if err := root.ParseFlags(tc.args); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}

			if got := ResolveVerbosity(root); got != tc.want {
				t.Fatalf("ResolveVerbosity() = %d, want %d", got, tc.want)
			}
		})
	}
}
