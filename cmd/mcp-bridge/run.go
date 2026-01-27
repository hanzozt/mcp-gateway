package main

import (
	"context"
	"errors"
	"os/signal"
	"syscall"

	"github.com/michaelquigley/df/dl"
	"github.com/openziti/mcp-gateway/bridge"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newRunCommand().cmd)
}

type runCommand struct {
	args       []string
	env        []string
	workingDir string
	shareToken string
	cmd        *cobra.Command
}

func newRunCommand() *runCommand {
	cmd := &cobra.Command{
		Use:   "run <command>",
		Short: "run the mcp bridge",
		Args:  cobra.ExactArgs(1),
	}
	command := &runCommand{cmd: cmd}
	cmd.Flags().StringArrayVar(&command.args, "args", nil, "Arguments to pass to the command (can be specified multiple times)")
	cmd.Flags().StringArrayVar(&command.env, "env", nil, "Environment variables in KEY=VALUE format (can be specified multiple times)")
	cmd.Flags().StringVar(&command.workingDir, "working-dir", "", "Working directory for the command")
	cmd.Flags().StringVar(&command.shareToken, "share-token", "", "Pre-created zrok share token (managed mode)")
	cmd.Run = command.run
	return command
}

func (c *runCommand) run(_ *cobra.Command, args []string) {
	command := args[0]

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// parse environment variables
	env := make(map[string]string)
	for _, e := range c.env {
		for i := 0; i < len(e); i++ {
			if e[i] == '=' {
				env[e[:i]] = e[i+1:]
				break
			}
		}
	}

	cfg := &bridge.Config{
		Command:    command,
		Args:       c.args,
		Env:        env,
		WorkingDir: c.workingDir,
		ShareToken: c.shareToken,
	}

	b, err := bridge.New(cfg)
	if err != nil {
		dl.Fatalf("failed to create bridge: %v", err)
	}

	if err := b.Start(ctx); err != nil {
		dl.Fatalf("failed to start: %v", err)
	}
	defer b.Stop()

	if err := b.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		dl.Fatalf("error: %v", err)
	}
}
