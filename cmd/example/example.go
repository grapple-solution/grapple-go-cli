/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package example

import (
	"fmt"

	"github.com/spf13/cobra"
)

// exampleCmd represents the example command
var ExampleCmd = &cobra.Command{
	Use:     "example",
	Aliases: []string{"e"},
	Short:   "example deployment operations",
	Long:    "Commands related to example deployment operations",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("example called")
	},
}

func init() {
	ExampleCmd.AddCommand(DeployCmd)
}
