package civo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/civo/civogo"
	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// connectCmd represents the connect command
var ConnectCmd = &cobra.Command{
	Use:     "connect",
	Aliases: []string{"conn"},
	Short:   "Connect to an existing Civo Kubernetes cluster",
	Long: `Connect to an existing Kubernetes cluster on Civo cloud platform and configure kubectl.
This will update your kubeconfig file to allow kubectl access to the cluster.`,
	RunE: connectToCluster,
}

func init() {
	ConnectCmd.Flags().StringVarP(&clusterName, "cluster-name", "", "", "Name of the cluster to connect to")
	ConnectCmd.Flags().StringVar(&civoRegion, "civo-region", "", "Civo region where the cluster is located")
}

// Function to handle the "connect" command logic
func connectToCluster(cmd *cobra.Command, args []string) error {

	logFile, _, logOnCliAndFileStart := utils.GetLogWriters("/tmp/grpl_civo_connect.log")

	var err error

	defer func() {
		logFile.Sync() // Ensure logs are flushed before closing
		logFile.Close()
		if err != nil {
			utils.ErrorMessage("Failed to connect to cluster, please run cat /tmp/grpl_civo_connect.log for more details")
		}
	}()

	logOnCliAndFileStart()

	// Check if already inside a cluster
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		utils.InfoMessage("Already running inside a Kubernetes cluster")
		return nil
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

	// List all clusters and find the target cluster
	clusters, err := client.ListKubernetesClusters()
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to list clusters: %v", err))
		return err
	}

	if clusterName == "" {
		var clusterNames []string
		for _, cluster := range clusters.Items {
			clusterNames = append(clusterNames, cluster.Name)
		}
		if len(clusterNames) == 0 {
			utils.ErrorMessage("No clusters found in region " + civoRegion)
			return errors.New("no clusters found in region " + civoRegion)
		}
		result, err := utils.PromptSelect("Select cluster to connect to", clusterNames)
		if err != nil {
			utils.ErrorMessage("Cluster selection is required")
			return errors.New("cluster selection is required")
		}
		clusterName = result
	}

	var targetCluster *civogo.KubernetesCluster
	for _, c := range clusters.Items {
		if c.Name == clusterName {
			targetCluster = &c
			break
		}
	}

	if targetCluster == nil {
		utils.ErrorMessage(fmt.Sprintf("Cluster '%s' not found", clusterName))
		return fmt.Errorf("cluster not found")
	}

	// Configure kubectl for the cluster
	utils.InfoMessage("Configuring kubectl for the cluster...")
	_, err = configureKubeConfig(targetCluster.KubeConfig)
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to configure kubectl for cluster '%s': %v", targetCluster.Name, err))
		return err
	}

	utils.SuccessMessage(fmt.Sprintf("Successfully connected to cluster '%s'", clusterName))

	return nil
}

// Configure kubectl for the created cluster
func configureKubeConfig(kubeConfig string) (*rest.Config, error) {
	// Get home directory
	home := os.Getenv("HOME")
	if home == "" {
		return nil, fmt.Errorf("HOME environment variable not set")
	}

	// Create .kube directory if it doesn't exist
	kubeDir := filepath.Join(home, ".kube")
	if err := os.MkdirAll(kubeDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create .kube directory: %w", err)
	}

	// Write kubeconfig file
	configPath := filepath.Join(kubeDir, "config")
	if err := os.WriteFile(configPath, []byte(kubeConfig), 0600); err != nil {
		return nil, fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	// Load kubeconfig and initialize kubectl client
	config, err := clientcmd.BuildConfigFromFlags("", configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	// Test client
	_, err = clientset.CoreV1().Namespaces().List(context.TODO(), v1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to test Kubernetes client: %w", err)
	}

	utils.SuccessMessage("Kubeconfig configured successfully.")
	return config, nil
}
