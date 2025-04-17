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

// CreateCmd represents the create command
var CreateCmd = &cobra.Command{
	Use:     "create",
	Aliases: []string{"c"},
	Short:   "Create a Kubernetes cluster using k3d",
	Long:    "Create a new Kubernetes cluster locally using k3d with specified configuration.",
	RunE:    createCluster,
}

func init() {
	CreateCmd.Flags().StringVarP(&clusterName, "cluster-name", "", "", "Name of the cluster")
	CreateCmd.Flags().IntVarP(&nodes, "nodes", "n", 1, "Number of nodes (default: 1)")
	CreateCmd.Flags().BoolVar(&waitForReady, "wait", false, "Wait for cluster to be ready (default: false)")
}

// Function to handle the "create" command logic
func createCluster(cmd *cobra.Command, args []string) error {
	utils.InstallK3d()

	logFileName := "grpl_k3d_create.log"
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

	// Check if the cluster already exists
	utils.InfoMessage(fmt.Sprintf("Checking if cluster '%s' already exists...", clusterName))
	checkCmd := exec.Command("k3d", "cluster", "list", clusterName, "-o", "json")
	output, err := checkCmd.CombinedOutput()
	if err == nil && len(output) > 2 { // not empty JSON
		utils.ErrorMessage(fmt.Sprintf("Cluster with name '%s' already exists", clusterName))
		return fmt.Errorf("cluster with name '%s' already exists", clusterName)
	}

	// Create the cluster
	utils.InfoMessage(fmt.Sprintf("Creating cluster '%s', it may take a while...", clusterName))
	createCmdArgs := []string{
		"cluster", "create", clusterName,
		"--servers", fmt.Sprintf("%d", nodes),
		"--api-port", "6550",
		"-p", "80:80@loadbalancer",
		"-p", "443:443@loadbalancer",
	}
	if waitForReady {
		createCmdArgs = append(createCmdArgs, "--wait")
	}
	createCmd := exec.Command("k3d", createCmdArgs...)

	// createCmd.Stdout = os.Stdout
	createCmd.Stderr = os.Stderr

	if err := createCmd.Run(); err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to create cluster: %v", err))
		return fmt.Errorf("failed to create cluster: %v", err)
	}

	utils.SuccessMessage(fmt.Sprintf("Cluster '%s' created successfully", clusterName))

	// Connect to the newly created cluster
	utils.InfoMessage("Connecting to the newly created cluster...")
	err = connectToCluster(cmd, args)
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to connect to the cluster: %v", err))
		return fmt.Errorf("failed to connect to the cluster: %v", err)
	}

	if waitForReady {
		utils.SuccessMessage("Cluster is ready and kubectl is configured.")
		restConfig, _, err := utils.GetKubernetesConfig()
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to get kubernetes config: %v", err))
			return fmt.Errorf("failed to get kubernetes config: %v", err)
		}
		err = waitForK3dClusterToBeReady(restConfig)
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to wait for cluster to be ready: %v", err))
			return fmt.Errorf("failed to wait for cluster to be ready: %v", err)
		}
	}

	return nil
}
