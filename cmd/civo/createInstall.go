package civo

import (
	"fmt"

	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
)

// CreateInstallCmd represents the createInstall command
var CreateInstallCmd = &cobra.Command{
	Use:     "create-install",
	Aliases: []string{"ci"},
	Short:   "Create a Kubernetes cluster in Civo and Install Grapple on a Civo Kubernetes cluster (step by step)",
	Long: `Create a Kubernetes cluster in Civo and Install Grapple on a Civo Kubernetes cluster (step by step).
This command combines the functionality of 'create' and 'install' commands.`,
	RunE: runCreateInstall,
}

func init() {
	// Create command flags
	CreateInstallCmd.Flags().StringVarP(&targetPlatform, "target-platform", "p", "civo", "Target platform (default: civo)")
	CreateInstallCmd.Flags().StringVarP(&clusterName, "cluster-name", "", "", "Name of the cluster")
	CreateInstallCmd.Flags().StringVar(&civoRegion, "civo-region", "", "Civo region")
	CreateInstallCmd.Flags().StringVar(&civoEmailAddress, "civo-email-address", "", "Civo email address")
	CreateInstallCmd.Flags().BoolVar(&autoConfirm, "auto-confirm", false, "Skip confirmation prompts (default: false)")
	CreateInstallCmd.Flags().StringVar(&applications, "applications", "traefik2-nodeport,civo-cluster-autoscaler,metrics-server", "Applications to install")
	CreateInstallCmd.Flags().IntVarP(&nodes, "nodes", "n", 3, "Number of nodes (default: 3)")
	CreateInstallCmd.Flags().StringVar(&size, "size", "g4s.kube.medium", "Node size (default: g4s.kube.medium)")

	// Install command flags
	CreateInstallCmd.Flags().StringVar(&grappleVersion, "grapple-version", "latest", "Version of Grapple to install (default: latest)")
	CreateInstallCmd.Flags().StringVar(&grappleDNS, "grapple-dns", "", "Domain for Grapple (default: {cluster-name}.grapple-solutions.com)")
	CreateInstallCmd.Flags().StringVar(&organization, "organization", "", "Organization name (default: grapple-solutions)")
	CreateInstallCmd.Flags().BoolVar(&installKubeblocks, "install-kubeblocks", false, "Install Kubeblocks in background (default: false)")
	CreateInstallCmd.Flags().BoolVar(&waitForReady, "wait", false, "Wait for Grapple to be fully ready at the end (default: false)")
	CreateInstallCmd.Flags().BoolVar(&sslEnable, "ssl-enable", false, "Enable SSL usage (default: false)")
	CreateInstallCmd.Flags().StringVar(&sslIssuer, "ssl-issuer", "letsencrypt-grapple-demo", "SSL Issuer (default: letsencrypt-grapple-demo)")
}

func runCreateInstall(cmd *cobra.Command, args []string) error {
	// First run create with waitForReady=true
	waitForReady = true // Force wait for cluster to be ready
	connectToCivoCluster = false
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
