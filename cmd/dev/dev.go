/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package dev

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
)

// DevCmd represents the dev command
var DevCmd = &cobra.Command{
	Use:     "dev",
	Aliases: []string{"d"},
	Short:   "Development commands for Grapple",
	Long: `Development commands for Grapple including:
- devspace dev: Start development environment
- devspace ns: Set namespace
- devspace enter [grapi|gruim]: Enter container`,
	RunE: runDev,
}

func init() {
	// No flags needed as we're passing through to devspace
}

func runDev(cmd *cobra.Command, args []string) error {
	// Setup logging
	logFileName := "grpl_dev.log"
	logFilePath := utils.GetLogFilePath(logFileName)
	logFile, _, logOnCliAndFileStart := utils.GetLogWriters(logFilePath)

	var err error

	defer func() {
		if syncErr := logFile.Sync(); syncErr != nil {
			fmt.Fprintf(os.Stderr, "Failed to sync log file: %v\n", syncErr)
		}
		if closeErr := logFile.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Failed to close log file: %v\n", closeErr)
		}
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to run dev, please run cat %s for more details", logFilePath))
		}
	}()

	logOnCliAndFileStart()

	if err := utils.InstallDevspace(); err != nil {
		utils.ErrorMessage(fmt.Sprintf("failed to install devspace: %v", err))
		return fmt.Errorf("failed to install devspace: %w", err)
	}

	if err := utils.InstallTaskCLI(); err != nil {
		utils.ErrorMessage(fmt.Sprintf("failed to install task cli: %v", err))
		return fmt.Errorf("failed to install task cli: %w", err)
	}

	if err := utils.InstallYq(); err != nil {
		utils.ErrorMessage(fmt.Sprintf("failed to install yq: %v", err))
		return fmt.Errorf("failed to install yq: %w", err)
	}

	// utils.InfoMessage(fmt.Sprintf("args : %v", args))

	// Handle different command scenarios
	if len(args) == 0 {
		return runDevspace()
	}

	if args[0] == "ns" {
		return handleNamespace(args[1:])
	}

	if len(args) == 2 && args[0] == "enter" && (args[1] == "grapi" || args[1] == "gruim") {
		return handleEnter(args[1])
	}

	// Pass through all other commands to devspace
	return runDevspaceWithArgs(args)
}

func runDevspace() error {
	var devCmd *exec.Cmd
	if runtime.GOOS == "linux" {
		devCmd = exec.Command("devspace", "dev")
		devCmd.Env = append(os.Environ(), "DEVSPACE_LINUX=true")
	} else {
		devCmd = exec.Command("devspace", "dev")
	}
	devCmd.Stdout = os.Stdout
	devCmd.Stderr = os.Stderr
	if err := devCmd.Run(); err != nil {
		utils.ErrorMessage(fmt.Sprintf("error running devspace dev: %v", err))
		return fmt.Errorf("error running devspace dev: %w", err)
	}
	return nil
}

func handleNamespace(args []string) error {
	nsArgs := []string{"use", "namespace"}
	if len(args) == 0 {
		nsArgs = append(nsArgs, "--help")
	} else {
		nsArgs = append(nsArgs, args...)
	}

	nsCmd := exec.Command("devspace", nsArgs...)
	nsCmd.Stdout = os.Stdout
	nsCmd.Stderr = os.Stderr
	if err := nsCmd.Run(); err != nil {
		utils.ErrorMessage(fmt.Sprintf("error running namespace command: %v", err))
		return fmt.Errorf("error running namespace command: %w", err)
	}
	return nil
}

func handleEnter(container string) error {
	labelSelector := fmt.Sprintf("--label-selector=app.kubernetes.io/name=%s", container)

	// Check for environment variables in .bashrc
	envVars := []string{}
	output, err := exec.Command("bash", "-c", "grep grapi_env_var ~/.bashrc").Output()
	if err == nil {
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			if line != "" {
				result := strings.TrimPrefix(line, "grapi_env_var_")
				envVars = append(envVars, result)
			}
		}
	}

	// Build devspace enter command
	enterArgs := []string{"enter", labelSelector}

	// Add environment variables if found
	if len(envVars) > 0 {
		enterArgs = append(enterArgs, "--")
		enterArgs = append(enterArgs, "env")
		enterArgs = append(enterArgs, envVars...)
	}

	// Add appropriate shell
	if container == "grapi" {
		enterArgs = append(enterArgs, "/bin/bash")
	} else {
		enterArgs = append(enterArgs, "/bin/sh")
	}

	// Execute devspace enter command
	enterCmd := exec.Command("devspace", enterArgs...)
	enterCmd.Stdin = os.Stdin
	enterCmd.Stdout = os.Stdout
	enterCmd.Stderr = os.Stderr

	if err := enterCmd.Run(); err != nil {
		utils.ErrorMessage(fmt.Sprintf("error entering container: %v", err))
		return fmt.Errorf("error entering container: %w", err)
	}

	return nil
}

func runDevspaceWithArgs(args []string) error {
	devCmd := exec.Command("devspace", args...)
	devCmd.Stdout = os.Stdout
	devCmd.Stderr = os.Stderr
	if err := devCmd.Run(); err != nil {
		utils.ErrorMessage(fmt.Sprintf("error running devspace command: %v", err))
		return fmt.Errorf("error running devspace command: %w", err)
	}
	return nil
}
