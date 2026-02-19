package main

import (
	"context"
	"errors"
	"os/signal"
	"syscall"

	"github.com/michaelquigley/df/dl"
	"github.com/hanzozt/mcp-gateway/tools"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newRunCommand().cmd)
}

type runCommand struct {
	cmd *cobra.Command
}

func newRunCommand() *runCommand {
	cmd := &cobra.Command{
		Use:   "run <shareToken>",
		Short: "connect to an mcp gateway share",
		Args:  cobra.ExactArgs(1),
	}
	command := &runCommand{cmd: cmd}
	cmd.Run = command.run
	return command
}

func (cmd *runCommand) run(_ *cobra.Command, args []string) {
	shareToken := args[0]

	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	c, err := tools.New(shareToken)
	if err != nil {
		dl.Fatalf("failed to create client: %v", err)
	}

	if err := c.Start(ctx); err != nil {
		dl.Fatalf("failed to start: %v", err)
	}
	defer c.Stop()

	if err := c.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		dl.Fatalf(err)
	}
}
