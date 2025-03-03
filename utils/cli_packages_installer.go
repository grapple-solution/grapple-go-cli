package utils

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// authSudo authenticates sudo once to avoid repeated password prompts
func authSudo() error {
	InfoMessage("Authenticating sudo for subsequent operations...")
	cmd := exec.Command("sudo", "-v")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		ErrorMessage(fmt.Sprintf("Error authenticating sudo: %v", err))
		return fmt.Errorf("error authenticating sudo: %w", err)
	}
	return nil
}

func InstallDevspace() error {
	if _, err := exec.LookPath("devspace"); err == nil {
		return nil // Already installed
	}

	defer StopSpinner()

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("brew", "install", "devspace")
	case "linux":
		// Authenticate sudo once before operations that need it
		if err := authSudo(); err != nil {
			return err
		}

		// Download devspace binary
		downloadCmd := exec.Command("curl", "-L", "-o", "devspace",
			"https://github.com/loft-sh/devspace/releases/latest/download/devspace-linux-amd64")
		downloadCmd.Stdout = os.Stdout
		StartSpinner("Downloading Devspace CLI, It will take a few minutes...")
		if err := downloadCmd.Run(); err != nil {
			ErrorMessage(fmt.Sprintf("Error downloading devspace: %v", err))
			return fmt.Errorf("error downloading devspace: %w", err)
		}
		StopSpinner()

		// Install binary to /usr/local/bin with correct permissions
		cmd = exec.Command("sudo", "install", "-c", "-m", "0755", "devspace", "/usr/local/bin")
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	cmd.Stdout = os.Stdout
	StartSpinner("Installing Devspace CLI, It will take a few minutes...")
	if err := cmd.Run(); err != nil {
		ErrorMessage(fmt.Sprintf("Error installing devspace: %v", err))
		return fmt.Errorf("error installing devspace: %w", err)
	}
	StopSpinner()

	SuccessMessage("\nDevspace CLI installed successfully")
	return nil
}

func InstallTaskCLI() error {
	if _, err := exec.LookPath("task"); err == nil {
		return nil // Already installed
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("brew", "install", "go-task/tap/go-task")
	case "linux":
		// Authenticate sudo once before operations that need it
		if err := authSudo(); err != nil {
			return err
		}
		cmd = exec.Command("sudo", "snap", "install", "task", "--classic")
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	cmd.Stdout = os.Stdout
	StartSpinner("Installing Task CLI, It will take a few minutes...")
	if err := cmd.Run(); err != nil {
		ErrorMessage(fmt.Sprintf("Error installing task: %v", err))
		return fmt.Errorf("error installing task: %w", err)
	}
	StopSpinner()
	SuccessMessage("Task CLI installed successfully")
	return nil
}
