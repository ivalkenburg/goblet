package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "dev"

func init() { registerFlags(rootCmd) }

var rootCmd = &cobra.Command{
	Use:   "goblet [path]",
	Short: "A fast, simple HTTP file server for local development",
	Long: `goblet serves a directory over HTTP, similar to the http-server npm package.

By default it serves the current directory on port 8080 with directory
listing, gzip compression, caching, and full request logging enabled.`,
	Version:       version,
	Args:          cobra.MaximumNArgs(1),
	RunE:          run,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func run(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		cfg.Root = args[0]
	} else {
		cfg.Root = "."
	}
	return Start(&cfg)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
