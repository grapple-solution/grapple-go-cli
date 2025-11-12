package utils

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const (
	brewPackageManager  = "brew"
	aptPackageManager   = "apt"
	dnfPackageManager   = "dnf"
	chocoPackageManager = "choco"

	linuxOS   = "linux"
	darwinOS  = "darwin"
	windowsOS = "windows"
)

var PackageManager = ""
var messagedPrinted = false

func init() {
	// Check if PackageManager is set as environment variable
	if envPackageManager := os.Getenv("PACKAGE_MANAGER"); envPackageManager != "" {
		PackageManager = envPackageManager
	} else {
		// If not set in env, determine default based on OS
		switch runtime.GOOS {
		case darwinOS:
			PackageManager = brewPackageManager
		case linuxOS:
			// Try to detect package manager
			if _, err := exec.LookPath(aptPackageManager); err == nil {
				PackageManager = aptPackageManager
			} else if _, err := exec.LookPath(dnfPackageManager); err == nil {
				PackageManager = dnfPackageManager
			}
		case windowsOS:
			PackageManager = chocoPackageManager
		}

	}
}

func displayPackageInstallerMessage() {
	if !messagedPrinted {
		if os.Getenv("PACKAGE_MANAGER") != "" {
			InfoMessage(fmt.Sprintf("PACKAGE_MANAGER is set to '%s'.", PackageManager))
		} else {
			InfoMessage(fmt.Sprintf("PACKAGE_MANAGER not set, will be using detected '%s'. You can set PACKAGE_MANAGER env var to: brew, apt, dnf, or choco for specific package manager. Note: The package manager you specify must be installed on your system.", PackageManager))
		}
		messagedPrinted = true
	}
}

func InstallDevspace() error {
	if _, err := exec.LookPath("devspace"); err == nil {
		return nil // Already installed
	}

	defer StopSpinner()

	displayPackageInstallerMessage()
	InfoMessage("Installing Devspace CLI...")
	var cmd *exec.Cmd

	switch PackageManager {
	case brewPackageManager:
		cmd = exec.Command(brewPackageManager, "install", "devspace")
	case aptPackageManager, dnfPackageManager:

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
	case chocoPackageManager:
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
		return fmt.Errorf("unsupported package manager: %s", PackageManager)
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

	displayPackageInstallerMessage()
	InfoMessage("Installing Task CLI...")
	var cmd *exec.Cmd
	switch PackageManager {
	case brewPackageManager:
		cmd = exec.Command(brewPackageManager, "install", "go-task/tap/go-task")
	case aptPackageManager, dnfPackageManager:

		cmd = exec.Command("sh", "-c", `
		curl -sL https://github.com/go-task/task/releases/latest/download/task_linux_amd64.tar.gz | \
		tar xz -C /tmp && \
		sudo mv /tmp/task /usr/local/bin/
	  `)
	case chocoPackageManager:
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
		return fmt.Errorf("unsupported package manager: %s", PackageManager)
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

	displayPackageInstallerMessage()
	InfoMessage("Installing Yq CLI...")
	var cmd *exec.Cmd
	switch PackageManager {
	case brewPackageManager:
		cmd = exec.Command(brewPackageManager, "install", "yq")
	case aptPackageManager, dnfPackageManager:

		// Download yq binary using curl
		downloadCmd := exec.Command("sh", "-c", `
			sudo curl -sL https://github.com/mikefarah/yq/releases/latest/download/yq_linux_amd64 -o /usr/bin/yq
		`)
		downloadCmd.Stdout = os.Stdout
		StartSpinner("Downloading Yq CLI, It will take a few minutes...")
		if err := downloadCmd.Run(); err != nil {
			ErrorMessage(fmt.Sprintf("Error downloading yq: %v", err))
			return fmt.Errorf("error downloading yq: %w", err)
		}
		StopSpinner()

		// Set executable permissions
		cmd = exec.Command("sudo", "chmod", "+x", "/usr/bin/yq")
	case chocoPackageManager:
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
		return fmt.Errorf("unsupported package manager: %s", PackageManager)
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

	displayPackageInstallerMessage()
	InfoMessage("Installing K3d CLI...")
	var cmd *exec.Cmd
	switch PackageManager {
	case brewPackageManager:
		cmd = exec.Command(brewPackageManager, "install", "k3d")
	case aptPackageManager, dnfPackageManager:

		cmd = exec.Command("bash", "-c", "curl -s https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | bash")
	case chocoPackageManager:
		cmd = exec.Command("powershell", "-Command",
			"Invoke-WebRequest -Uri https://raw.githubusercontent.com/k3d-io/k3d/main/install.ps1 -OutFile install-k3d.ps1; ./install-k3d.ps1")
	default:
		return fmt.Errorf("unsupported package manager: %s", PackageManager)
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

	displayPackageInstallerMessage()
	var cmd *exec.Cmd
	switch PackageManager {
	case brewPackageManager:
		cmd = exec.Command(brewPackageManager, "install", "dnsmasq")
	case aptPackageManager:

		cmd = exec.Command("sudo", aptPackageManager, "install", "-y", "dnsmasq")
	case dnfPackageManager:

		cmd = exec.Command("sudo", dnfPackageManager, "install", "-y", "dnsmasq")
	case chocoPackageManager:
		cmd = exec.Command(chocoPackageManager, "install", "-y", "dnsmasq")
	default:
		return fmt.Errorf("unsupported package manager: %s", PackageManager)
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
func InstallMkcert() error {
	if _, err := exec.LookPath("mkcert"); err == nil {
		return nil // Already installed
	}

	displayPackageInstallerMessage()
	InfoMessage("Installing Mkcert...")

	var err error
	switch PackageManager {
	case brewPackageManager:
		// Install mkcert
		cmd := exec.Command(brewPackageManager, "install", "mkcert")
		if err = cmd.Run(); err != nil {
			ErrorMessage(fmt.Sprintf("Error installing mkcert: %v", err))
			return fmt.Errorf("error installing mkcert: %w", err)
		}

		// Install nss
		cmd = exec.Command(brewPackageManager, "install", "nss")
		if err = cmd.Run(); err != nil {
			ErrorMessage(fmt.Sprintf("Error installing nss: %v", err))
			return fmt.Errorf("error installing nss: %w", err)
		}

	case aptPackageManager, dnfPackageManager:
		// Check if brew is available
		if _, err := exec.LookPath("brew"); err != nil {
			ErrorMessage("Mkcert can only be installed using Homebrew. Please install Homebrew first.")
			return fmt.Errorf("brew is required to install mkcert")
		}
		InfoMessage("Mkcert can only be installed using Homebrew, proceeding with brew installation...")

		// Install mkcert
		cmd := exec.Command("brew", "install", "mkcert")
		if err = cmd.Run(); err != nil {
			ErrorMessage(fmt.Sprintf("Error installing mkcert: %v", err))
			return fmt.Errorf("error installing mkcert: %w", err)
		}

		// Install nss
		cmd = exec.Command("brew", "install", "nss")
		if err = cmd.Run(); err != nil {
			ErrorMessage(fmt.Sprintf("Error installing nss: %v", err))
			return fmt.Errorf("error installing nss: %w", err)
		}

	case chocoPackageManager:
		cmd := exec.Command(chocoPackageManager, "install", "-y", "mkcert")
		StartSpinner("Installing Mkcert, It will take a few minutes...")
		if err = cmd.Run(); err != nil {
			ErrorMessage(fmt.Sprintf("Error installing mkcert: %v", err))
			return fmt.Errorf("error installing mkcert: %w", err)
		}
		StopSpinner()

	default:
		return fmt.Errorf("unsupported package manager: %s", PackageManager)
	}

	SuccessMessage("Mkcert installed successfully")
	return nil
}

func InstallStern() error {
	if _, err := exec.LookPath("stern"); err == nil {
		return nil // Already installed
	}

	defer StopSpinner()

	displayPackageInstallerMessage()
	InfoMessage("Installing Stern CLI...")
	var cmd *exec.Cmd

	switch PackageManager {
	case brewPackageManager:
		cmd = exec.Command(brewPackageManager, "install", "stern")
		StartSpinner("Installing Stern CLI, It will take a few minutes...")
		if err := cmd.Run(); err != nil {
			ErrorMessage(fmt.Sprintf("Error installing stern: %v", err))
			return fmt.Errorf("error installing stern: %w", err)
		}
		StopSpinner()
	case aptPackageManager, dnfPackageManager:
		// Determine architecture
		var arch string
		if runtime.GOARCH == "arm64" {
			arch = "arm64"
		} else {
			arch = "amd64"
		}

		// Get the latest release download URL from GitHub API
		apiURL := "https://api.github.com/repos/stern/stern/releases/latest"
		apiCmd := exec.Command("curl", "-s", apiURL)
		apiOutput, err := apiCmd.Output()
		if err != nil {
			ErrorMessage(fmt.Sprintf("Error fetching stern release info: %v", err))
			return fmt.Errorf("error fetching stern release info: %w", err)
		}

		// Parse JSON to find the correct asset URL
		// We're looking for a line like: "browser_download_url": "https://github.com/stern/stern/releases/download/v1.33.1/stern_1.33.1_linux_amd64.tar.gz"
		pattern := fmt.Sprintf(`stern_.*_linux_%s\.tar\.gz`, arch)
		grepCmd := exec.Command("grep", "-o", fmt.Sprintf(`https://[^"]*%s`, pattern))
		grepCmd.Stdin = strings.NewReader(string(apiOutput))
		downloadURLBytes, err := grepCmd.Output()
		if err != nil {
			ErrorMessage(fmt.Sprintf("Error finding stern download URL: %v", err))
			return fmt.Errorf("error finding stern download URL: %w", err)
		}

		downloadURL := strings.TrimSpace(string(downloadURLBytes))
		if downloadURL == "" {
			ErrorMessage("Could not find stern download URL for your architecture")
			return fmt.Errorf("could not find stern download URL for architecture: %s", arch)
		}

		// Extract filename from URL
		parts := strings.Split(downloadURL, "/")
		tarballName := parts[len(parts)-1]

		// Download stern tarball
		downloadCmd := exec.Command("curl", "-L", "-o", tarballName, downloadURL)
		downloadCmd.Stdout = os.Stdout
		downloadCmd.Stderr = os.Stderr
		StartSpinner("Downloading Stern CLI, It will take a few minutes...")
		if err := downloadCmd.Run(); err != nil {
			ErrorMessage(fmt.Sprintf("Error downloading stern: %v", err))
			return fmt.Errorf("error downloading stern: %w", err)
		}
		StopSpinner()

		// Extract tarball
		extractCmd := exec.Command("tar", "-xzf", tarballName, "stern")
		extractCmd.Stdout = os.Stdout
		extractCmd.Stderr = os.Stderr
		StartSpinner("Extracting Stern CLI...")
		if err := extractCmd.Run(); err != nil {
			ErrorMessage(fmt.Sprintf("Error extracting stern: %v", err))
			// Clean up downloaded tarball
			os.Remove(tarballName)
			return fmt.Errorf("error extracting stern: %w", err)
		}
		StopSpinner()

		// Install binary to /usr/local/bin with correct permissions
		InfoMessage("Installing Stern CLI to /usr/local/bin (requires sudo)...")
		cmd = exec.Command("sudo", "install", "-c", "-m", "0755", "stern", "/usr/local/bin")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			ErrorMessage(fmt.Sprintf("Error installing stern: %v", err))
			// Clean up
			os.Remove(tarballName)
			os.Remove("stern")
			return fmt.Errorf("error installing stern: %w", err)
		}

		// Clean up downloaded files
		os.Remove(tarballName)
		os.Remove("stern")
	case chocoPackageManager:
		// Get the latest release download URL from GitHub API
		apiURL := "https://api.github.com/repos/stern/stern/releases/latest"
		apiCmd := exec.Command("powershell", "-Command",
			fmt.Sprintf("Invoke-RestMethod -Uri %s", apiURL))
		apiOutput, err := apiCmd.Output()
		if err != nil {
			ErrorMessage(fmt.Sprintf("Error fetching stern release info: %v", err))
			return fmt.Errorf("error fetching stern release info: %w", err)
		}

		// Parse JSON to find the correct asset URL for Windows
		pattern := `stern_.*_windows_amd64\.zip`
		grepCmd := exec.Command("powershell", "-Command",
			fmt.Sprintf("$input | Select-String -Pattern '%s' -AllMatches | ForEach-Object { $_.Matches.Value }", pattern))
		grepCmd.Stdin = strings.NewReader(string(apiOutput))
		zipNameBytes, err := grepCmd.Output()
		if err != nil || len(zipNameBytes) == 0 {
			// Fallback to a simple pattern search
			if strings.Contains(string(apiOutput), "windows_amd64.zip") {
				// Extract URL manually
				lines := strings.Split(string(apiOutput), "\n")
				for _, line := range lines {
					if strings.Contains(line, "browser_download_url") && strings.Contains(line, "windows_amd64.zip") {
						// Extract URL from the line
						start := strings.Index(line, "https://")
						if start != -1 {
							end := strings.Index(line[start:], "\"")
							if end != -1 {
								downloadURL := line[start : start+end]
								parts := strings.Split(downloadURL, "/")
								zipName := parts[len(parts)-1]

								// Download stern zip
								downloadCmd := exec.Command("powershell", "-Command",
									fmt.Sprintf("Invoke-WebRequest -Uri '%s' -OutFile %s", downloadURL, zipName))
								downloadCmd.Stdout = os.Stdout
								downloadCmd.Stderr = os.Stderr
								StartSpinner("Downloading Stern CLI, It will take a few minutes...")
								if err := downloadCmd.Run(); err != nil {
									ErrorMessage(fmt.Sprintf("Error downloading stern: %v", err))
									return fmt.Errorf("error downloading stern: %w", err)
								}
								StopSpinner()

								// Extract zip
								extractCmd := exec.Command("powershell", "-Command",
									fmt.Sprintf("Expand-Archive -Force %s -DestinationPath .", zipName))
								extractCmd.Stdout = os.Stdout
								extractCmd.Stderr = os.Stderr
								StartSpinner("Extracting Stern CLI...")
								if err := extractCmd.Run(); err != nil {
									ErrorMessage(fmt.Sprintf("Error extracting stern: %v", err))
									exec.Command("powershell", "-Command", fmt.Sprintf("Remove-Item -Force %s", zipName)).Run()
									return fmt.Errorf("error extracting stern: %w", err)
								}
								StopSpinner()

								// Move to Windows PATH location
								cmd = exec.Command("powershell", "-Command",
									"Move-Item -Force stern.exe $env:USERPROFILE\\AppData\\Local\\Microsoft\\WindowsApps\\")
								StartSpinner("Installing Stern CLI, It will take a few minutes...")
								if err := cmd.Run(); err != nil {
									ErrorMessage(fmt.Sprintf("Error installing stern: %v", err))
									exec.Command("powershell", "-Command", fmt.Sprintf("Remove-Item -Force %s", zipName)).Run()
									exec.Command("powershell", "-Command", "Remove-Item -Force stern.exe").Run()
									return fmt.Errorf("error installing stern: %w", err)
								}
								StopSpinner()

								// Clean up
								exec.Command("powershell", "-Command", fmt.Sprintf("Remove-Item -Force %s", zipName)).Run()
								break
							}
						}
					}
				}
			} else {
				ErrorMessage("Could not find stern download URL for Windows")
				return fmt.Errorf("could not find stern download URL for Windows")
			}
		}
	default:
		return fmt.Errorf("unsupported package manager: %s", PackageManager)
	}

	SuccessMessage("Stern CLI installed successfully")
	return nil
}
