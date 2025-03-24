package k3d

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/grapple-solution/grapple_cli/utils" // your logging/prompting
	"github.com/spf13/cobra"
	// Helm libraries
	// Kubernetes libraries
)

// Variables for command flags
var (
	grappleVersion string
	autoConfirm    bool
	// clusterName       string
	clusterIP         string
	grappleDNS        string
	organization      string
	installKubeblocks bool
	// waitForReady      bool
	sslEnable      bool
	sslIssuer      string
	grappleLicense string
	completeDomain string
)

// Constants for configuration keys
const (
	secKeyEmail               = "email"
	secKeyOrganization        = "organization"
	secKeyClusterdomain       = "clusterdomain"
	secKeyGrapiversion        = "grapiversion"
	secKeyGruimversion        = "gruimversion"
	secKeyDev                 = "dev"
	secKeySsl                 = "ssl"
	secKeySslissuer           = "sslissuer"
	secKeyClusterName         = "clustername"
	secKeyGrapleDNS           = "GRAPPLE_DNS"
	secKeyGrapleVersion       = "GRAPPLE_VERSION"
	secKeyGrapleLicense       = "GRAPPLE_LICENSE"
	secKeyProviderClusterType = "PROVIDER_CLUSTER_TYPE"
	providerClusterTypeK3d    = "k3d"
)

// InstallCmd represents the install command
var InstallCmd = &cobra.Command{
	Use:     "install",
	Aliases: []string{"i"},
	Short:   "Install Grapple on a K3d Kubernetes cluster (step by step)",
	Long: `Installs Grapple components (grsf-init, grsf, grsf-config, grsf-integration) 
sequentially, waiting for required resources in between, mirroring the step-by-step logic of your Bash script.`,
	RunE: runInstallStepByStep,
}

// init sets up flags for install
func init() {
	InstallCmd.Flags().StringVar(&grappleVersion, "grapple-version", "latest", "Version of Grapple to install (default: latest)")
	InstallCmd.Flags().BoolVar(&autoConfirm, "auto-confirm", false, "Skip confirmation prompts (default: false)")
	InstallCmd.Flags().StringVar(&clusterName, "cluster-name", "", "K3d cluster name")
	InstallCmd.Flags().StringVar(&clusterIP, "cluster-ip", "", "Cluster IP")
	InstallCmd.Flags().StringVar(&organization, "organization", "", "Organization name (default: grapple-solutions)")
	InstallCmd.Flags().BoolVar(&installKubeblocks, "install-kubeblocks", false, "Install Kubeblocks in background (default: false)")
	InstallCmd.Flags().BoolVar(&waitForReady, "wait", false, "Wait for Grapple to be fully ready at the end (default: false)")
	InstallCmd.Flags().BoolVar(&sslEnable, "ssl-enable", false, "Enable SSL usage (default: false)")
	InstallCmd.Flags().StringVar(&sslIssuer, "ssl-issuer", "letsencrypt-grapple-demo", "SSL Issuer (default: letsencrypt-grapple-demo)")
	InstallCmd.Flags().StringVar(&grappleLicense, "grapple-license", "", "Grapple license key")
}

// runInstallStepByStep is the main function
func runInstallStepByStep(cmd *cobra.Command, args []string) error {
	logFile, logOnFileStart, logOnCliAndFileStart := utils.GetLogWriters("/tmp/grpl_k3d_install.log")

	var err error

	defer func() {
		logFile.Sync() // Ensure logs are flushed before closing
		logFile.Close()
		if err != nil {
			utils.ErrorMessage("Failed to install grpl, please run cat /tmp/grpl_k3d_install.log for more details")
		}
	}()

	// Start logging to both CLI and file
	logOnCliAndFileStart()

	// Set default values if not provided
	if organization == "" {
		organization = "grapple-solutions"
	}

	if clusterName == "" {
		utils.ErrorMessage("Cluster name is required")
		return fmt.Errorf("cluster name is required")
	}

	grappleDNS = "grpl-k3d.dev"

	if grappleVersion == "" || grappleVersion == "latest" {
		grappleVersion = "0.2.8"
	}

	completeDomain = grappleDNS

	// 1) Create/fetch the K3d client and cluster info, build a Kube + Helm client
	kubeClient, restConfig, err := initClientsAndConfig()
	if err != nil {
		return err
	}

	// If user wants to install Kubeblocks in background:
	var kubeblocksWg sync.WaitGroup
	kubeblocksInstallStatus := true
	var kubeblocksInstallError error

	// Check if flag was not set and not explicitly false
	if !cmd.Flags().Changed("install-kubeblocks") && !installKubeblocks {
		// Ask user if they want to install KubeBlocks
		confirmMsg := "Do you want to install KubeBlocks? (y/N): "
		confirmed, err := utils.PromptInput(confirmMsg, "n", "^[yYnN]$")
		if err != nil {
			return err
		}
		if strings.ToLower(confirmed) == "y" {
			installKubeblocks = true
		}
	}

	if installKubeblocks {
		kubeblocksWg.Add(1)
		go func() {
			defer kubeblocksWg.Done()
			if err := utils.InstallKubeBlocksOnCluster(restConfig); err != nil {
				utils.ErrorMessage("kubeblocks installation error: " + err.Error())
				kubeblocksInstallStatus = false
				kubeblocksInstallError = err
			} else {
				utils.InfoMessage("kubeblocks installed.")
			}
		}()
	}

	err = waitForK3dClusterToBeReady(restConfig)
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to wait for cluster to be ready: %v", err))
		return fmt.Errorf("failed to wait for cluster to be ready: %v", err)
	}

	// Setup local DNS configuration
	utils.InfoMessage("Setting up local DNS configuration...")

	// Call the patch DNS command to configure DNS
	if err := runPatchDNS(cmd, []string{}); err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to patch DNS: %v", err))
		return fmt.Errorf("failed to patch DNS: %w", err)
	}

	utils.SuccessMessage("Local DNS configuration completed successfully")

	// Start preloading images in parallel
	var preloadImagesWg sync.WaitGroup
	preloadImagesWg.Add(1)
	go func() {
		defer preloadImagesWg.Done()
		if err := utils.PreloadGrappleImages(restConfig, grappleVersion); err != nil {
			utils.ErrorMessage("image preload error: " + err.Error())
		} else {
			utils.InfoMessage("grapple images preloaded.")
		}
	}()

	prepareValuesFile()

	deploymentPath, err := utils.GetResourcePath("template-files")
	if err != nil {
		return fmt.Errorf("failed to get deployment path: %w", err)
	}
	// deploymentPath := "template-files"
	valuesFileForK3d := filepath.Join(deploymentPath, "values-k3d.yaml")

	valuesFile := []string{"/tmp/values-override.yaml", valuesFileForK3d}
	// Step 3) Deploy "grsf-init"
	utils.InfoMessage("Deploying 'grsf-init' chart...")
	logOnFileStart()
	err = utils.HelmDeployGrplReleasesWithRetry(kubeClient, "grsf-init", "grpl-system", grappleVersion, valuesFile)
	logOnCliAndFileStart()
	if err != nil {
		return fmt.Errorf("failed to deploy grsf-init: %w", err)
	}

	utils.InfoMessage("Waiting for grsf-init to be ready...")
	logOnFileStart()
	err = utils.WaitForGrsfInit(kubeClient)
	logOnCliAndFileStart()
	if err != nil {
		return fmt.Errorf("grsf-init not ready: %w", err)
	}
	utils.SuccessMessage("grsf-init is installed and ready.")

	// Step 4) Deploy "grsf"
	utils.InfoMessage("Deploying 'grsf' chart...")
	logOnFileStart()
	err = utils.HelmDeployGrplReleasesWithRetry(kubeClient, "grsf", "grpl-system", grappleVersion, valuesFile)
	logOnCliAndFileStart()
	if err != nil {
		return fmt.Errorf("failed to deploy grsf: %w", err)
	}

	utils.InfoMessage("Waiting for grsf to be ready (checking crossplane providers, etc.)...")
	logOnFileStart()
	err = utils.WaitForGrsf(kubeClient, "grpl-system")
	logOnCliAndFileStart()
	if err != nil {
		return fmt.Errorf("grsf not ready: %w", err)
	}
	utils.SuccessMessage("grsf is installed and ready.")

	// Step 5) Deploy "grsf-config"
	utils.InfoMessage("Deploying 'grsf-config' chart...")
	logOnFileStart()
	err = utils.HelmDeployGrplReleasesWithRetry(kubeClient, "grsf-config", "grpl-system", grappleVersion, valuesFile)
	logOnCliAndFileStart()
	if err != nil {
		return fmt.Errorf("failed to deploy grsf-config: %w", err)
	}

	utils.InfoMessage("Waiting for grsf-config to be applied (CRDs, XRDs, etc.)...")
	logOnFileStart()
	err = utils.WaitForGrsfConfig(kubeClient, restConfig)
	logOnCliAndFileStart()
	if err != nil {
		return fmt.Errorf("grsf-config not ready: %w", err)
	}
	utils.SuccessMessage("grsf-config is installed.")

	// Step 6) Deploy "grsf-integration"
	utils.InfoMessage("Deploying 'grsf-integration' chart...")
	logOnFileStart()
	if err := utils.HelmDeployGrplReleasesWithRetry(kubeClient, "grsf-integration", "grpl-system", grappleVersion, valuesFile); err != nil {
		return fmt.Errorf("failed to deploy grsf-integration: %w", err)
	}

	utils.InfoMessage("Waiting for grsf-integration to be ready...")
	logOnFileStart()
	err = utils.WaitForGrsfIntegration(restConfig)
	logOnCliAndFileStart()
	if err != nil {
		return fmt.Errorf("grsf-integration not ready: %w", err)
	}
	utils.SuccessMessage("grsf-integration is installed.")

	// Step 8) If user wants to wait for the entire Grapple system
	if waitForReady {
		utils.InfoMessage("Waiting for Grapple to be ready...")
		logOnFileStart()
		err = utils.WaitForGrappleReady(restConfig)
		logOnCliAndFileStart()
		if err != nil {
			return fmt.Errorf("failed to wait for grapple to be ready: %w", err)
		}
		utils.SuccessMessage("Grapple is ready!")
	}

	if installKubeblocks {
		utils.InfoMessage("Waiting for kubeblocks to be ready, it might take a while...")
		logOnFileStart()
		kubeblocksWg.Wait()
		logOnCliAndFileStart()
		if kubeblocksInstallStatus {
			utils.SuccessMessage("Kubeblocks installation completed!")
		} else {
			utils.ErrorMessage("Kubeblocks installation failed! with error: " + kubeblocksInstallError.Error())
		}
	}

	utils.InfoMessage("Waiting for grapple images to be preloaded...")
	preloadImagesWg.Wait()

	err = utils.CreateClusterIssuer(kubeClient, sslEnable)
	if err != nil {
		return fmt.Errorf("failed to setup cluster issuer: %w", err)
	}

	utils.SuccessMessage("Grapple installation completed!")
	return nil
}
