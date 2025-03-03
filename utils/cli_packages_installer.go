package utils

import (
	"fmt"
	"os"
	"os/exec"
)

func InstallDevspace() error {
	// Get current directory
	dir, err := os.Getwd()
	if err != nil {
		ErrorMessage(fmt.Sprintf("Error getting current directory: %v", err))
		return fmt.Errorf("error getting current directory: %w", err)
	}

	// Execute package_installer script with "devspace" argument
	cmd := fmt.Sprintf("bash %s/utils/package_installer devspace", dir)
	if err := exec.Command("bash", "-c", cmd).Run(); err != nil {
		ErrorMessage(fmt.Sprintf("Error installing devspace: %v", err))
		return fmt.Errorf("error installing devspace: %w", err)
	}

	SuccessMessage("Devspace CLI installed successfully")
	return nil
}

func InstallTaskCLI() error {
	// Get current directory
	dir, err := os.Getwd()
	if err != nil {
		ErrorMessage(fmt.Sprintf("Error getting current directory: %v", err))
		return fmt.Errorf("error getting current directory: %w", err)
	}

	// Execute package_installer script with "task" argument
	cmd := fmt.Sprintf("bash %s/utils/package_installer task", dir)
	if err := exec.Command("bash", "-c", cmd).Run(); err != nil {
		ErrorMessage(fmt.Sprintf("Error installing task: %v", err))
		return fmt.Errorf("error installing task: %w", err)
	}

	return nil
}
