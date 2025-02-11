package civo

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/civo/civogo"
	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
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
	RemoveCmd.Flags().BoolVar(&autoConfirm, "auto-confirm", false, "Skip confirmation prompts")
	RemoveCmd.Flags().StringVar(&civoRegion, "civo-region", "", "Civo region")
	RemoveCmd.Flags().StringVar(&clusterName, "cluster-name", "", "Civo cluster name")
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

	if !autoConfirm {
		if confirmed, err := utils.PromptConfirm("This will permanently delete the cluster. Are you sure?"); err != nil || !confirmed {
			return fmt.Errorf("remove cancelled by user")
		}
	}

	civoAPIKey = os.Getenv("CIVO_API_TOKEN")
	if civoAPIKey == "" {
		utils.ErrorMessage("Civo API key is required, set CIVO_API_TOKEN in your environment variables")
		return errors.New("civo API key is required, set CIVO_API_TOKEN in your environment variables")
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
