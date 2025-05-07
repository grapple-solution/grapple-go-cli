/*
Copyright © 2025 Grapple Solutions
*/
package version

import (
	"fmt"
	"os"
	"strings"

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
		version := getVersion()
		fmt.Printf("Grapple CLI version: %s\n", version)
	},
}

// getVersion reads the version from the VERSION file
func getVersion() string {

	versionPath, err := utils.GetResourcePath("VERSION")
	if err == nil {
		content, err := os.ReadFile(versionPath)
		if err == nil {
			return strings.TrimSpace(string(content))
		}
	}

	return versionPath
}

func init() {
	// No flags needed for version command
}
