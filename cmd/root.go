package cmd

import (
	"fmt"
	"os"

	"github.com/grapple-solution/grapple_cli/cmd/application"
	"github.com/grapple-solution/grapple_cli/cmd/civo" // Import the civo package
	"github.com/grapple-solution/grapple_cli/cmd/dev"
	"github.com/grapple-solution/grapple_cli/cmd/example" // Import the example package
	"github.com/grapple-solution/grapple_cli/cmd/k3d"
	"github.com/grapple-solution/grapple_cli/cmd/resource"
	"github.com/grapple-solution/grapple_cli/cmd/version"
	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "grapple",
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
	rootCmd.AddCommand(k3d.K3dCmd)
	rootCmd.AddCommand(example.ExampleCmd)
	rootCmd.AddCommand(resource.ResourceCmd)
	rootCmd.AddCommand(application.ApplicationCmd)
	rootCmd.AddCommand(dev.DevCmd)
	rootCmd.AddCommand(version.VersionCmd)
}
