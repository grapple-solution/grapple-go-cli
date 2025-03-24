package utils

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

var OSType = ""

func init() {
	// Check if OSType is set as environment variable
	if envOSType := os.Getenv("OSTYPE"); envOSType != "" {
		OSType = envOSType
	} else {
		// If not set in env, determine from runtime
		switch runtime.GOOS {
		case "darwin":
			OSType = "mac"
		case "linux":
			OSType = "linux"
		case "windows":
			OSType = "windows"
		}
	}
}

// AuthSudo authenticates sudo once to avoid repeated password prompts
func AuthSudo() error {
	if OSType == "windows" {
		return nil // No sudo needed on Windows
	}
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

	InfoMessage("Installing Devspace CLI...")
	var cmd *exec.Cmd
	switch OSType {
	case "mac":
		cmd = exec.Command("brew", "install", "devspace")
	case "linux":
		// Authenticate sudo once before operations that need it
		if err := AuthSudo(); err != nil {
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
	case "windows":
		// Download devspace binary for Windows
		downloadCmd := exec.Command("powershell", "-Command",
			"Invoke-WebRequest -Uri https://github.com/loft-sh/devspace/releases/latest/download/devspace-windows-amd64.exe -OutFile devspace.exe")
		downloadCmd.Stdout = os.Stdout
		StartSpinner("Downloading Devspace CLI, It will take a few minutes...")
		if err := downloadCmd.Run(); err != nil {
			ErrorMessage(fmt.Sprintf("Error downloading devspace: %v", err))
			return fmt.Errorf("error downloading devspace: %w", err)
		}
		StopSpinner()

		// Move to Windows PATH location
		cmd = exec.Command("powershell", "-Command",
			"Move-Item -Force devspace.exe $env:USERPROFILE\\AppData\\Local\\Microsoft\\WindowsApps\\")
	default:
		return fmt.Errorf("unsupported operating system: %s", OSType)
	}

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

	InfoMessage("Installing Task CLI...")

	var cmd *exec.Cmd
	switch OSType {
	case "mac":
		cmd = exec.Command("brew", "install", "go-task/tap/go-task")
	case "linux":
		// Authenticate sudo once before operations that need it
		if err := AuthSudo(); err != nil {
			return err
		}
		cmd = exec.Command("sudo", "snap", "install", "task", "--classic")
	case "windows":
		// Download Task binary for Windows
		downloadCmd := exec.Command("powershell", "-Command",
			"Invoke-WebRequest -Uri https://github.com/go-task/task/releases/latest/download/task_windows_amd64.zip -OutFile task.zip")
		downloadCmd.Stdout = os.Stdout
		StartSpinner("Downloading Task CLI, It will take a few minutes...")
		if err := downloadCmd.Run(); err != nil {
			ErrorMessage(fmt.Sprintf("Error downloading task: %v", err))
			return fmt.Errorf("error downloading task: %w", err)
		}
		StopSpinner()

		// Extract and install
		cmd = exec.Command("powershell", "-Command",
			"Expand-Archive -Path task.zip -DestinationPath $env:USERPROFILE\\AppData\\Local\\Microsoft\\WindowsApps\\ -Force")
	default:
		return fmt.Errorf("unsupported operating system: %s", OSType)
	}

	StartSpinner("Installing Task CLI, It will take a few minutes...")
	if err := cmd.Run(); err != nil {
		ErrorMessage(fmt.Sprintf("Error installing task: %v", err))
		return fmt.Errorf("error installing task: %w", err)
	}
	StopSpinner()
	SuccessMessage("Task CLI installed successfully")
	return nil
}

func InstallYq() error {
	if _, err := exec.LookPath("yq"); err == nil {
		return nil // Already installed
	}

	InfoMessage("Installing Yq CLI...")

	var cmd *exec.Cmd
	switch OSType {
	case "mac":
		cmd = exec.Command("brew", "install", "yq")
	case "linux":
		// Authenticate sudo once before operations that need it
		if err := AuthSudo(); err != nil {
			return err
		}

		// Download yq binary
		downloadCmd := exec.Command("sudo", "wget", "-O", "/usr/bin/yq",
			"https://github.com/mikefarah/yq/releases/latest/download/yq_linux_amd64")
		downloadCmd.Stdout = os.Stdout
		StartSpinner("Downloading Yq CLI, It will take a few minutes...")
		if err := downloadCmd.Run(); err != nil {
			ErrorMessage(fmt.Sprintf("Error downloading yq: %v", err))
			return fmt.Errorf("error downloading yq: %w", err)
		}
		StopSpinner()

		// Set executable permissions
		cmd = exec.Command("sudo", "chmod", "+x", "/usr/bin/yq")
	case "windows":
		// Download yq binary for Windows
		downloadCmd := exec.Command("powershell", "-Command",
			"Invoke-WebRequest -Uri https://github.com/mikefarah/yq/releases/latest/download/yq_windows_amd64.exe -OutFile yq.exe")
		downloadCmd.Stdout = os.Stdout
		StartSpinner("Downloading Yq CLI, It will take a few minutes...")
		if err := downloadCmd.Run(); err != nil {
			ErrorMessage(fmt.Sprintf("Error downloading yq: %v", err))
			return fmt.Errorf("error downloading yq: %w", err)
		}
		StopSpinner()

		// Move to Windows PATH location
		cmd = exec.Command("powershell", "-Command",
			"Move-Item -Force yq.exe $env:USERPROFILE\\AppData\\Local\\Microsoft\\WindowsApps\\")
	default:
		return fmt.Errorf("unsupported operating system: %s", OSType)
	}

	StartSpinner("Installing Yq CLI, It will take a few minutes...")
	if err := cmd.Run(); err != nil {
		ErrorMessage(fmt.Sprintf("Error installing yq: %v", err))
		return fmt.Errorf("error installing yq: %w", err)
	}
	StopSpinner()
	SuccessMessage("Yq CLI installed successfully")
	return nil
}

func InstallK3d() error {
	if _, err := exec.LookPath("k3d"); err == nil {
		return nil // Already installed
	}

	InfoMessage("Installing K3d CLI...")

	var cmd *exec.Cmd
	switch OSType {
	case "mac":
		cmd = exec.Command("brew", "install", "k3d")
	case "linux":
		// Authenticate sudo once before operations that need it
		if err := AuthSudo(); err != nil {
			return err
		}

		// Use bash to properly handle the pipe with the installation script
		cmd = exec.Command("bash", "-c", "curl -s https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | bash")
	case "windows":
		// Download k3d binary for Windows using the official installation script
		cmd = exec.Command("powershell", "-Command",
			"Invoke-WebRequest -Uri https://raw.githubusercontent.com/k3d-io/k3d/main/install.ps1 -OutFile install-k3d.ps1; ./install-k3d.ps1")
	default:
		return fmt.Errorf("unsupported operating system: %s", OSType)
	}

	StartSpinner("Installing K3d CLI, It will take a few minutes...")
	if err := cmd.Run(); err != nil {
		ErrorMessage(fmt.Sprintf("Error installing k3d: %v", err))
		return fmt.Errorf("error installing k3d: %w", err)
	}
	StopSpinner()
	SuccessMessage("K3d CLI installed successfully")
	return nil
}

func InstallDnsmasq() error {
	if _, err := exec.LookPath("dnsmasq"); err == nil {
		return nil // Already installed
	}

	InfoMessage("Installing Dnsmasq...")

	var cmd *exec.Cmd
	switch OSType {
	case "mac":
		cmd = exec.Command("brew", "install", "dnsmasq")
	case "linux":
		// Authenticate sudo once before operations that need it
		if err := AuthSudo(); err != nil {
			return err
		}
		cmd = exec.Command("sudo", "apt-get", "install", "-y", "dnsmasq")
	default:
		return fmt.Errorf("unsupported operating system: %s", OSType)
	}

	StartSpinner("Installing Dnsmasq, It will take a few minutes...")
	if err := cmd.Run(); err != nil {
		ErrorMessage(fmt.Sprintf("Error installing dnsmasq: %v", err))
		return fmt.Errorf("error installing dnsmasq: %w", err)
	}
	StopSpinner()
	SuccessMessage("Dnsmasq installed successfully")
	return nil
}
