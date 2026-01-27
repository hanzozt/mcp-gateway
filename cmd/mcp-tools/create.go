package main

import (
	"fmt"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/df/dl"
	"github.com/openziti/mcp-gateway/model"
	"github.com/openziti/zrok/v2/environment"
	"github.com/openziti/zrok/v2/sdk/golang/sdk"
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
		Short: "create a reserved zrok private share",
		Args:  cobra.MaximumNArgs(1),
	}
	command := &createCommand{cmd: cmd}
	cmd.Run = command.run
	return command
}

func (cmd *createCommand) run(_ *cobra.Command, args []string) {
	root, err := environment.LoadRoot()
	if err != nil {
		dl.Fatalf("failed to load zrok environment: %v", err)
	}

	if !root.IsEnabled() {
		dl.Fatalf("zrok environment is not enabled; run 'zrok enable' first")
	}

	shareReq := &sdk.ShareRequest{
		BackendMode:    sdk.ProxyBackendMode,
		ShareMode:      sdk.PrivateShareMode,
		PermissionMode: sdk.OpenPermissionMode,
	}

	if len(args) > 0 {
		shareReq.PrivateShareToken = args[0]
	}

	shr, err := sdk.CreateShare(root, shareReq)
	if err != nil {
		dl.Fatalf("failed to create share: %v", err)
	}

	dl.Log().With("share_token", shr.Token).Info("created reserved share")

	if json, err := dd.UnbindJSON(&model.TokenOutput{ShareToken: shr.Token}); err == nil {
		fmt.Println(string(json))
	} else {
		dl.Log().With("error", err).Warn("failed to unbind share token")
	}
}
