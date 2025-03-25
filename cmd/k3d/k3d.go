/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package k3d

import (
	"fmt"

	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
)

// k3dCmd represents the k3d command
var K3dCmd = &cobra.Command{
	Use:     "k3d",
	Aliases: []string{"k"},
	Short:   "K3d operations",
	Long:    "Commands related to operations on the K3d platform.",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("k3d called")
	},
}

func init() {
	K3dCmd.AddCommand(CreateCmd)
	K3dCmd.AddCommand(ConnectCmd)
	K3dCmd.AddCommand(InstallCmd)
	K3dCmd.AddCommand(PatchCmd)
	K3dCmd.AddCommand(CreateInstallCmd)
	K3dCmd.AddCommand(RemoveCmd)
	K3dCmd.AddCommand(UninstallCmd)
	utils.InstallK3d()
	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// k3dCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// k3dCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
