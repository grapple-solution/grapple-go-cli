package k3d

import (
	"fmt"

	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
)

// CreateInstallCmd represents the createInstall command
var CreateInstallCmd = &cobra.Command{
	Use:     "create-install",
	Aliases: []string{"ci"},
	Short:   "Create a Kubernetes cluster using k3d and Install Grapple on it (step by step)",
	Long: `Create a Kubernetes cluster using k3d and Install Grapple on it (step by step).
This command combines the functionality of 'create' and 'install' commands.`,
	RunE: runCreateInstall,
}

func init() {
	// Create command flags
	CreateInstallCmd.Flags().StringVarP(&clusterName, "cluster-name", "", "", "Name of the cluster")
	CreateInstallCmd.Flags().IntVar(&server, "servers", 1, "Number of server nodes")
	CreateInstallCmd.Flags().IntVar(&agent, "agents", 0, "Number of agent nodes")
	CreateInstallCmd.Flags().StringVar(&httpLoadBalancer, "http-loadbalancer", "80:80@loadbalancer", "Port mapping for HTTP load balancer")
	CreateInstallCmd.Flags().StringVar(&httpsLoadBalancer, "https-loadbalancer", "443:443@loadbalancer", "Port mapping for HTTPS load balancer")
	CreateInstallCmd.Flags().StringVar(&apiPort, "api-port", "6550", "API port for the k3d cluster")
	CreateInstallCmd.Flags().BoolVar(&waitForReady, "wait", false, "Wait for cluster to be ready (default: false)")
	CreateInstallCmd.Flags().BoolVar(&autoConfirm, "auto-confirm", false, "Skip confirmation prompts (default: false)")

	// Install command flags
	CreateInstallCmd.Flags().StringVar(&grappleVersion, "grapple-version", "latest", "Version of Grapple to install (default: latest)")
	CreateInstallCmd.Flags().StringVar(&clusterIP, "cluster-ip", "", "Cluster IP")
	CreateInstallCmd.Flags().StringVar(&organization, "organization", "", "Organization name (default: grapple-solutions)")
	CreateInstallCmd.Flags().BoolVar(&installKubeblocks, "install-kubeblocks", false, "Install Kubeblocks in background (default: false)")
	CreateInstallCmd.Flags().BoolVar(&sslEnable, "ssl-enable", false, "Enable SSL usage (default: false)")
	CreateInstallCmd.Flags().StringVar(&sslIssuer, "ssl-issuer", "letsencrypt-grapple-demo", "SSL Issuer (default: letsencrypt-grapple-demo)")
	CreateInstallCmd.Flags().StringVar(&grappleLicense, "grapple-license", "", "Grapple license key")
}

func runCreateInstall(cmd *cobra.Command, args []string) error {
	// First run create with waitForReady=true
	waitForReady = true // Force wait for cluster to be ready
	err := createCluster(cmd, args)
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to create cluster: %v", err))
		return err
	}

	// Then run install
	err = runInstallStepByStep(cmd, args)
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to install Grapple: %v", err))
		return err
	}

	utils.SuccessMessage("Successfully created cluster and installed Grapple!")
	return nil
}
