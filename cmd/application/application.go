/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package application

import (
	"fmt"

	"github.com/spf13/cobra"
)

// applicationCmd represents the application command
var ApplicationCmd = &cobra.Command{
	Use:   "application",
	Short: "Initialize and manage Grapple applications",
	Long: `The application command allows you to initialize and manage Grapple applications.
It provides functionality to create new projects from templates and set up development environments.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("application called")
	},
}

func init() {

	ApplicationCmd.AddCommand(InitCmd)
	ApplicationCmd.AddCommand(UpdateCmd)
	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// applicationCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// applicationCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
