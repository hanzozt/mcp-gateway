package main

import (
	"github.com/michaelquigley/df/dl"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "mcp-bridge",
	Short: "bridge a local stdio mcp server to the network via zrok",
}

func main() {
	dl.Init(dl.DefaultOptions().SetTrimPrefix("github.com/openziti/"))
	if err := rootCmd.Execute(); err != nil {
		dl.Fatalf(err)
	}
}
