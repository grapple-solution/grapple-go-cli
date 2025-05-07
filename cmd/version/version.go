/*
Copyright © 2025 Grapple Solutions
*/
package version

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	// Try to find VERSION file relative to executable
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		rootDir := filepath.Dir(execDir)
		versionPath := filepath.Join(rootDir, "VERSION")

		content, err := os.ReadFile(versionPath)
		if err == nil {
			return strings.TrimSpace(string(content))
		}
	}

	// Fallback: try to find VERSION in current directory or parent directories
	dir, err := os.Getwd()
	if err == nil {
		for i := 0; i < 3; i++ { // Try current dir and up to 2 parent dirs
			versionPath := filepath.Join(dir, "VERSION")
			content, err := os.ReadFile(versionPath)
			if err == nil {
				return strings.TrimSpace(string(content))
			}
			dir = filepath.Dir(dir)
		}
	}

	// If all else fails, return a default version
	return "development"
}

func init() {
	// No flags needed for version command
}
