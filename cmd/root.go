package cmd

import (
	"fmt"
	"os"

	"github.com/grapple-solution/grapple_cli/cmd/civo"    // Import the civo package
	"github.com/grapple-solution/grapple_cli/cmd/example" // Import the example package
	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "grpl",
	Short: "A CLI tool for managing Civo and Kubernetes clusters",
	Long:  "Grapple CLI is a tool for managing cloud and Kubernetes operations.",
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main().
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	// Add the civo command
	rootCmd.AddCommand(civo.CivoCmd)
	rootCmd.AddCommand(example.ExampleCmd)
}
