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
	logFile, _, logOnCliAndFileStart := utils.GetLogWriters("/tmp/grpl_k3d_patch.log")
	defer logFile.Close()
	logOnCliAndFileStart()

	restConfig, _, err := utils.GetKubernetesConfig()
	if err != nil {
		return fmt.Errorf("failed to get kubernetes config: %w", err)
	}

	clusterIP, err = getClusterExternalIP(restConfig, "kube-system", "traefik")
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
	err = waitForDeployment(kubeClient, "kube-system", "coredns")
	if err != nil {
		return fmt.Errorf("failed to wait for CoreDNS deployment: %w", err)
	}
	utils.SuccessMessage("CoreDNS deployment is ready")

	// Variables
	namespace := "kube-system"
	configMapName := "coredns"

	// Backup the current ConfigMap
	utils.InfoMessage("Backing up the current CoreDNS ConfigMap...")
	configMap, err := kubeClient.CoreV1().ConfigMaps(namespace).Get(context.TODO(), configMapName, v1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get CoreDNS ConfigMap: %w", err)
	}

	// // Create backup file
	// backupData, err := yaml.Marshal(configMap)
	// if err != nil {
	// 	return fmt.Errorf("failed to marshal ConfigMap for backup: %w", err)
	// }

	// if err := os.WriteFile("coredns-backup.yaml", backupData, 0644); err != nil {
	// 	return fmt.Errorf("failed to write backup file: %w", err)
	// }
	// utils.InfoMessage("Backup saved as coredns-backup.yaml")

	// Check if the forward directive is already updated
	corefile := configMap.Data["Corefile"]
	if !strings.Contains(corefile, "forward . 8.8.8.8 8.8.4.4") {
		// Update the ConfigMap by replacing the forward directive
		updatedCorefile := strings.Replace(
			corefile,
			"forward . /etc/resolv.conf",
			"forward . 8.8.8.8 8.8.4.4",
			-1,
		)

		if updatedCorefile == corefile {
			return fmt.Errorf("failed to update CoreDNS ConfigMap: forward directive not found")
		}

		configMap.Data["Corefile"] = updatedCorefile

		// Update the ConfigMap
		_, err = kubeClient.CoreV1().ConfigMaps(namespace).Update(context.TODO(), configMap, v1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update CoreDNS ConfigMap: %w", err)
		}
		utils.InfoMessage("CoreDNS ConfigMap updated successfully")
	}

	// Create custom CoreDNS ConfigMap
	dockerAPIGateway := clusterIP
	if dockerAPIGateway == "" {
		return fmt.Errorf("failed to patch CoreDNS ConfigMap: cluster IP is empty")
	}

	if grappleDNS == "" {
		grappleDNS = "grpl-k3d.dev"
	}
	// Get the path to the coredns-custom.yaml file
	// resourcePath, err := utils.GetResourcePath("files")
	// if err != nil {
	// 	return fmt.Errorf("failed to get resource path: %w", err)
	// }
	resourcePath := "files"

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

func configureDNSForLinux() error {

	utils.AuthSudo()
	// Create resolv.conf file
	resolvContent := "nameserver 127.0.0.1\nnameserver 8.8.8.8"
	if err := os.WriteFile("/tmp/resolv.conf", []byte(resolvContent), 0644); err != nil {
		return fmt.Errorf("failed to create temporary resolv.conf: %w", err)
	}

	// Create dnsmasq.conf file
	dnsmasqContent := "listen-address=127.0.0.1\nserver=8.8.8.8\nserver=8.8.4.4\naddress=/grpl-k3d.dev/127.0.0.1\n"
	if err := os.WriteFile("/tmp/dnsmasq.conf", []byte(dnsmasqContent), 0644); err != nil {
		return fmt.Errorf("failed to create temporary dnsmasq.conf: %w", err)
	}

	// Create NetworkManager DNS configuration
	nmContent := "[main]\ndns=dnsmasq"
	if err := os.WriteFile("/tmp/dns-local.conf", []byte(nmContent), 0644); err != nil {
		return fmt.Errorf("failed to create temporary NetworkManager DNS config: %w", err)
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
	if err := exec.Command("sudo", "cp", "/tmp/resolv.conf", "/etc/resolv.conf").Run(); err != nil {
		return fmt.Errorf("failed to copy resolv.conf: %w", err)
	}
	if err := exec.Command("sudo", "cp", "/tmp/dnsmasq.conf", "/etc/dnsmasq.conf").Run(); err != nil {
		return fmt.Errorf("failed to copy dnsmasq.conf: %w", err)
	}
	if err := exec.Command("sudo", "mkdir", "-p", "/etc/NetworkManager/conf.d").Run(); err != nil {
		return fmt.Errorf("failed to create NetworkManager conf.d directory: %w", err)
	}
	if err := exec.Command("sudo", "cp", "/tmp/dns-local.conf", "/etc/NetworkManager/conf.d/dns-local.conf").Run(); err != nil {
		return fmt.Errorf("failed to copy NetworkManager DNS config: %w", err)
	}

	// Restart services
	if err := exec.Command("sudo", "systemctl", "stop", "systemd-resolved").Run(); err != nil {
		utils.InfoMessage("Failed to stop systemd-resolved, continuing anyway")
	}
	if err := exec.Command("sudo", "systemctl", "restart", "dnsmasq").Run(); err != nil {
		return fmt.Errorf("failed to restart dnsmasq: %w", err)
	}
	if err := exec.Command("sudo", "systemctl", "enable", "dnsmasq").Run(); err != nil {
		return fmt.Errorf("failed to enable dnsmasq: %w", err)
	}
	// if err := exec.Command("sudo", "systemctl", "restart", "NetworkManager").Run(); err != nil {
	// 	utils.InfoMessage("Failed to restart NetworkManager, continuing anyway")
	// }

	return nil
}

func configureDNSForMacOS() error {
	// Create dnsmasq.conf file
	dnsmasqContent := "listen-address=127.0.0.1\nserver=8.8.8.8\nserver=8.8.4.4\naddress=/grpl-k3d.dev/127.0.0.1\n"
	if err := os.WriteFile("/tmp/dnsmasq.conf", []byte(dnsmasqContent), 0644); err != nil {
		return fmt.Errorf("failed to create temporary dnsmasq.conf: %w", err)
	}

	// Display commands to be executed
	commandsToRun := "sudo brew install dnsmasq && sudo cp /tmp/dnsmasq.conf /usr/local/etc/dnsmasq.conf && sudo brew services restart dnsmasq && sudo mkdir -p /etc/resolver && echo \"nameserver 127.0.0.1\" | sudo tee /etc/resolver/grpl-k3d.dev"
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
	if err := exec.Command("sudo", "brew", "install", "dnsmasq").Run(); err != nil {
		utils.InfoMessage("Failed to install dnsmasq, it might already be installed")
	}
	if err := exec.Command("sudo", "cp", "/tmp/dnsmasq.conf", "/usr/local/etc/dnsmasq.conf").Run(); err != nil {
		return fmt.Errorf("failed to copy dnsmasq.conf: %w", err)
	}
	if err := exec.Command("sudo", "brew", "services", "restart", "dnsmasq").Run(); err != nil {
		return fmt.Errorf("failed to restart dnsmasq: %w", err)
	}
	if err := exec.Command("sudo", "mkdir", "-p", "/etc/resolver").Run(); err != nil {
		return fmt.Errorf("failed to create resolver directory: %w", err)
	}

	// Create resolver file
	resolverContent := "nameserver 127.0.0.1"
	if err := os.WriteFile("/tmp/resolver-grpl-k3d.dev", []byte(resolverContent), 0644); err != nil {
		return fmt.Errorf("failed to create temporary resolver file: %w", err)
	}
	if err := exec.Command("sudo", "cp", "/tmp/resolver-grpl-k3d.dev", "/etc/resolver/grpl-k3d.dev").Run(); err != nil {
		return fmt.Errorf("failed to copy resolver file: %w", err)
	}

	return nil
}
