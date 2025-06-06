package civo

import (
	"errors"
	"fmt"
	"strings"

	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
)

// UninstallCmd represents the uninstall command
var UninstallCmd = &cobra.Command{
	Use:     "uninstall",
	Aliases: []string{"u"},
	Short:   "Uninstall Grapple from the cluster",
	Long: `Uninstall command removes all Grapple components and resources from the cluster.
This will completely remove all traces of Grapple installation including:
- All Grapple namespaces and resources
- Configuration settings
- Deployed applications
- Associated storage volumes and data`,
	RunE: runUninstall,
}

func init() {
	UninstallCmd.Flags().BoolVar(&autoConfirm, "auto-confirm", true, "If true, uninstalls grapple from the currently connected Civo cluster. If false, prompts for cluster name and civo region and removes grapple from the specified cluster. Default value of auto-confirm is true")
	UninstallCmd.Flags().StringVar(&civoRegion, "civo-region", "", "Civo region")
	UninstallCmd.Flags().StringVar(&clusterName, "cluster-name", "", "Civo cluster name")
	UninstallCmd.Flags().BoolVarP(&skipConfirmation, "yes", "y", false, "Skip confirmation prompt before uninstalling")
}

func runUninstall(cmd *cobra.Command, args []string) error {
	
	logFileName := "grpl_civo_uninstall.log"
	logFilePath := utils.GetLogFilePath(logFileName)
	logFile, logOnFileStart, logOnCliAndFileStart := utils.GetLogWriters(logFilePath)

	var err error

	defer func() {
		logFile.Sync()
		logFile.Close()
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to uninstall grpl, please run cat %s for more details", logFilePath))
		}
	}()

	logOnCliAndFileStart()

	// Ask for confirmation unless --yes flag is set
	if !skipConfirmation {
		confirmMsg := "Are you sure you want to uninstall Grapple? This will remove all Grapple components and data (y/N): "
		confirmed, err := utils.PromptInput(confirmMsg, "n", "^[yYnN]$")
		if err != nil {
			return err
		}
		if strings.ToLower(confirmed) != "y" {
			utils.InfoMessage("Uninstallation cancelled")
			return nil
		}
	}

	// Connect to cluster
	connectToCivoCluster := func() error {
		err := connectToCluster(cmd, args)
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to connect to cluster: %v", err))
			return err
		}
		return nil
	}

	// Try to get existing connection first
	_, clientset, err := utils.GetKubernetesConfig()
	if err != nil {
		utils.InfoMessage("No existing connection found")
		err = connectToCivoCluster()
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to connect to cluster: %v", err))
			return err
		}
	}

	providerClusterType, err := utils.GetClusterProviderType(clientset)
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to get cluster provider type: %v", err))
		return err
	}
	if providerClusterType != utils.ProviderClusterTypeCivo {
		utils.ErrorMessage("This command is only available for Civo clusters")
		return errors.New("this command is only available for Civo clusters")
	}

	return utils.UninstallGrapple(connectToCivoCluster, logOnFileStart, logOnCliAndFileStart)
}
