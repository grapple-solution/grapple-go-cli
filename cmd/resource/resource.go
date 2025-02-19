/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package resource

import (
	"fmt"

	"github.com/spf13/cobra"
)

// resourceCmd represents the resource command
var ResourceCmd = &cobra.Command{
	Use:   "resource",
	Short: "Manage GrappleApplicationSet resources",
	Long: `The resource command allows you to work with GrappleApplicationSet resources.

You can use this command to:
- Render a GrappleApplicationSet resource without deploying it
- Deploy a GrappleApplicationSet resource to your cluster

Use the subcommands to perform specific actions on resources.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Use --help to see available subcommands")
	},
}

func init() {
	ResourceCmd.AddCommand(DeployCmd)
	ResourceCmd.AddCommand(RenderCmd)
	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// resourceCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// resourceCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
