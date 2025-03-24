package k3d

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/grapple-solution/grapple_cli/utils" // your logging/prompting
	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/cli/values"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/registry"

	// Helm libraries

	// Kubernetes libraries

	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiv1 "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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

	valuesFile := "/tmp/values-override.yaml"
	// Step 3) Deploy "grsf-init"
	utils.InfoMessage("Deploying 'grsf-init' chart...")
	logOnFileStart()
	err = helmDeployReleaseWithRetry(kubeClient, "grsf-init", "grpl-system", grappleVersion, valuesFile)
	logOnCliAndFileStart()
	if err != nil {
		return fmt.Errorf("failed to deploy grsf-init: %w", err)
	}

	utils.InfoMessage("Waiting for grsf-init to be ready...")
	logOnFileStart()
	err = waitForGrsfInit(kubeClient)
	logOnCliAndFileStart()
	if err != nil {
		return fmt.Errorf("grsf-init not ready: %w", err)
	}
	utils.SuccessMessage("grsf-init is installed and ready.")

	// Step 4) Deploy "grsf"
	utils.InfoMessage("Deploying 'grsf' chart...")
	logOnFileStart()
	err = helmDeployReleaseWithRetry(kubeClient, "grsf", "grpl-system", grappleVersion, valuesFile)
	logOnCliAndFileStart()
	if err != nil {
		return fmt.Errorf("failed to deploy grsf: %w", err)
	}

	utils.InfoMessage("Waiting for grsf to be ready (checking crossplane providers, etc.)...")
	logOnFileStart()
	err = waitForGrsf(kubeClient, "grpl-system")
	logOnCliAndFileStart()
	if err != nil {
		return fmt.Errorf("grsf not ready: %w", err)
	}
	utils.SuccessMessage("grsf is installed and ready.")

	// Step 5) Deploy "grsf-config"
	utils.InfoMessage("Deploying 'grsf-config' chart...")
	logOnFileStart()
	err = helmDeployReleaseWithRetry(kubeClient, "grsf-config", "grpl-system", grappleVersion, valuesFile)
	logOnCliAndFileStart()
	if err != nil {
		return fmt.Errorf("failed to deploy grsf-config: %w", err)
	}

	utils.InfoMessage("Waiting for grsf-config to be applied (CRDs, XRDs, etc.)...")
	logOnFileStart()
	err = waitForGrsfConfig(kubeClient, restConfig)
	logOnCliAndFileStart()
	if err != nil {
		return fmt.Errorf("grsf-config not ready: %w", err)
	}
	utils.SuccessMessage("grsf-config is installed.")

	// Step 6) Deploy "grsf-integration"
	utils.InfoMessage("Deploying 'grsf-integration' chart...")
	logOnFileStart()
	if err := helmDeployReleaseWithRetry(kubeClient, "grsf-integration", "grpl-system", grappleVersion, valuesFile); err != nil {
		return fmt.Errorf("failed to deploy grsf-integration: %w", err)
	}

	utils.InfoMessage("Waiting for grsf-integration to be ready...")
	logOnFileStart()
	err = waitForGrsfIntegration(restConfig)
	logOnCliAndFileStart()
	if err != nil {
		return fmt.Errorf("grsf-integration not ready: %w", err)
	}
	utils.SuccessMessage("grsf-integration is installed.")

	// Step 7) SSL enabling
	if sslEnable {
		utils.InfoMessage("Enabling SSL (applying clusterissuer, etc.)")
		logOnFileStart()
		err = createClusterIssuer(kubeClient)
		logOnCliAndFileStart()
		if err != nil {
			return fmt.Errorf("failed to create clusterissuer: %w", err)
		}
		utils.InfoMessage("Successfully created clusterissuer.")
	}

	// Step 8) If user wants to wait for the entire Grapple system
	if waitForReady {
		utils.InfoMessage("Waiting for Grapple to be ready...")
		logOnFileStart()
		err = waitForGrappleReady(restConfig)
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

	err = setupClusterIssuer(context.TODO(), restConfig)
	if err != nil {
		return fmt.Errorf("failed to setup cluster issuer: %w", err)
	}

	utils.SuccessMessage("Grapple installation completed!")
	return nil
}

// waitForDeployment waits for a deployment to be ready
func waitForDeployment(kubeClient *apiv1.Clientset, namespace, name string) error {
	for {
		deployment, err := kubeClient.AppsV1().Deployments(namespace).Get(context.TODO(), name, v1.GetOptions{})
		if err != nil {
			return err
		}

		if deployment.Status.ReadyReplicas == *deployment.Spec.Replicas {
			return nil
		}

		utils.InfoMessage(fmt.Sprintf("Waiting for deployment %s in namespace %s to be ready...", name, namespace))
		time.Sleep(5 * time.Second)
	}
}

// getClusterExternalIP waits for and retrieves the external IP of a LoadBalancer service
func getClusterExternalIP(restConfig *rest.Config, namespace, serviceName string) (string, error) {
	// Maximum wait time and interval
	maxWait := 300 * time.Second
	interval := 5 * time.Second
	deadline := time.Now().Add(maxWait)

	utils.InfoMessage(fmt.Sprintf("Waiting for the external IP of LoadBalancer '%s' in namespace '%s'", serviceName, namespace))

	// Create client from restConfig
	clientset, err := apiv1.NewForConfig(restConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	for time.Now().Before(deadline) {
		service, err := clientset.CoreV1().Services(namespace).Get(context.TODO(), serviceName, v1.GetOptions{})
		if err != nil {
			if !errors.IsNotFound(err) {
				return "", fmt.Errorf("failed to get service %s in namespace %s: %w", serviceName, namespace, err)
			}
			// Service not found yet, continue waiting
			fmt.Print(".")
			time.Sleep(interval)
			continue
		}

		// Check if external IP is assigned
		if len(service.Status.LoadBalancer.Ingress) > 0 {
			var externalIP string
			if service.Status.LoadBalancer.Ingress[0].IP != "" {
				externalIP = service.Status.LoadBalancer.Ingress[0].IP
			} else if service.Status.LoadBalancer.Ingress[0].Hostname != "" {
				externalIP = service.Status.LoadBalancer.Ingress[0].Hostname
			}

			if externalIP != "" {
				utils.InfoMessage(fmt.Sprintf("\nExternal IP for LoadBalancer '%s': %s", serviceName, externalIP))
				return externalIP, nil
			}
		}

		fmt.Print(".")
		time.Sleep(interval)
	}

	return "", fmt.Errorf("timeout: external IP not assigned for service '%s' in namespace '%s' within %v",
		serviceName, namespace, maxWait)
}

func helmInstallOrUpgrade(kubeClient apiv1.Interface, releaseName, namespace, chartVersion, valuesFile string) error {

	utils.StartSpinner(fmt.Sprintf("Installing/upgrading release %s...", releaseName))
	defer utils.StopSpinner()

	// check and create namespace if it doesn't exist
	checkAndCreateNamespace(kubeClient, namespace)

	// Mirrors the Bash variables
	awsRegistry := "p7h7z5g3"

	// Construct the OCI chart reference without version in URL
	// Example: "oci://public.ecr.aws/p7h7z5g3/grsf-init"
	chartRef := fmt.Sprintf("oci://public.ecr.aws/%s/%s", awsRegistry, releaseName)

	utils.InfoMessage(fmt.Sprintf("chartRef: %s", chartRef))

	// Create the Helm settings (used for CLI-based defaults)
	settings := cli.New()
	settings.SetNamespace(namespace)
	// Prepare an action.Configuration, which wires up Helm internals
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(
		settings.RESTClientGetter(),
		namespace,
		os.Getenv("HELM_DRIVER"), // defaults to "secret" if empty
		log.Printf,
	); err != nil {
		return fmt.Errorf("failed to initialize Helm action configuration: %v", err)
	}

	// Create a registry client (for pulling OCI charts)
	regClient, err := registry.NewClient()
	if err != nil {
		return fmt.Errorf("failed to create registry client: %v", err)
	}
	actionConfig.RegistryClient = regClient

	// Check if release exists
	histClient := action.NewHistory(actionConfig)
	histClient.Max = 1
	_, err = histClient.Run(releaseName)

	if err != nil {
		// Release doesn't exist, do install
		installClient := action.NewInstall(actionConfig)
		installClient.Namespace = namespace
		installClient.ReleaseName = releaseName
		installClient.ChartPathOptions.Version = chartVersion
		installClient.SkipCRDs = true

		// Locate the chart (pull it if needed) and get a local path
		chartPath, err := installClient.ChartPathOptions.LocateChart(chartRef, settings)
		if err != nil {
			return fmt.Errorf("failed to locate chart from %q: %v", chartRef, err)
		}

		utils.InfoMessage(fmt.Sprintf("Chartpath %v", chartPath))

		// Load the chart from the local path
		chartLoaded, err := loader.Load(chartPath)
		if err != nil {
			return fmt.Errorf("failed to load chart: %v", err)
		}

		// deploymentPath, err := utils.GetResourcePath("template-files")
		// if err != nil {
		// 	return fmt.Errorf("failed to get deployment path: %w", err)
		// }
		deploymentPath := "template-files"
		valuesFileForK3d := filepath.Join(deploymentPath, "values-k3d.yaml")

		valueOpts := &values.Options{
			ValueFiles: []string{valuesFile, valuesFileForK3d},
		}
		vals, err := valueOpts.MergeValues(getter.All(settings))
		if err != nil {
			return fmt.Errorf("failed to merge values from %q: %v", valuesFile, err)
		}

		utils.InfoMessage("Values from file:")
		for key, value := range vals {
			switch v := value.(type) {
			case map[string]interface{}:
				utils.InfoMessage(fmt.Sprintf("%s:", key))
				for subKey, subValue := range v {
					utils.InfoMessage(fmt.Sprintf("  %s: %v", subKey, subValue))
				}
			default:
				utils.InfoMessage(fmt.Sprintf("%s: %v", key, value))
			}
		}

		// Run the install
		rel, err := installClient.Run(chartLoaded, vals)
		if err != nil {
			return fmt.Errorf("failed to install chart %q: %v", chartRef, err)
		}

		utils.SuccessMessage(fmt.Sprintf("\nSuccessfully installed release %q in namespace %q, chart version: %s",
			rel.Name, rel.Namespace, rel.Chart.Metadata.Version))

	} else {
		// Release exists, do upgrade
		upgradeClient := action.NewUpgrade(actionConfig)
		upgradeClient.Namespace = namespace
		upgradeClient.ChartPathOptions.Version = chartVersion
		upgradeClient.SkipCRDs = true

		// Locate the chart (pull it if needed) and get a local path
		chartPath, err := upgradeClient.ChartPathOptions.LocateChart(chartRef, settings)
		if err != nil {
			return fmt.Errorf("failed to locate chart from %q: %v", chartRef, err)
		}

		// Load the chart from the local path
		chartLoaded, err := loader.Load(chartPath)
		if err != nil {
			return fmt.Errorf("failed to load chart: %v", err)
		}

		// deploymentPath, err := utils.GetResourcePath("template-files")
		// if err != nil {
		// 	return fmt.Errorf("failed to get deployment path: %w", err)
		// }
		deploymentPath := "template-files"
		valuesFileForK3d := filepath.Join(deploymentPath, "values-k3d.yaml")

		valueOpts := &values.Options{
			ValueFiles: []string{valuesFile, valuesFileForK3d},
		}
		vals, err := valueOpts.MergeValues(getter.All(settings))
		if err != nil {
			return fmt.Errorf("failed to merge values from %q: %v", valuesFile, err)
		}

		utils.InfoMessage("Values from file:")
		for key, value := range vals {
			switch v := value.(type) {
			case map[string]interface{}:
				utils.InfoMessage(fmt.Sprintf("%s:", key))
				for subKey, subValue := range v {
					utils.InfoMessage(fmt.Sprintf("  %s: %v", subKey, subValue))
				}
			default:
				utils.InfoMessage(fmt.Sprintf("%s: %v", key, value))
			}
		}
		// Run the upgrade
		rel, err := upgradeClient.Run(releaseName, chartLoaded, vals)
		if err != nil {
			return fmt.Errorf("failed to upgrade chart %q: %v", chartRef, err)
		}

		utils.SuccessMessage(fmt.Sprintf("\nSuccessfully upgraded release %q in namespace %q, chart version: %s",
			rel.Name, rel.Namespace, rel.Chart.Metadata.Version))

	}
	return nil
}
