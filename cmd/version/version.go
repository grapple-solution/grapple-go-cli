/*
Copyright © 2025 Grapple Solutions
*/
package version

import (
	"fmt"

	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
)

// VersionCmd represents the version command
var VersionCmd = &cobra.Command{
	Use:     "version",
	Aliases: []string{"v"},
	Short:   "Display the version of Grapple CLI",
	Long:    `Display the current version of the Grapple CLI tool.`,
	Run: func(cmd *cobra.Command, args []string) {
		version := utils.GetGrappleCliVersion()
		fmt.Printf("Grapple CLI version: %s\n", version)
	},
}

func init() {
	// No flags needed for version command
}
