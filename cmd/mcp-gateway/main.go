package main

import (
	"github.com/michaelquigley/df/dl"
	"github.com/hanzozt/mcp-gateway/build"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:     "mcp-gateway",
	Short:   "Aggregate and serve MCP tools via zrok",
	Version: build.String(),
}

func main() {
	dl.Init(dl.DefaultOptions().SetTrimPrefix("github.com/hanzozt/"))
	if err := rootCmd.Execute(); err != nil {
		dl.Fatalf(err)
	}
}
