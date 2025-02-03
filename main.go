/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package main

import (
	"fmt"

	"github.com/grapple-solution/grapple_cli/cmd"
	"github.com/joho/godotenv"
)

func init() {
	// Load .env file
	err := godotenv.Load(".env")
	if err != nil {
		fmt.Println("Error loading .env file")
	}
}

func main() {
	cmd.Execute()
}
