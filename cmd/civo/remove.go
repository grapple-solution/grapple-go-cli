package civo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/civo/civogo"
	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// RemoveCmd represents the remove command
var RemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove all traces of the cluster from the supplier account",
	Long: `Remove command will clean up and delete all resources associated with 
the Kubernetes cluster from the supplier account

This ensures a complete cleanup of all cluster-related resources.`,
	RunE: runRemove,
}

func init() {
	RemoveCmd.Flags().BoolVar(&autoConfirm, "auto-confirm", true, "If true, deletes the currently connected Civo cluster. If false, prompts for cluster name and civo region and deletes the specified cluster. Default value of auto-confirm is true")
	RemoveCmd.Flags().StringVar(&civoRegion, "civo-region", "", "Civo region")
	RemoveCmd.Flags().StringVar(&clusterName, "cluster-name", "", "Civo cluster name")
}

func getClusterDetailsFromConfig() bool {
	// Get kubernetes config
	config, err := rest.InClusterConfig()
	if err != nil {
		// Try loading from kubeconfig file if not in cluster
		home := os.Getenv("HOME")
		kubeconfig := filepath.Join(home, ".kube", "config")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			utils.InfoMessage("Could not load kubernetes config, proceeding with provided values")
			return false
		}
	}

	// Create kubernetes clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return false
	}
	// Try to get grsf-config secret
	secret, err := clientset.CoreV1().Secrets("grpl-system").Get(context.TODO(), "grsf-config", v1.GetOptions{})
	if err != nil {
		return false
	}
	// Check provider type
	if string(secret.Data[secKeyProviderClusterType]) == providerClusterTypeCivo {
		// Extract cluster name and region if not provided via flags
		if clusterName == "" {
			clusterName = string(secret.Data[secKeyClusterName])
		}
		if civoRegion == "" {
			civoRegion = string(secret.Data[secKeyCivoRegion])
		}
		utils.InfoMessage(fmt.Sprintf("Using values from grsf-config: cluster=%s, region=%s", clusterName, civoRegion))
		return true
	}
	return false
}

func runRemove(cmd *cobra.Command, args []string) error {

	logFile, _, logOnCliAndFileStart := utils.GetLogWriters("/tmp/grpl_civo_remove.log")

	var err error

	defer func() {
		logFile.Sync()
		logFile.Close()
		if err != nil {
			utils.ErrorMessage("Failed to remove cluster, please run cat /tmp/grpl_civo_remove.log for more details")
		}
	}()

	logOnCliAndFileStart()

	civoAPIKey = os.Getenv("CIVO_API_TOKEN")
	if civoAPIKey == "" {
		utils.ErrorMessage("Civo API key is required, set CIVO_API_TOKEN in your environment variables")
		return errors.New("civo API key is required, set CIVO_API_TOKEN in your environment variables")
	}

	if autoConfirm {
		reconnect = false
		err = connectToCluster(cmd, args)
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to connect to cluster: %v", err))
			return err
		}

		if civoRegion == "" && clusterName == "" && !getClusterDetailsFromConfig() {
			utils.InfoMessage("Unable to find cluster details in grsf-config, moving to prompt for region and cluster name")
		}
	}

	if civoRegion == "" {
		regions := []string{
			"nyc1",
			"phx1",
			"fra1",
			"lon1",
		}
		result, err := utils.PromptSelect("Select region", regions)
		if err != nil {
			utils.ErrorMessage("Region selection is required")
			return errors.New("region selection is required")
		}
		civoRegion = result
	}
	// Initialize Civo client
	apiKey := strings.TrimSpace(civoAPIKey)
	client, err := civogo.NewClient(apiKey, civoRegion)
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to initialize Civo client: %v", err))
		return err
	}

	// Get list of clusters first to verify existence
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
		result, err := utils.PromptSelect("Select cluster to remove", clusterNames)
		if err != nil {
			utils.ErrorMessage("Cluster selection is required")
			return errors.New("cluster selection is required")
		}
		clusterName = result
	}

	var targetCluster *civogo.KubernetesCluster
	for _, cluster := range clusters.Items {
		if cluster.Name == clusterName {
			utils.InfoMessage(fmt.Sprintf("Cluster %s found in region %s", clusterName, civoRegion))
			targetCluster = &cluster
			break
		}
	}

	if targetCluster == nil {
		utils.ErrorMessage(fmt.Sprintf("Cluster %s not found in region %s", clusterName, civoRegion))
		return fmt.Errorf("cluster %s not found in region %s", clusterName, civoRegion)
	}

	utils.InfoMessage(fmt.Sprintf("Deleting cluster %s...", clusterName))
	// Delete the cluster using Civo API
	_, err = client.DeleteKubernetesCluster(targetCluster.ID)
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to delete cluster: %v", err))
		return err
	}
	// Wait and verify deletion
	maxRetries := 10
	for i := 0; i < maxRetries; i++ {
		time.Sleep(10 * time.Second)

		// Check if cluster still exists
		clusters, err := client.ListKubernetesClusters()
		if err != nil {
			continue
		}

		clusterExists := false
		for _, cluster := range clusters.Items {
			if cluster.ID == targetCluster.ID {
				clusterExists = true
				break
			}
		}

		if !clusterExists {
			logOnCliAndFileStart()
			utils.SuccessMessage(fmt.Sprintf("Successfully deleted cluster %s", clusterName))
			return nil
		}

	}

	logOnCliAndFileStart()
	utils.SuccessMessage(fmt.Sprintf("Delete request sent for cluster %s. The cluster should be removed shortly.", clusterName))
	return nil
}
