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
	Use:                "dev",
	Aliases:            []string{"d"},
	Short:              "Development commands for Grapple",
	DisableFlagParsing: true,
	Long: `Development commands for Grapple including:
- grapple dev: Start development environment
- grapple dev ns: Set namespace
- grapple dev enter [grapi|gruim]: Enter container
- grapple dev logs: View logs (passes through to devspace logs)
- grapple dev logs --all: View logs from all devspace pods using stern
`,
	RunE: runDev,
}

func init() {
	// No flags needed as we're passing through to devspace
	// Set custom help function
	DevCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printDevHelp()
	})
}

func runDev(cmd *cobra.Command, args []string) error {
	// Check for help flags first
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			printDevHelp()
			return nil
		}
	}

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

	// Handle different command scenarios
	if len(args) == 0 {
		return runDevspace()
	}

	if args[0] == "ns" {
		return handleNamespace(args[1:])
	}

	if args[0] == "logs" {
		return handleLogs(args[1:])
	}

	if len(args) == 2 && args[0] == "enter" && (args[1] == "grapi" || args[1] == "gruim") {
		return handleEnter(args[1])
	}

	// Pass through all other commands to devspace
	return runDevspaceWithArgs(args)
}

func printDevHelp() {
	fmt.Println("Development commands for Grapple including:")
	fmt.Println()
	fmt.Println("  grapple dev")
	fmt.Println("    Start development environment")
	fmt.Println()
	fmt.Println("  grapple dev ns [namespace]")
	fmt.Println("    Set or view namespace")
	fmt.Println()
	fmt.Println("  grapple dev enter [grapi|gruim]")
	fmt.Println("    Enter a container (grapi or gruim)")
	fmt.Println()
	fmt.Println("  grapple dev logs")
	fmt.Println("    View logs from a container (interactive container selection)")
	fmt.Println()
	fmt.Println("  grapple dev logs --all")
	fmt.Println("    View logs from all devspace pods using stern")
	fmt.Println()
	fmt.Println("  grapple dev logs [devspace logs options]")
	fmt.Println("    Pass through to devspace logs with any devspace logs options")
	fmt.Println()
	fmt.Println("  grapple dev [other devspace commands]")
	fmt.Println("    Pass through to devspace with any devspace command")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  grapple dev [command] [flags]")
	fmt.Println()
	fmt.Println("Aliases:")
	fmt.Println("  dev, d")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  -h, --help   help for dev")
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
	// If no namespace provided, show help
	if len(args) == 0 {
		nsCmd := exec.Command("devspace", "use", "namespace", "--help")
		nsCmd.Stdout = os.Stdout
		nsCmd.Stderr = os.Stderr
		if err := nsCmd.Run(); err != nil {
			utils.ErrorMessage(fmt.Sprintf("error running namespace command: %v", err))
			return fmt.Errorf("error running namespace command: %w", err)
		}
		return nil
	}

	namespace := args[0]

	// Check if namespace is longer than 10 characters
	if len(namespace) > 10 {
		truncatedNs := namespace[:10]
		fmt.Printf("Warning: Namespace '%s' is longer than 10 characters.\n", namespace)
		fmt.Printf("It will be truncated to '%s'.\n", truncatedNs)

		// Prompt user for confirmation
		confirmed, err := utils.PromptConfirm("Do you want to continue with the truncated namespace?")
		if err != nil {
			return fmt.Errorf("failed to get user confirmation: %w", err)
		}

		if !confirmed {
			fmt.Println("Operation cancelled.")
			return nil
		}

		// Use truncated namespace
		namespace = truncatedNs
	}

	nsArgs := []string{"use", "namespace", namespace}

	// Add any additional args
	if len(args) > 1 {
		nsArgs = append(nsArgs, args[1:]...)
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
func handleLogs(args []string) error {
	// Check if --all flag is present
	hasAllFlag := false
	filteredArgs := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--all" {
			hasAllFlag = true
			continue // Remove --all from args
		}
		filteredArgs = append(filteredArgs, arg)
	}

	// If --all flag is present, use stern instead of devspace logs
	if hasAllFlag {
		// Ensure stern is installed
		if err := utils.InstallStern(); err != nil {
			utils.ErrorMessage(fmt.Sprintf("failed to install stern: %v", err))
			return fmt.Errorf("failed to install stern: %w", err)
		}

		// Get current namespace
		namespace, err := getCurrentNamespace()
		if err != nil {
			return fmt.Errorf("failed to get namespace: %w", err)
		}

		// Run stern command: stern -n <namespace> 'devspace.*'
		sternCmd := exec.Command("stern", "-n", namespace, "devspace.*", "--template={{color .ContainerColor .ContainerName}} ▶ {{.Message}}{{\"\\n\"}}")
		sternCmd.Stdout = os.Stdout
		sternCmd.Stderr = os.Stderr
		if err := sternCmd.Run(); err != nil {
			utils.ErrorMessage(fmt.Sprintf("error running stern: %v", err))
			return fmt.Errorf("error running stern: %w", err)
		}
		return nil
	}

	// Pass everything to devspace logs (let devspace handle container selection)
	logsArgs := append([]string{"logs"}, filteredArgs...)
	logsCmd := exec.Command("devspace", logsArgs...)
	logsCmd.Stdin = os.Stdin // Connect stdin for interactive prompts
	logsCmd.Stdout = os.Stdout
	logsCmd.Stderr = os.Stderr
	if err := logsCmd.Run(); err != nil {
		utils.ErrorMessage(fmt.Sprintf("error running devspace logs: %v", err))
		return fmt.Errorf("error running devspace logs: %w", err)
	}
	return nil
}

func getCurrentNamespace() (string, error) {
	// Try to get namespace from devspace config first
	devspaceCmd := exec.Command("devspace", "print", "namespace")
	output, err := devspaceCmd.Output()
	if err == nil {
		namespace := strings.TrimSpace(string(output))
		if namespace != "" {
			return namespace, nil
		}
	}

	// Fallback to kubectl current context namespace
	kubectlCmd := exec.Command("kubectl", "config", "view", "--minify", "-o", "jsonpath={..namespace}")
	output, err = kubectlCmd.Output()
	if err == nil {
		namespace := strings.TrimSpace(string(output))
		if namespace != "" {
			return namespace, nil
		}
	}

	// Default to "default" namespace
	return "default", nil
}

func runDevspaceWithArgs(args []string) error {
	devCmd := exec.Command("devspace", args...)
	devCmd.Stdin = os.Stdin // Always connect stdin for interactive commands
	devCmd.Stdout = os.Stdout
	devCmd.Stderr = os.Stderr
	if err := devCmd.Run(); err != nil {
		utils.ErrorMessage(fmt.Sprintf("error running devspace command: %v", err))
		return fmt.Errorf("error running devspace command: %w", err)
	}
	return nil
}
