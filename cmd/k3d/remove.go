package k3d

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// K3dCluster represents the relevant cluster info from k3d
type K3dCluster struct {
	Name string `json:"name"`
}

// RemoveCmd represents the remove command
var RemoveCmd = &cobra.Command{
	Use:     "remove",
	Aliases: []string{"r"},
	Short:   "Remove all traces of the cluster from k3d",
	Long: `Remove command will clean up and delete all resources associated with 
the Kubernetes cluster from k3d

This ensures a complete cleanup of all cluster-related resources.`,
	RunE: runRemove,
}

func init() {
	RemoveCmd.Flags().BoolVar(&autoConfirm, "auto-confirm", true, "If true, deletes the currently connected k3d cluster. If false, prompts for cluster name. Default value of auto-confirm is true")
	RemoveCmd.Flags().StringVar(&clusterName, "cluster-name", "", "k3d cluster name")
	RemoveCmd.Flags().BoolVarP(&skipConfirmation, "yes", "y", false, "Skip confirmation prompt before removing cluster")
}

func getClusterDetailsFromConfig(clientset *kubernetes.Clientset) bool {
	// Try to get grsf-config secret
	secret, err := clientset.CoreV1().Secrets("grpl-system").Get(context.TODO(), "grsf-config", v1.GetOptions{})
	if err != nil {
		return false
	}
	// Check provider type
	if string(secret.Data["provider_cluster_type"]) == "k3d" {
		// Extract cluster name if not provided via flags
		if clusterName == "" {
			clusterName = string(secret.Data["cluster_name"])
		}
		utils.InfoMessage(fmt.Sprintf("Using values from grsf-config: cluster=%s", clusterName))
		return true
	}
	return false
}

func runRemove(cmd *cobra.Command, args []string) error {
	
	logFileName := "grpl_k3d_remove.log"
	logFilePath := utils.GetLogFilePath(logFileName)
	logFile, _, logOnCliAndFileStart := utils.GetLogWriters(logFilePath)

	var err error

	defer func() {
		logFile.Sync()
		logFile.Close()
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to remove cluster, please run cat %s for more details", logFilePath))
		}
	}()

	logOnCliAndFileStart()

	// Try to get existing connection first
	_, clientset, err := utils.GetKubernetesConfig()
	if err != nil {
		utils.InfoMessage("No existing connection found")
	} else if autoConfirm {
		if !getClusterDetailsFromConfig(clientset) {
			utils.InfoMessage("Unable to find cluster details in grsf-config, moving to prompt for cluster name")
		}
	}

	if clusterName == "" {
		// Get list of k3d clusters
		output, err := exec.Command("k3d", "cluster", "list", "-o", "json").Output()
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to list clusters: %v", err))
			return err
		}

		// Parse the JSON output to get cluster names
		var clusters []K3dCluster
		if err := json.Unmarshal(output, &clusters); err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to parse clusters: %v", err))
			return err
		}

		if len(clusters) == 0 {
			utils.ErrorMessage("No k3d clusters found")
			return errors.New("no k3d clusters found")
		}

		var clusterNames []string
		for _, cluster := range clusters {
			clusterNames = append(clusterNames, cluster.Name)
		}

		result, err := utils.PromptSelect("Select cluster to remove", clusterNames)
		if err != nil {
			utils.ErrorMessage("Cluster selection is required")
			return errors.New("cluster selection is required")
		}
		clusterName = result
	}

	// Verify cluster exists
	err = exec.Command("k3d", "cluster", "list", clusterName).Run()
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Cluster %s not found", clusterName))
		return fmt.Errorf("cluster %s not found", clusterName)
	}

	// Ask for confirmation unless --yes flag is set
	if !skipConfirmation {
		confirmMsg := fmt.Sprintf("Are you sure you want to delete cluster '%s'? This action cannot be undone (y/N): ", clusterName)
		confirmed, err := utils.PromptInput(confirmMsg, "n", "^[yYnN]$")
		if err != nil {
			return err
		}
		if strings.ToLower(confirmed) != "y" {
			utils.InfoMessage("Cluster deletion cancelled")
			return nil
		}
	}

	utils.InfoMessage(fmt.Sprintf("Deleting cluster %s...", clusterName))

	// Delete the cluster using k3d CLI
	err = exec.Command("k3d", "cluster", "delete", clusterName).Run()
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to delete cluster: %v", err))
		return err
	}

	logOnCliAndFileStart()
	utils.SuccessMessage(fmt.Sprintf("Successfully deleted cluster %s", clusterName))
	return nil
}
