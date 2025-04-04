package civo

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/civo/civogo"
	"github.com/grapple-solution/grapple_cli/utils" // your logging/prompting
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/repo"

	// Helm libraries

	// Kubernetes libraries

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiv1 "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// InstallCmd is your Cobra command
var InstallCmd = &cobra.Command{
	Use:     "install",
	Aliases: []string{"i"},
	Short:   "Install Grapple on a Civo Kubernetes cluster (step by step)",
	Long: `Installs Grapple components (grsf-init, grsf, grsf-config, grsf-integration) 
sequentially, waiting for required resources in between, mirroring the step-by-step logic of your Bash script.`,
	RunE: runInstallStepByStep,
}

// init sets up flags for install
func init() {
	InstallCmd.Flags().StringVar(&grappleVersion, "grapple-version", "latest", "Version of Grapple to install (default: latest)")
	InstallCmd.Flags().BoolVar(&autoConfirm, "auto-confirm", false, "Skip confirmation prompts (default: false)")
	InstallCmd.Flags().StringVar(&civoRegion, "civo-region", "", "Civo region")
	InstallCmd.Flags().StringVar(&clusterName, "cluster-name", "", "Civo cluster name")
	InstallCmd.Flags().StringVar(&civoClusterID, "civo-cluster-id", "", "Civo cluster ID")
	InstallCmd.Flags().StringVar(&civoEmailAddress, "civo-email-address", "", "Civo email address")
	InstallCmd.Flags().StringVar(&clusterIP, "cluster-ip", "", "Cluster IP")
	InstallCmd.Flags().StringVar(&grappleDNS, "grapple-dns", "", "Domain for Grapple (default: {cluster-name}.grapple-solutions.com)")
	InstallCmd.Flags().StringVar(&organization, "organization", "", "Organization name (default: grapple-solutions)")
	InstallCmd.Flags().BoolVar(&installKubeblocks, "install-kubeblocks", false, "Install Kubeblocks in background (default: false)")
	InstallCmd.Flags().BoolVar(&waitForReady, "wait", false, "Wait for Grapple to be fully ready at the end (default: false)")
	InstallCmd.Flags().BoolVar(&sslEnable, "ssl-enable", false, "Enable SSL usage (default: false)")
	InstallCmd.Flags().StringVar(&sslIssuer, "ssl-issuer", "letsencrypt-grapple-demo", "SSL Issuer (default: letsencrypt-grapple-demo)")

}

// runInstallStepByStep is the main function
func runInstallStepByStep(cmd *cobra.Command, args []string) error {

	logFile, logOnFileStart, logOnCliAndFileStart := utils.GetLogWriters("/tmp/grpl_civo_install.log")

	var err error

	defer func() {
		logFile.Sync() // Ensure logs are flushed before closing
		logFile.Close()
		if err != nil {
			utils.ErrorMessage("Failed to install grpl, please run cat /tmp/grpl_civo_install.log for more details")
		}
	}()

	// Start logging to both CLI and file
	logOnCliAndFileStart()

	connectToCivoCluster := func() error {
		// Instead of duplicating connection logic, use the connect command
		err := connectToCluster(cmd, args)
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to connect to cluster: %v", err))
			return err
		}
		return nil
	}

	// 1) Create/fetch the Civo client and cluster info, build a Kube + Helm client
	kubeClient, restConfig, err := initClientsAndConfig(connectToCivoCluster)
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

	logOnFileStart()
	err = setupTraefik(restConfig)
	logOnCliAndFileStart()
	if err != nil {
		return fmt.Errorf("failed to setup Traefik: %w", err)
	}
	utils.SuccessMessage("Loadbalancer setup completed.")

	valuesFiles := []string{"/tmp/values-override.yaml"}
	// Step 3) Deploy "grsf-init"
	utils.InfoMessage("Deploying 'grsf-init' chart...")
	logOnFileStart()
	err = utils.HelmDeployGrplReleasesWithRetry(kubeClient, "grsf-init", "grpl-system", grappleVersion, valuesFiles)
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

	// // Step 4) Deploy "grsf"
	utils.InfoMessage("Deploying 'grsf' chart...")
	logOnFileStart()
	err = utils.HelmDeployGrplReleasesWithRetry(kubeClient, "grsf", "grpl-system", grappleVersion, valuesFiles)
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

	// // Step 5) Deploy "grsf-config"
	utils.InfoMessage("Deploying 'grsf-config' chart...")
	logOnFileStart()
	err = utils.HelmDeployGrplReleasesWithRetry(kubeClient, "grsf-config", "grpl-system", grappleVersion, valuesFiles)
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
	if err := utils.HelmDeployGrplReleasesWithRetry(kubeClient, "grsf-integration", "grpl-system", grappleVersion, valuesFiles); err != nil {
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

	// Step 7) SSL enabling (placeholder)
	if sslEnable {
		utils.InfoMessage("Enabling SSL (applying clusterissuer, etc.) - placeholder logic.")
		logOnFileStart()
		err = utils.CreateClusterIssuer(kubeClient, sslEnable)
		logOnCliAndFileStart()
		if err != nil {
			return fmt.Errorf("failed to create clusterissuer: %w", err)
		}
		utils.InfoMessage("Successfully created clusterissuer.")
	}

	// // Step 8) If user wants to wait for the entire Grapple system
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

	clusterIP, err = utils.GetClusterExternalIP(restConfig, "traefik", "traefik")
	if err != nil {
		return fmt.Errorf("failed to get k3d cluster IP: %w", err)
	}

	// 2) If domain is NOT resolvable, create your DNS route53 upsert job (placeholder)
	if !utils.IsResolvable(utils.ExtractDomain(grappleDNS)) {
		utils.InfoMessage("Domain not resolvable. Creating DNS upsert job...")
		code := utils.GenerateRandomString()
		if err := utils.SetupCodeVerificationServer(restConfig, code, completeDomain, "civo"); err != nil {
			utils.ErrorMessage("Failed to setup code verification server: " + err.Error())
			return err
		}
		apiURL := "https://0anfj8jy8j.execute-api.eu-central-1.amazonaws.com/prod/grpl-route53-dns-manager-v2"
		if err := utils.UpsertDNSRecord(restConfig, apiURL, completeDomain, code, clusterIP, "Z008820536Y8KC83QNPB2", "A"); err != nil {
			utils.ErrorMessage("Failed to upsert DNS record: " + err.Error())
			return err
		}
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

	utils.RemoveCodeVerificationServer(restConfig)

	utils.SuccessMessage("Grapple installation completed!")
	return nil
}

func prepareValuesFile() error {
	// Create values map
	values := map[string]interface{}{
		"clusterdomain": completeDomain,
		"config": map[string]interface{}{
			// Common fields
			utils.SecKeyEmail:               civoEmailAddress,
			utils.SecKeyOrganization:        organization,
			utils.SecKeyClusterdomain:       completeDomain,
			utils.SecKeyGrapiversion:        "0.0.1",
			utils.SecKeyGruimversion:        "0.0.1",
			utils.SecKeyDev:                 "false",
			utils.SecKeySsl:                 fmt.Sprintf("%v", sslEnable),
			utils.SecKeySslissuer:           sslIssuer,
			utils.SecKeyClusterName:         clusterName,
			utils.SecKeyGrapleDNS:           completeDomain,
			utils.SecKeyGrapleVersion:       grappleVersion,
			utils.SecKeyGrapleLicense:       grappleLicense,
			utils.SecKeyProviderClusterType: utils.ProviderClusterTypeCivo,

			// Civo specific fields
			utils.SecKeyCivoClusterID: civoClusterID,
			utils.SecKeyCivoRegion:    civoRegion,
			utils.SecKeyCivoMasterIP:  clusterIP,
		},
	}

	// Marshal to YAML
	yamlData, err := yaml.Marshal(values)
	if err != nil {
		return fmt.Errorf("failed to marshal values to YAML: %w", err)
	}

	// Write to temp file
	if err := os.WriteFile("/tmp/values-override.yaml", yamlData, 0644); err != nil {
		return fmt.Errorf("failed to write values file: %w", err)
	}

	// Print values if needed
	if !autoConfirm {
		utils.InfoMessage("Going to deploy grpl on CIVO with following configurations")

		utils.InfoMessage(fmt.Sprintf("civo-cluster-id: %s", civoClusterID))
		utils.InfoMessage(fmt.Sprintf("cluster-name: %s", clusterName))
		utils.InfoMessage(fmt.Sprintf("civo-region: %s", civoRegion))
		utils.InfoMessage(fmt.Sprintf("civo-email-address: %s", civoEmailAddress))
		utils.InfoMessage(fmt.Sprintf("cluster-ip: %s", clusterIP))
		utils.InfoMessage(fmt.Sprintf("grapple-version: %s", grappleVersion))
		utils.InfoMessage(fmt.Sprintf("grapple-dns: %s", completeDomain))
		utils.InfoMessage(fmt.Sprintf("grapple-license: %s", grappleLicense))
		utils.InfoMessage(fmt.Sprintf("organization: %s", organization))
		utils.InfoMessage(fmt.Sprintf("email: %s", civoEmailAddress))

		if confirmed, err := utils.PromptConfirm("Proceed with deployment using the values above?"); err != nil || !confirmed {
			return fmt.Errorf("failed to install grpl: user cancelled")
		}
	}

	return nil
}

// -----------------------------------------------------------------------------
// initClientsAndConfig: does the following:
// 1) Create a civo client from flags
// 2) Retrieve the cluster's kubeconfig
// 3) Build a K8s client-go client
// -----------------------------------------------------------------------------
func initClientsAndConfig(connectToCivoCluster func() error) (apiv1.Interface, *rest.Config, error) {
	// Check if running inside CIVO cluster
	insideCivoCluster := false
	if civoClusterID != "" {
		utils.InfoMessage("Running inside a CIVO Kubernetes cluster")
		insideCivoCluster = true
	}

	var client *civogo.Client
	var k8sClient *apiv1.Clientset
	var restConfig *rest.Config
	var err error

	if !insideCivoCluster {
		// Get CIVO API key if not provided
		civoAPIKey := getCivoAPIKey()

		// Get CIVO region if not provided
		if civoRegion == "" {
			regions := getCivoRegion(civoAPIKey)
			result, err := utils.PromptSelect("Select region", regions)
			if err != nil {
				utils.ErrorMessage("Region selection is required")
				return nil, nil, fmt.Errorf("region selection is required")
			}
			civoRegion = result
		}

		client, err = civogo.NewClient(civoAPIKey, civoRegion)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create civo client: %w", err)
		}

		// Get cluster info
		if clusterName == "" {
			clusters, err := client.ListKubernetesClusters()
			if err != nil {
				return nil, nil, fmt.Errorf("failed to list clusters: %w", err)
			}

			clusterNames := make([]string, len(clusters.Items))
			for i, c := range clusters.Items {
				clusterNames[i] = c.Name
			}

			result, err := utils.PromptSelect("Select CIVO cluster", clusterNames)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to select cluster: %w", err)
			}
			clusterName = result
		}

		err = connectToCivoCluster()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to connect to civo cluster: %w", err)
		}

		var cluster *civogo.KubernetesCluster
		cluster, err = findClusterByName(client, clusterName)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get civo cluster: %w", err)
		}

		// Get cluster ID if not provided
		if civoClusterID == "" {
			civoClusterID = cluster.ID
			utils.InfoMessage(fmt.Sprintf("Using cluster ID: %s", civoClusterID))
		}

		if !cluster.Ready {
			utils.InfoMessage("Waiting for cluster to be ready...")
			if err := waitForClusterReady(client, cluster); err != nil {
				return nil, nil, err
			}
		}

		// Get Grapple DNS if not provided
		if grappleDNS == "" {
			grappleDNS = clusterName
			utils.InfoMessage(fmt.Sprintf("Using cluster name as Grapple DNS: %s.grapple-demo.com", grappleDNS))
		}

		// Get CIVO email address if not provided
		if civoEmailAddress == "" {
			result, err := utils.PromptInput("Enter CIVO email address", utils.DefaultValue, utils.EmailRegex)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to get email address: %w", err)
			}
			civoEmailAddress = result

			// Set organization from email domain if not already set
			if organization == "" {
				parts := strings.Split(civoEmailAddress, "@")
				if len(parts) == 2 {
					organization = parts[1]
				}
			}
		}

		// clusterIP = cluster.MasterIP
		// utils.InfoMessage(fmt.Sprintf("Selected civo master ip: %s", clusterIP))
		// Build restConfig from civo cluster's kubeconfig
		kubeconfigBytes := []byte(cluster.KubeConfig)
		restConfig, err = clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get email address: %w", err)
		}
		k8sClient, err = apiv1.NewForConfig(restConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create kubernetes client: %w", err)
		}

	} else {
		// Inside cluster, use in-cluster config
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get in-cluster config: %w", err)
		}

		k8sClient, err := apiv1.NewForConfig(restConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create kubernetes client: %w", err)
		}

		// Get cluster IP when inside cluster
		utils.InfoMessage("Retrieving cluster IP from kubectl cluster-info")
		utils.InfoMessage("Waiting for cluster IP to be ready (30 seconds max)")

		timeout := 30
		elapsed := 0
		var clusterIP string
		for clusterIP == "" && elapsed < timeout {
			nodes, err := k8sClient.CoreV1().Nodes().List(context.Background(), v1.ListOptions{})
			if err != nil {
				return nil, nil, fmt.Errorf("failed to list nodes: %w", err)
			}

			for _, node := range nodes.Items {
				for _, addr := range node.Status.Addresses {
					if addr.Type == "ExternalIP" {
						clusterIP = addr.Address
						break
					}
				}
				if clusterIP != "" {
					break
				}
			}

			if clusterIP == "" {
				time.Sleep(10 * time.Second)
				elapsed += 10
			}
		}

		if clusterIP == "" {
			utils.InfoMessage("")
			utils.InfoMessage("Unable to retrieve cluster IP within 30 seconds")
		}

		time.Sleep(2 * time.Second)
	}

	if grappleVersion == "" || grappleVersion == "latest" {
		grappleVersion = "0.2.8"
	}

	// Define grappleDomain variable
	var grappleDomain string

	// Check if a full domain name was passed in grappleDNS
	if grappleDNS != "" {
		if !utils.IsResolvable(utils.ExtractDomain(grappleDNS)) {
			utils.InfoMessage(fmt.Sprintf("DNS name %s is not a FQDN", grappleDNS))
			grappleDomain = ".grapple-demo.com"
		}
	}

	// Set default grappleDNS if empty
	if grappleDNS == "" {
		grappleDNS = clusterName
		grappleDomain = ".grapple-demo.com"
	}

	// Set default organization if empty
	if organization == "" {
		organization = "grapple solutions AG"
	}

	// Create complete domain
	if utils.IsResolvable(utils.ExtractDomain(grappleDNS)) {
		completeDomain = grappleDNS
	} else {
		completeDomain = grappleDNS + grappleDomain
	}

	// Get license from grsf-config secret if it exists, otherwise use "free"
	secret, err := k8sClient.CoreV1().Secrets("grpl-system").Get(context.Background(), "grsf-config", v1.GetOptions{})
	if err != nil {
		grappleLicense = "free"
	} else {
		if licBytes, ok := secret.Data["LIC"]; !ok || len(licBytes) == 0 {
			grappleLicense = "free"
		} else {
			grappleLicense = string(licBytes)
		}
	}

	return k8sClient, restConfig, nil
}

// findClusterByName attempts to get a cluster by listing and matching name
func findClusterByName(client *civogo.Client, name string) (*civogo.KubernetesCluster, error) {
	list, err := client.ListKubernetesClusters()
	if err != nil {
		return nil, err
	}
	for _, c := range list.Items {
		if c.Name == name {
			return &c, nil
		}
	}
	return nil, fmt.Errorf("no cluster found with name '%s'", name)
}

// setupTraefik installs Traefik as a load balancer in the Kubernetes cluster
func setupTraefik(restConfig *rest.Config) error {

	utils.StartSpinner("Setting up Traefik load balancer...")
	defer utils.StopSpinner()

	// Initialize Helm client
	helmCfg, err := utils.GetHelmConfig(restConfig, "traefik")
	if err != nil {
		utils.ErrorMessage("Failed to initialize Helm configuration: " + err.Error())
		return err
	}

	// Check if Traefik is already installed
	listClient := action.NewList(helmCfg)
	listClient.AllNamespaces = true
	releases, err := listClient.Run()
	if err != nil {
		utils.ErrorMessage("Failed to list releases: " + err.Error())
		return err
	}

	traefikInstalled := false
	for _, release := range releases {
		if release.Name == "traefik" {
			traefikInstalled = true
			break
		}
	}

	if !traefikInstalled {
		utils.InfoMessage("Installing Traefik...")

		// Create Helm environment settings
		settings := cli.New()
		settings.SetNamespace("traefik")

		// Add the Traefik Helm repository
		repoEntry := repo.Entry{
			Name: "traefik",
			URL:  "https://helm.traefik.io/traefik",
		}

		chartRepo, err := repo.NewChartRepository(&repoEntry, getter.All(settings))
		if err != nil {
			utils.ErrorMessage("Failed to create chart repository object: " + err.Error())
			return err
		}

		// Add repo to repositories.yaml
		repoFile := settings.RepositoryConfig
		b, err := os.ReadFile(repoFile)
		if err != nil && !os.IsNotExist(err) {
			utils.ErrorMessage("Failed to read repository file: " + err.Error())
			return err
		}

		var f repo.File
		if err := yaml.Unmarshal(b, &f); err != nil {
			utils.ErrorMessage("Failed to unmarshal repository file: " + err.Error())
			return err
		}

		// Add new repo or update existing
		f.Add(&repoEntry)

		if err := f.WriteFile(repoFile, 0644); err != nil {
			utils.ErrorMessage("Failed to write repository file: " + err.Error())
			return err
		}

		_, err = chartRepo.DownloadIndexFile()
		if err != nil {
			utils.ErrorMessage("Failed to download repository index: " + err.Error())
			return err
		}

		// Create install client
		installClient := action.NewInstall(helmCfg)
		installClient.Namespace = "traefik"
		installClient.CreateNamespace = true
		installClient.ReleaseName = "traefik"
		installClient.Version = ""

		// Locate and load the chart
		chartPath, err := installClient.ChartPathOptions.LocateChart("traefik/traefik", settings)
		if err != nil {
			utils.ErrorMessage("Failed to locate Traefik chart: " + err.Error())
			return err
		}

		// Load chart
		chart, err := loader.Load(chartPath)
		if err != nil {
			utils.ErrorMessage("Failed to load Traefik chart: " + err.Error())
			return err
		}

		// Set values
		values := map[string]interface{}{
			"service": map[string]interface{}{
				"type": "LoadBalancer",
			},
			"ports": map[string]interface{}{
				"web": map[string]interface{}{
					"port": 80,
				},
				"websecure": map[string]interface{}{
					"port": 443,
				},
			},
		}

		// Install chart
		_, err = installClient.Run(chart, values)
		if err != nil {
			utils.ErrorMessage("Failed to install Traefik: " + err.Error())
			return err
		}

		utils.InfoMessage("Traefik installed successfully")
	} else {
		utils.InfoMessage("Traefik already installed")
	}

	return nil
}
