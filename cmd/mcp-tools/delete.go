package main

import (
	"github.com/michaelquigley/df/dl"
	"github.com/openziti/zrok/v2/environment"
	"github.com/openziti/zrok/v2/sdk/golang/sdk"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newDeleteCommand().cmd)
}

type deleteCommand struct {
	cmd *cobra.Command
}

func newDeleteCommand() *deleteCommand {
	cmd := &cobra.Command{
		Use:   "delete <shareToken>",
		Short: "delete a reserved zrok private share",
		Args:  cobra.ExactArgs(1),
	}
	command := &deleteCommand{cmd: cmd}
	cmd.Run = command.run
	return command
}

func (cmd *deleteCommand) run(_ *cobra.Command, args []string) {
	root, err := environment.LoadRoot()
	if err != nil {
		dl.Fatalf("failed to load zrok environment: %v", err)
	}

	if !root.IsEnabled() {
		dl.Fatalf("zrok environment is not enabled; run 'zrok enable' first")
	}

	shr := &sdk.Share{Token: args[0]}
	if err := sdk.DeleteShare(root, shr); err != nil {
		dl.Fatalf("failed to delete share '%s': %v", args[0], err)
	}

	dl.Log().With("share_token", args[0]).Info("deleted share")
}
