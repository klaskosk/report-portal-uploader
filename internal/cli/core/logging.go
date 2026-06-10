package core

import (
	"context"
	"log/slog"
	"math"
	"os"

	"github.com/spf13/cobra"
)

const (
	VerbositySilent = 0
	VerbosityInfo   = 1
	VerbosityDebug  = 2
	VerbosityTrace  = 3
)

// LevelTrace is more verbose than slog.LevelDebug.
const LevelTrace = slog.Level(-8)

type loggerKey struct{}

// NewLogger creates a *slog.Logger that writes to stderr at the level implied by verbosity.
func NewLogger(verbosity int) *slog.Logger {
	level := verbosityToLevel(verbosity)

	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})

	return slog.New(handler)
}

// ResolveVerbosity returns the effective log verbosity from -v/--verbose and --verbosity.
// --verbosity wins when explicitly set; otherwise -v enables debug logging.
func ResolveVerbosity(cmd *cobra.Command) int {
	flags := cmd.Root().PersistentFlags()

	verbosity, _ := flags.GetInt("verbosity")
	if flags.Changed("verbosity") {
		return verbosity
	}

	verbose, _ := flags.GetBool("verbose")
	if verbose {
		return VerbosityDebug
	}

	return VerbositySilent
}

func verbosityToLevel(verbosity int) slog.Level {
	switch {
	case verbosity <= VerbositySilent:
		return slog.Level(math.MaxInt)
	case verbosity == VerbosityInfo:
		return slog.LevelInfo
	case verbosity == VerbosityDebug:
		return slog.LevelDebug
	default:
		return LevelTrace
	}
}

// LoggerFromCmd extracts the logger stored in the command's context.
func LoggerFromCmd(cmd *cobra.Command) *slog.Logger {
	return LoggerFromContext(cmd.Context())
}

// LoggerFromContext extracts the logger from ctx, returning a discard logger if none is set.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return logger
	}

	return slog.New(slog.DiscardHandler)
}

// WithLogger stores logger in ctx.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}
