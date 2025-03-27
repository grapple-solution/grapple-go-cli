/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package k3d

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
)

// connectCmd represents the connect command
var ConnectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Connect to an existing k3d Kubernetes cluster",
	Long: `Connect to an existing Kubernetes cluster created with k3d and configure kubectl.
This will update your kubeconfig file to allow kubectl access to the cluster.`,
	RunE: connectToCluster,
}

func init() {
	ConnectCmd.Flags().StringVarP(&clusterName, "cluster-name", "", "", "Name of the cluster to connect to")
}

// Function to handle the "connect" command logic
func connectToCluster(cmd *cobra.Command, args []string) error {
	utils.InstallK3d()

	logFile, _, logOnCliAndFileStart := utils.GetLogWriters("/tmp/grpl_k3d_connect.log")

	var err error

	defer func() {
		logFile.Sync() // Ensure logs are flushed before closing
		logFile.Close()
		if err != nil {
			utils.ErrorMessage("Failed to connect to cluster, please run cat /tmp/grpl_k3d_connect.log for more details")
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

	// Check if the cluster exists
	utils.InfoMessage(fmt.Sprintf("Checking if cluster '%s' exists...", clusterName))
	checkCmd := exec.Command("k3d", "cluster", "list", clusterName, "-o", "json")
	output, err := checkCmd.CombinedOutput()
	if err != nil || len(output) <= 2 { // empty JSON
		utils.ErrorMessage(fmt.Sprintf("Cluster with name '%s' does not exist", clusterName))
		return fmt.Errorf("cluster with name '%s' does not exist", clusterName)
	}

	// Configure kubectl for the cluster
	utils.InfoMessage("Configuring kubectl for the cluster...")
	configureCmd := exec.Command("k3d", "kubeconfig", "merge", clusterName, "--kubeconfig-merge-default", "--kubeconfig-switch-context")
	configureCmd.Stdout = os.Stdout
	configureCmd.Stderr = os.Stderr

	if err := configureCmd.Run(); err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to configure kubectl for cluster '%s': %v", clusterName, err))
		return fmt.Errorf("failed to configure kubectl for cluster '%s': %v", clusterName, err)
	}

	utils.SuccessMessage(fmt.Sprintf("Successfully connected to cluster '%s'", clusterName))
	return nil
}
