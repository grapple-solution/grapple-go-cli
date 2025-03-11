package civo

import (
	"github.com/spf13/cobra"
)

// CivoCmd represents the civo command
var CivoCmd = &cobra.Command{
	Use:     "civo",
	Aliases: []string{"c"},
	Short:   "Civo cloud operations",
	Long:    "Commands related to operations on the Civo cloud platform.",
}

func init() {
	// Initialize subcommands for civo
	CivoCmd.AddCommand(CreateCmd)
	CivoCmd.AddCommand(InstallCmd)
	CivoCmd.AddCommand(CreateInstallCmd)
	CivoCmd.AddCommand(ConnectCmd)
	CivoCmd.AddCommand(UninstallCmd)
	CivoCmd.AddCommand(RemoveCmd)
}
