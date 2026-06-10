package core

import "github.com/spf13/cobra"

// GroupAnnotationKey marks noun-group commands that only group subcommands.
const GroupAnnotationKey = "rpu-group"

// IsGroupCommand returns true for commands that only exist to group subcommands.
func IsGroupCommand(cmd *cobra.Command) bool {
	_, ok := cmd.Annotations[GroupAnnotationKey]

	return ok
}

// MarkGroupCommand marks cmd as help-only.
func MarkGroupCommand(cmd *cobra.Command) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}

	cmd.Annotations[GroupAnnotationKey] = "true"
	cmd.RunE = func(c *cobra.Command, _ []string) error {
		return c.Help()
	}

	return cmd
}
