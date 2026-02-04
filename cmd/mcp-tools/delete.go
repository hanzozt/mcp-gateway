package main

import (
	"fmt"

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
		Use:   "delete",
		Short: "delete a reserved zrok private share (deprecated)",
	}
	command := &deleteCommand{cmd: cmd}
	cmd.Run = command.run
	return command
}

func (cmd *deleteCommand) run(_ *cobra.Command, args []string) {
	fmt.Println("this command has been removed. use 'zrok2 delete share' instead.")
	fmt.Println()
	fmt.Println("example:")
	fmt.Println("  zrok2 delete share my-gateway")
}
