package core

import (
	"errors"

	"github.com/spf13/cobra"
)

type printedError struct{ err error }

func (e *printedError) Error() string { return e.err.Error() }
func (e *printedError) Unwrap() error { return e.err }

// WithPrintedErrors wraps a RunE function so returned errors are marked as already printed.
func WithPrintedErrors(
	fn func(*cobra.Command, []string) error,
) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		err := fn(cmd, args)
		if err != nil {
			return &printedError{err: err}
		}

		return nil
	}
}

// PrintAndReturnError prints err to stderr and returns it for wrapping by WithPrintedErrors.
func PrintAndReturnError(printer *Printer, err error) error {
	printer.PrintError(err)

	return err
}

// IsPrintedError reports whether err has already been printed to stderr.
func IsPrintedError(err error) bool {
	var pe *printedError

	return errors.As(err, &pe)
}
