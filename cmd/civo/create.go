package civo

import (
	"errors"
	"fmt"
	"time"

	"github.com/civo/civogo"
	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
)

// CreateCmd represents the create command
var CreateCmd = &cobra.Command{
	Use:     "create",
	Aliases: []string{"c"},
	Short:   "Create a Kubernetes cluster in Civo",
	Long:    "Create a new Kubernetes cluster on the Civo cloud platform with specified configuration.",
	RunE:    createCluster,
}

// Initialize flags
func init() {
	CreateCmd.Flags().StringVarP(&targetPlatform, "target-platform", "p", "civo", "Target platform (default: civo)")
	CreateCmd.Flags().StringVarP(&clusterName, "cluster-name", "", "", "Name of the cluster")
	CreateCmd.Flags().StringVar(&civoRegion, "civo-region", "", "Civo region")
	CreateCmd.Flags().StringVar(&civoEmailAddress, "civo-email-address", "", "Civo email address")
	CreateCmd.Flags().BoolVar(&autoConfirm, "auto-confirm", false, "Skip confirmation prompts (default: false)")
	CreateCmd.Flags().StringVar(&applications, "applications", "traefik2-nodeport,civo-cluster-autoscaler,metrics-server", "Applications to install")
	CreateCmd.Flags().IntVarP(&nodes, "nodes", "n", 3, "Number of nodes (default: 3)")
	CreateCmd.Flags().StringVar(&size, "size", "g4s.kube.medium", "Node size (default: g4s.kube.medium)")
	CreateCmd.Flags().BoolVar(&waitForReady, "wait", false, "Wait for cluster to be ready (default: false)")
}

// Function to handle the "create" command logic
func createCluster(cmd *cobra.Command, args []string) error {

	logFileName := "grpl_civo_create.log"
	logFilePath := utils.GetLogFilePath(logFileName)
	logFile, _, logOnCliAndFileStart := utils.GetLogWriters(logFilePath)

	var err error

	defer func() {
		logFile.Sync()
		logFile.Close()
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to create cluster, please run cat %s for more details", logFilePath))
		}
	}()

	logOnCliAndFileStart()

	// Validate input
	if clusterName == "" {
		result, err := utils.PromptInput("Enter cluster name", utils.DefaultValue, utils.NonEmptyValueRegex)
		if err != nil {
			utils.ErrorMessage("Cluster name is required")
			return errors.New("cluster name is required")
		}
		clusterName = result
	}

	civoAPIKey := getCivoAPIKey()

	if civoRegion == "" {
		regions := getCivoRegion(civoAPIKey)
		result, err := utils.PromptSelect("Select region", regions)
		if err != nil {
			utils.ErrorMessage("Region selection is required")
			return errors.New("region selection is required")
		}
		civoRegion = result
	}

	utils.InfoMessage("Initializing Civo client...")
	client, err := civogo.NewClient(civoAPIKey, civoRegion)
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to initialize Civo client: %v", err))
		return err
	}

	utils.SuccessMessage("Civo client initialized successfully.")

	// Check if cluster already exists
	utils.InfoMessage(fmt.Sprintf("Checking if cluster '%s' already exists...", clusterName))
	if exists, err := checkClusterExists(client, clusterName); err != nil {
		utils.ErrorMessage(fmt.Sprintf("Error checking cluster existence: %v", err))
		return err
	} else if exists {
		utils.ErrorMessage(fmt.Sprintf("Cluster with name '%s' already exists", clusterName))
		return fmt.Errorf("cluster with name '%s' already exists", clusterName)
	}

	// Create the cluster
	utils.InfoMessage("Creating the cluster...")
	cluster, err := createCivoCluster(client)
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to create cluster: %v", err))
		return err
	}
	utils.SuccessMessage(fmt.Sprintf("Cluster '%s' creation initiated, it will be ready in a few minutes", cluster.Name))

	if waitForReady {
		// Wait for cluster readiness
		utils.InfoMessage(fmt.Sprintf("Waiting for cluster '%s' to be ready...", cluster.Name))
		if err := waitForClusterReady(client, cluster); err != nil {
			utils.ErrorMessage(fmt.Sprintf("Cluster '%s' is not ready: %v", cluster.Name, err))
			return err
		}

		// sleep for 20 seconds to ensure cluster is fully registered
		time.Sleep(20 * time.Second)

		// Instead of duplicating connection logic, use the connect command
		if connectToCivoCluster {
			err = connectToCluster(cmd, args)
			if err != nil {
				utils.ErrorMessage(fmt.Sprintf("Failed to connect to cluster: %v", err))
				return err
			}
		}

		utils.SuccessMessage(fmt.Sprintf("Cluster '%s' is ready and kubectl is configured.", clusterName))

	}

	return nil
}

// Check if a cluster already exists
func checkClusterExists(client *civogo.Client, name string) (bool, error) {
	clusters, err := client.ListKubernetesClusters()
	if err != nil {
		return false, fmt.Errorf("error fetching clusters: %w", err)
	}
	for _, cluster := range clusters.Items {
		if cluster.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// Create a new Civo cluster
func createCivoCluster(client *civogo.Client) (*civogo.KubernetesCluster, error) {
	config := &civogo.KubernetesClusterConfig{
		Name:            clusterName,
		NumTargetNodes:  nodes,
		TargetNodesSize: size,
		Applications:    applications,
		Region:          civoRegion,
		FirewallRule:    "80,443,6443",
	}
	cluster, err := client.NewKubernetesClusters(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster: %w", err)
	}
	return cluster, nil
}
