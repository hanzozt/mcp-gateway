package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newCreateCommand().cmd)
}

type createCommand struct {
	cmd *cobra.Command
}

func newCreateCommand() *createCommand {
	cmd := &cobra.Command{
		Use:   "create [<name>]",
		Short: "create a reserved zrok private share (deprecated)",
		Args:  cobra.MaximumNArgs(1),
	}
	command := &createCommand{cmd: cmd}
	cmd.Run = command.run
	return command
}

func (cmd *createCommand) run(_ *cobra.Command, args []string) {
	fmt.Println("this command has been removed. use 'zrok2 create share' instead.")
	fmt.Println()
	fmt.Println("example:")
	fmt.Println("  zrok2 create share my-gateway")
}
