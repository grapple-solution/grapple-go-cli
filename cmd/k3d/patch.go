/*
Copyright © 2023 Grapple Solutions
*/
package k3d

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	apiv1 "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// PatchCmd represents the patch command
var PatchCmd = &cobra.Command{
	Use:   "patch",
	Short: "Patch DNS configuration for k3d cluster",
	Long: `Configures local DNS settings to resolve grpl-k3d.dev domain to your k3d cluster IP.
This is required for proper functioning of Grapple on k3d.`,
	RunE: runPatchDNS,
}

func init() {
	PatchCmd.Flags().BoolVar(&autoConfirm, "auto-confirm", false, "Skip confirmation prompts (default: false)")
}

func runPatchDNS(cmd *cobra.Command, args []string) error {
	// Setup logging
	logFileName := "grpl_k3d_patch.log"
	logFilePath := utils.GetLogFilePath(logFileName)
	logFile, _, logOnCliAndFileStart := utils.GetLogWriters(logFilePath)

	var err error

	defer func() {
		logFile.Sync()
		logFile.Close()
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to patch DNS, please run cat %s for more details", logFilePath))
		}
	}()

	logOnCliAndFileStart()

	restConfig, _, err := utils.GetKubernetesConfig()
	if err != nil {
		return fmt.Errorf("failed to get kubernetes config: %w", err)
	}

	clusterIP, err = utils.GetClusterExternalIP(restConfig, "traefik")
	if err != nil {
		return fmt.Errorf("failed to get k3d cluster IP: %w", err)
	}

	// Patch CoreDNS
	if err := patchCoreDNS(restConfig); err != nil {
		return fmt.Errorf("failed to patch CoreDNS: %w", err)
	}

	// Setup DNS with dnsmasq
	if err := setupDnsWithDnsmasq(); err != nil {
		return fmt.Errorf("failed to setup DNS with dnsmasq: %w", err)
	}

	utils.SuccessMessage("DNS patched successfully")
	return nil
}

func setupDnsWithDnsmasq() error {
	// Check and install dnsmasq if needed
	if err := utils.InstallDnsmasq(); err != nil {
		return fmt.Errorf("failed to check/install dnsmasq: %w", err)
	}

	// Configure DNS based on OS
	osType := runtime.GOOS
	switch osType {
	case "linux":
		if err := configureDNSForLinux(); err != nil {
			return fmt.Errorf("failed to configure DNS for Linux: %w", err)
		}
	case "darwin":
		if err := configureDNSForMacOS(); err != nil {
			return fmt.Errorf("failed to configure DNS for macOS: %w", err)
		}
	default:
		return fmt.Errorf("unsupported operating system: %s", osType)
	}

	return nil
}

func patchCoreDNS(restConfig *rest.Config) error {
	// Create Kubernetes client from restConfig
	kubeClient, err := apiv1.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Check if coredns deployment is ready
	utils.InfoMessage("Checking if CoreDNS deployment is ready...")

	// Wait for CoreDNS deployment to be ready
	err = utils.WaitForDeployment(kubeClient, "kube-system", "coredns")
	if err != nil {
		return fmt.Errorf("failed to wait for CoreDNS deployment: %w", err)
	}
	utils.SuccessMessage("CoreDNS deployment is ready")

	// Variables
	namespace := "kube-system"

	// Create custom CoreDNS ConfigMap
	dockerAPIGateway := clusterIP
	if dockerAPIGateway == "" {
		return fmt.Errorf("failed to patch CoreDNS ConfigMap: cluster IP is empty")
	}

	if grappleDNS == "" {
		grappleDNS = "grpl-k3d.dev"
	}
	// Get the path to the coredns-custom.yaml file
	resourcePath, err := utils.GetResourcePath("files")
	if err != nil {
		return fmt.Errorf("failed to get resource path: %w", err)
	}
	// resourcePath := "files"

	// Read the ConfigMap yaml file
	configMapPath := path.Join(resourcePath, "coredns-custom.yaml")
	yamlFile, err := os.ReadFile(configMapPath)
	if err != nil {
		return fmt.Errorf("failed to read coredns-custom.yaml: %w", err)
	}

	// Replace the placeholder with the actual Docker API Gateway IP
	yamlContent := string(yamlFile)
	yamlContent = strings.ReplaceAll(yamlContent, "$DOCKER_API_GATEWAY", dockerAPIGateway)

	// Parse the YAML into a ConfigMap object
	decoder := k8syaml.NewYAMLOrJSONDecoder(strings.NewReader(yamlContent), 100)
	customConfigMap := &corev1.ConfigMap{}
	if err := decoder.Decode(customConfigMap); err != nil {
		return fmt.Errorf("failed to decode coredns-custom.yaml: %w", err)
	}

	// Apply the ConfigMap using the Kubernetes API
	_, err = kubeClient.CoreV1().ConfigMaps(namespace).Get(context.TODO(), customConfigMap.Name, v1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			// Create if not exists
			_, err = kubeClient.CoreV1().ConfigMaps(namespace).Create(context.TODO(), customConfigMap, v1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create coredns-custom ConfigMap: %w", err)
			}
		} else {
			return fmt.Errorf("failed to check if coredns-custom ConfigMap exists: %w", err)
		}
	} else {
		// Update if exists
		_, err = kubeClient.CoreV1().ConfigMaps(namespace).Update(context.TODO(), customConfigMap, v1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update coredns-custom ConfigMap: %w", err)
		}
	}

	// Restart CoreDNS deployment
	_, err = kubeClient.AppsV1().Deployments(namespace).Patch(
		context.TODO(),
		"coredns",
		types.StrategicMergePatchType,
		[]byte(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"`+time.Now().Format(time.RFC3339)+`"}}}}}`),
		v1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to restart CoreDNS deployment: %w", err)
	}

	utils.SuccessMessage("Successfully applied ConfigMap coredns-custom")
	return nil
}

// checkAndAddLineToFile checks if the given content (line) is already present in the file at filePath (ignoring commented lines).
// If not present, it appends the content to the file.
func checkAndAddLineToFile(filePath string, content string) error {
	var lines []string
	// Read existing file if it exists
	if data, err := os.ReadFile(filePath); err == nil {
		existingLines := strings.Split(string(data), "\n")
		for _, line := range existingLines {
			lines = append(lines, line)
		}
	}
	// Check if content is already present (ignoring commented lines)
	found := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == content {
			found = true
			break
		}
	}
	// Build new file content
	var builder strings.Builder
	for _, line := range lines {
		if line != "" {
			builder.WriteString(line + "\n")
		}
	}
	if !found {
		builder.WriteString(content + "\n")
	}
	// Write back to file
	if err := os.WriteFile(filePath, []byte(builder.String()), 0644); err != nil {
		return fmt.Errorf("failed to update %s: %w", filePath, err)
	}
	return nil
}

func configureDNSForLinux() error {
	// Define file paths and content variables
	resolvPath := "/tmp/resolv.conf"
	dnsmasqPath := "/tmp/dnsmasq.conf"
	nmConfPath := "/tmp/dns-local.conf"

	localDNSServer := "127.0.0.1"
	googleDNSServer := "8.8.8.8"
	googleAltDNSServer := "8.8.4.4"
	dnsDomain := "grpl-k3d.dev"

	// Ensure both nameservers are present using the helper
	if err := checkAndAddLineToFile(resolvPath, "nameserver "+localDNSServer); err != nil {
		return err
	}
	if err := checkAndAddLineToFile(resolvPath, "nameserver "+googleDNSServer); err != nil {
		return err
	}

	// Ensure each dnsmasq.conf line is present using the helper
	dnsmasqLine1 := "listen-address=" + localDNSServer
	dnsmasqLine2 := "server=" + googleDNSServer
	dnsmasqLine3 := "server=" + googleAltDNSServer
	dnsmasqLine4 := "address=/" + dnsDomain + "/" + localDNSServer

	if err := checkAndAddLineToFile(dnsmasqPath, dnsmasqLine1); err != nil {
		return fmt.Errorf("failed to update dnsmasq.conf: %w", err)
	}
	if err := checkAndAddLineToFile(dnsmasqPath, dnsmasqLine2); err != nil {
		return fmt.Errorf("failed to update dnsmasq.conf: %w", err)
	}
	if err := checkAndAddLineToFile(dnsmasqPath, dnsmasqLine3); err != nil {
		return fmt.Errorf("failed to update dnsmasq.conf: %w", err)
	}
	if err := checkAndAddLineToFile(dnsmasqPath, dnsmasqLine4); err != nil {
		return fmt.Errorf("failed to update dnsmasq.conf: %w", err)
	}

	// Ensure NetworkManager DNS configuration lines are present using the helper
	nmLine1 := "[main]"
	nmLine2 := "dns=dnsmasq"
	if err := checkAndAddLineToFile(nmConfPath, nmLine1); err != nil {
		return fmt.Errorf("failed to update NetworkManager DNS config: %w", err)
	}
	if err := checkAndAddLineToFile(nmConfPath, nmLine2); err != nil {
		return fmt.Errorf("failed to update NetworkManager DNS config: %w", err)
	}

	// Display commands to be executed
	commandsToRun := "sudo cp /tmp/resolv.conf /etc/resolv.conf && sudo cp /tmp/dnsmasq.conf /etc/dnsmasq.conf && sudo mkdir -p /etc/NetworkManager/conf.d && sudo cp /tmp/dns-local.conf /etc/NetworkManager/conf.d/dns-local.conf"
	utils.InfoMessage("Going to run following commands:")
	fmt.Println(commandsToRun)

	// If not auto-confirm, prompt for confirmation
	if !autoConfirm {
		confirmed, err := utils.PromptInput("Proceed with DNS configuration? (y/N): ", "n", "^[yYnN]$")
		if err != nil {
			return fmt.Errorf("failed to get confirmation: %w", err)
		}
		if strings.ToLower(confirmed) != "y" {
			return fmt.Errorf("grapple cannot be installed without DNS configuration")
		}
	}

	// Execute the commands
	if err := exec.Command("sudo", "rm", "/etc/resolv.conf").Run(); err != nil {
		return fmt.Errorf("failed to remove existing resolv.conf: %w", err)
	}
	if err := exec.Command("sudo", "cp", resolvPath, "/etc/resolv.conf").Run(); err != nil {
		return fmt.Errorf("failed to copy resolv.conf: %w", err)
	}
	if err := exec.Command("sudo", "cp", dnsmasqPath, "/etc/dnsmasq.conf").Run(); err != nil {
		return fmt.Errorf("failed to copy dnsmasq.conf: %w", err)
	}
	if err := exec.Command("sudo", "mkdir", "-p", "/etc/NetworkManager/conf.d").Run(); err != nil {
		return fmt.Errorf("failed to create NetworkManager conf.d directory: %w", err)
	}
	if err := exec.Command("sudo", "cp", nmConfPath, "/etc/NetworkManager/conf.d/dns-local.conf").Run(); err != nil {
		return fmt.Errorf("failed to copy NetworkManager DNS config: %w", err)
	}

	// Restart services
	if err := exec.Command("sudo", "systemctl", "stop", "systemd-resolved").Run(); err != nil {
		utils.InfoMessage("Failed to stop systemd-resolved, continuing anyway")
	}

	if err := exec.Command("sudo", "systemctl", "restart", "dnsmasq").Run(); err != nil {
		utils.InfoMessage("Failed to restart dnsmasq, please retry, if error presist then please restart your system and try again")
		return fmt.Errorf("failed to restart dnsmasq: %w", err)
	}
	if err := exec.Command("sudo", "systemctl", "enable", "dnsmasq").Run(); err != nil {
		return fmt.Errorf("failed to enable dnsmasq: %w", err)
	}

	return nil
}

func configureDNSForMacOS() error {
	// Define file paths and content variables
	dnsmasqTmpPath := "/tmp/dnsmasq.conf"
	resolverTmpPath := "/tmp/resolver-grpl-k3d.dev"
	dnsDomain := "grpl-k3d.dev"
	localDNSServer := "127.0.0.1"
	googleDNSServer := "8.8.8.8"
	googleAltDNSServer := "8.8.4.4"
	dnsmasqPort := "5353"

	// Ensure each dnsmasq.conf line is present using the helper
	dnsmasqLine1 := "listen-address=" + localDNSServer
	dnsmasqLine2 := "server=" + googleDNSServer
	dnsmasqLine3 := "server=" + googleAltDNSServer
	dnsmasqLine4 := "address=/" + dnsDomain + "/" + localDNSServer
	dnsmasqLine5 := "port=" + dnsmasqPort

	if err := checkAndAddLineToFile(dnsmasqTmpPath, dnsmasqLine1); err != nil {
		return fmt.Errorf("failed to update dnsmasq.conf: %w", err)
	}
	if err := checkAndAddLineToFile(dnsmasqTmpPath, dnsmasqLine2); err != nil {
		return fmt.Errorf("failed to update dnsmasq.conf: %w", err)
	}
	if err := checkAndAddLineToFile(dnsmasqTmpPath, dnsmasqLine3); err != nil {
		return fmt.Errorf("failed to update dnsmasq.conf: %w", err)
	}
	if err := checkAndAddLineToFile(dnsmasqTmpPath, dnsmasqLine4); err != nil {
		return fmt.Errorf("failed to update dnsmasq.conf: %w", err)
	}
	if err := checkAndAddLineToFile(dnsmasqTmpPath, dnsmasqLine5); err != nil {
		return fmt.Errorf("failed to update dnsmasq.conf: %w", err)
	}

	// Get homebrew prefix dynamically
	homebrewPrefix, err := exec.Command("brew", "--prefix").Output()
	if err != nil {
		return fmt.Errorf("failed to get homebrew prefix: %w", err)
	}
	brewPrefix := strings.TrimSpace(string(homebrewPrefix))
	dnsmasqPath := fmt.Sprintf("%s/etc/dnsmasq.conf", brewPrefix)

	// Ensure resolver file lines are present using the helper
	resolverLine1 := "nameserver " + localDNSServer
	resolverLine2 := "port " + dnsmasqPort
	if err := checkAndAddLineToFile(resolverTmpPath, resolverLine1); err != nil {
		return fmt.Errorf("failed to update resolver file: %w", err)
	}
	if err := checkAndAddLineToFile(resolverTmpPath, resolverLine2); err != nil {
		return fmt.Errorf("failed to update resolver file: %w", err)
	}

	// Display commands to be executed
	commandsToRun := fmt.Sprintf("sudo cp /tmp/dnsmasq.conf %s && brew services restart dnsmasq && sudo mkdir -p /etc/resolver && sudo cp /tmp/resolver-grpl-k3d.dev /etc/resolver/grpl-k3d.dev", dnsmasqPath)
	utils.InfoMessage("Going to run following commands:")
	fmt.Println(commandsToRun)

	// If not auto-confirm, prompt for confirmation
	if !autoConfirm {
		confirmed, err := utils.PromptInput("Proceed with DNS configuration? (y/N): ", "n", "^[yYnN]$")
		if err != nil {
			return fmt.Errorf("failed to get confirmation: %w", err)
		}
		if strings.ToLower(confirmed) != "y" {
			return fmt.Errorf("grapple cannot be installed without DNS configuration")
		}
	}

	if err := exec.Command("sudo", "cp", dnsmasqTmpPath, dnsmasqPath).Run(); err != nil {
		return fmt.Errorf("failed to copy dnsmasq.conf to %s: %w", dnsmasqPath, err)
	}

	if err := exec.Command("sudo", "mkdir", "-p", "/etc/resolver").Run(); err != nil {
		return fmt.Errorf("failed to create resolver directory: %w", err)
	}

	if err := exec.Command("sudo", "cp", resolverTmpPath, "/etc/resolver/grpl-k3d.dev").Run(); err != nil {
		return fmt.Errorf("failed to copy resolver file: %w", err)
	}

	// Restart dnsmasq service
	if err := exec.Command("brew", "services", "restart", "dnsmasq").Run(); err != nil {
		utils.InfoMessage("Failed to restart dnsmasq, please retry, if error persists then please restart your system and try again")
		return fmt.Errorf("failed to restart dnsmasq: %w", err)
	}

	// Add 127.0.0.1 to DNS servers for all network services
	networkServices, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return fmt.Errorf("failed to list network services: %w", err)
	}

	services := strings.Split(string(networkServices), "\n")
	for _, service := range services {
		service = strings.TrimSpace(service)
		if service == "" || strings.Contains(service, "asterisk") {
			continue
		}

		// Get current DNS servers
		out, err := exec.Command("networksetup", "-getdnsservers", service).Output()
		if err != nil {
			utils.InfoMessage(fmt.Sprintf("Failed to get DNS servers for %s: %v", service, err))
			continue
		}
		current := strings.Fields(string(out))
		// If no DNS servers are set, current will contain a message
		if len(current) > 0 && current[0] == "There" {
			current = []string{}
		}

		// Add 127.0.0.1 if not present
		found := false
		for _, dns := range current {
			if dns == "127.0.0.1" {
				found = true
				break
			}
		}
		if !found {
			current = append(current, "127.0.0.1")
		}

		// Set the updated DNS servers
		args := append([]string{"-setdnsservers", service}, current...)
		if err := exec.Command("networksetup", args...).Run(); err != nil {
			utils.InfoMessage(fmt.Sprintf("Failed to set DNS server for %s: %v", service, err))
		} else {
			utils.InfoMessage(fmt.Sprintf("Updated DNS servers for %s: %v", service, current))
		}
	}

	return nil
}
