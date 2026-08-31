// Package cli implements the Refraict command-line interface.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// global options shared across commands.
var (
	outputDir string
	configPath string
	verbose bool
	flagJSON bool
)

// newRootCmd builds the top-level command tree.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "refraict",
		Short: "Refraict: portable UI/UX screenshot analysis pipeline",
		Long:  "Refraict converts UI screenshots into reusable, structured, AI-friendly context.",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			// nothing heavy here
		},
	}
	root.PersistentFlags().StringVarP(&configPath, "config", "c", "", "path to JSON config file")
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose diagnostics")
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "emit machine-readable JSON output where applicable")

	root.AddCommand(
		newAnalyzeCmd(),
		newOCRCmd(),
		newRegionsCmd(),
		newInspectCmd(),
		newMergeCmd(),
		newSummarizeCmd(),
		newReconstructCmd(),
		newCacheCmd(),
		newBenchmarkCmd(),
		newVersionCmd(),
	)
	return root
}

// Execute runs the CLI and returns an error (text) or sets exit code.
func Execute() error {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return err
	}
	return nil
}

// fatal logs an error to stderr and returns a non-nil error.
func fail(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
