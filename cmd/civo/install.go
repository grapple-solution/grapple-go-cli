package civo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/civo/civogo"
	"github.com/grapple-solution/grapple_cli/utils" // your logging/prompting
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	// Helm libraries
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/cli/values"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/registry"

	// Kubernetes libraries

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
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

	// // Remove the DNS job if it was created:
	// if !isResolvable(extractDomain(grappleDNS)) {
	// 	deleteDnsRoute53UpsertJob(kubeClient, grappleDNS)
	// }

	// // Step 4) Deploy "grsf"
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

	// // Step 5) Deploy "grsf-config"
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

	// Step 7) SSL enabling (placeholder)
	if sslEnable {
		utils.InfoMessage("Enabling SSL (applying clusterissuer, etc.) - placeholder logic.")
		logOnFileStart()
		err = createClusterIssuer(kubeClient)
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

	utils.RemoveCodeVerificationServer(restConfig)

	utils.SuccessMessage("Grapple installation completed!")
	return nil
}

func waitForGrappleReady(restConfig *rest.Config) error {
	// Wait for all Crossplane packages to be healthy
	utils.InfoMessage("Waiting for grpl to be ready")

	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {

		dynamicClient, err := dynamic.NewForConfig(restConfig)
		if err != nil {
			return fmt.Errorf("failed to create dynamic client: %w", err)
		}

		// Try to list all types of packages (providers, configurations, functions)
		gvr := schema.GroupVersionResource{Group: "pkg.crossplane.io", Version: "v1", Resource: "configurations"}

		var grplPackage unstructured.Unstructured
		pkgList, err := dynamicClient.Resource(gvr).List(context.TODO(), v1.ListOptions{})
		if err != nil {
			if !strings.Contains(err.Error(), "the server could not find the requested resource") {
				utils.ErrorMessage(fmt.Sprintf("Failed to list Crossplane %s for grpl: %v", gvr.Resource, err))
				return err
			}
			continue
		}

		for _, pkg := range pkgList.Items {
			if pkg.GetName() == "grpl" {
				grplPackage = pkg
				break
			}
		}

		utils.InfoMessage(fmt.Sprintf("Checking package %s", grplPackage.GetName()))
		conditions, found, err := unstructured.NestedSlice(grplPackage.Object, "status", "conditions")
		if err != nil || !found {
			utils.InfoMessage(fmt.Sprintf("Package %s not yet healthy", grplPackage.GetName()))
			continue
		}

		isHealthy := false
		for _, condition := range conditions {
			conditionMap := condition.(map[string]interface{})
			if conditionMap["type"] == "Healthy" && conditionMap["status"] == "True" {
				utils.SuccessMessage("grpl is ready")
				return nil
			}
		}

		if !isHealthy {
			utils.InfoMessage(fmt.Sprintf("Package %s not yet healthy", grplPackage.GetName()))
			continue
		}

		time.Sleep(10 * time.Second)
	}

	return fmt.Errorf("timeout waiting for Crossplane packages to be healthy")
}

func prepareValuesFile() error {
	// Create values map
	values := map[string]interface{}{
		"clusterdomain": completeDomain,
		"config": map[string]interface{}{
			// Common fields
			secKeyEmail:               civoEmailAddress,
			secKeyOrganization:        organization,
			secKeyClusterdomain:       completeDomain,
			secKeyGrapiversion:        "0.0.1",
			secKeyGruimversion:        "0.0.1",
			secKeyDev:                 "false",
			secKeySsl:                 fmt.Sprintf("%v", sslEnable),
			secKeySslissuer:           sslIssuer,
			secKeyClusterName:         clusterName,
			secKeyGrapleDNS:           completeDomain,
			secKeyGrapleVersion:       grappleVersion,
			secKeyGrapleLicense:       grappleLicense,
			secKeyProviderClusterType: providerClusterTypeCivo,

			// Civo specific fields
			secKeyCivoClusterID: civoClusterID,
			secKeyCivoRegion:    civoRegion,
			secKeyCivoMasterIP:  clusterIP,
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

		clusterIP = cluster.MasterIP
		utils.InfoMessage(fmt.Sprintf("Selected civo master ip: %s", clusterIP))
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

// // waitForClusterReady polls civo cluster readiness
// func waitForClusterReady(client *civogo.Client, cluster *civogo.KubernetesCluster) error {
// 	deadline := time.Now().Add(5 * time.Minute)
// 	for time.Now().Before(deadline) {
// 		updated, err := client.GetKubernetesCluster(cluster.ID)
// 		if err == nil && updated.Ready {
// 			utils.InfoMessage("Civo cluster is ready now.")
// 			return nil
// 		}
// 		time.Sleep(10 * time.Second)
// 	}
// 	return fmt.Errorf("cluster '%s' not ready within timeout", cluster.Name)
// }

// -----------------------------------------------------------------------------
// Step-by-step Deploy Functions (mirroring the Bash steps)
// -----------------------------------------------------------------------------

// helmDeployReleaseWithRetry tries to install/upgrade a Helm chart up to 3 times
func helmDeployReleaseWithRetry(kubeClient apiv1.Interface, releaseName, namespace, version, valuesFile string) error {
	const maxRetries = 3
	var err error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		err = helmInstallOrUpgrade(kubeClient, releaseName, namespace, version, valuesFile)
		if err == nil {
			return nil
		}
		utils.InfoMessage(fmt.Sprintf("Attempt %d/%d for %s failed: %v", attempt, maxRetries, releaseName, err))

		// The Bash script logs out of ECR registry if it fails.
		// There's no direct "helm registry logout" equivalent in the Helm Go SDK.
		// This is just a placeholder if you have custom logic to re-auth with the registry.
		if attempt < maxRetries {
			utils.InfoMessage("Retrying after re-auth (placeholder).")
			// e.g. re-auth to registry here
		}
	}
	return fmt.Errorf("helm deploy of %s failed after %d attempts: %w", releaseName, maxRetries, err)
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

		// Merge values from the file (like '/tmp/values-override.yaml')
		valueOpts := &values.Options{
			ValueFiles: []string{valuesFile},
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

		// Merge values from the file (like '-f /tmp/values-override.yaml')
		valueOpts := &values.Options{
			ValueFiles: []string{valuesFile},
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

func checkAndCreateNamespace(kubeClient apiv1.Interface, namespace string) error {
	// Try to retrieve the namespace
	_, err := kubeClient.CoreV1().Namespaces().Get(context.Background(), namespace, v1.GetOptions{})
	if err == nil {
		// Namespace already exists
		return nil
	}

	// If the error says "NotFound," then we need to create the namespace
	if errors.IsNotFound(err) {
		_, createErr := kubeClient.CoreV1().Namespaces().Create(
			context.Background(),
			&corev1.Namespace{
				ObjectMeta: v1.ObjectMeta{
					Name: namespace,
				},
			},
			v1.CreateOptions{},
		)
		if createErr != nil {
			return fmt.Errorf("failed to create namespace %q: %w", namespace, createErr)
		}
		return nil
	}

	// If it's any other error, return it
	return fmt.Errorf("failed to get namespace %q: %w", namespace, err)
}

// waitForGrsfInit checks for cert-manager, crossplane, external secrets, etc.
func waitForGrsfInit(kubeClient apiv1.Interface) error {

	// STEP 1: Check if traefik is installed in kube-system namespace
	_, err := kubeClient.AppsV1().Deployments("kube-system").Get(context.TODO(), "traefik", v1.GetOptions{})
	if err == nil {
		// Wait for Middleware CRD if traefik exists
		utils.InfoMessage("Waiting for Middleware CRD...")
		discoveryClient := kubeClient.Discovery()
		for attempts := 0; attempts < 30; attempts++ {
			_, resources, err := discoveryClient.ServerGroupsAndResources()
			if err != nil {
				time.Sleep(time.Second)
				continue
			}

			crdFound := false
			for _, list := range resources {
				for _, r := range list.APIResources {
					if r.Kind == "Middleware" {
						crdFound = true
						utils.SuccessMessage("Middleware CRD is available")
						break
					}
				}
				if crdFound {
					break
				}
			}

			if crdFound {
				break
			}

			utils.InfoMessage("Waiting for Middleware CRD...")
			time.Sleep(time.Second)
		}
	}

	// STEP 2: Check if cert-manager is installed in grpl-system namespace
	for attempts := 0; attempts < 30; attempts++ {
		deployment, err := kubeClient.AppsV1().Deployments("grpl-system").Get(context.TODO(), "grsf-init-cert-manager", v1.GetOptions{})
		if err != nil {
			utils.InfoMessage("Waiting for cert-manager deployment...")
			time.Sleep(10 * time.Second)
			continue
		}

		if deployment.Status.AvailableReplicas == *deployment.Spec.Replicas {
			utils.SuccessMessage("Cert-manager deployment is available")
			break
		}

		utils.InfoMessage("Waiting for cert-manager replicas to be ready...")
		time.Sleep(10 * time.Second)
	}

	// Wait for ClusterIssuer CRD
	discoveryClient := kubeClient.Discovery()
	for attempts := 0; attempts < 30; attempts++ {
		_, resources, err := discoveryClient.ServerGroupsAndResources()
		if err != nil {
			utils.InfoMessage("Waiting for ClusterIssuer CRD...")
			time.Sleep(10 * time.Second)
			continue
		}

		crdFound := false
		for _, list := range resources {
			for _, r := range list.APIResources {
				if r.Kind == "ClusterIssuer" {
					crdFound = true
					utils.SuccessMessage("ClusterIssuer CRD is available")
					break
				}
			}
			if crdFound {
				break
			}
		}

		if crdFound {
			break
		}

		utils.InfoMessage("Waiting for ClusterIssuer CRD...")
		time.Sleep(10 * time.Second)
	}

	// STEP 3: Check if crossplane is installed in grpl-system namespace
	_, err = kubeClient.AppsV1().Deployments("grpl-system").Get(context.TODO(), "crossplane", v1.GetOptions{})
	if err == nil {
		// Wait for Provider CRD
		discoveryClient := kubeClient.Discovery()
		for attempts := 0; attempts < 30; attempts++ {
			_, resources, err := discoveryClient.ServerGroupsAndResources()
			if err != nil {
				utils.InfoMessage("Waiting for Provider CRD...")
				time.Sleep(10 * time.Second)
				continue
			}

			crdFound := false
			for _, list := range resources {
				for _, r := range list.APIResources {
					if r.Kind == "Provider" {
						crdFound = true
						utils.SuccessMessage("Provider CRD is available")
						break
					}
				}
				if crdFound {
					break
				}
			}

			if crdFound {
				break
			}

			utils.InfoMessage("Waiting for Provider CRD...")
			time.Sleep(10 * time.Second)
		}
	}

	// STEP 4: Check if external-secrets webhook is installed and ready
	_, err = kubeClient.AppsV1().Deployments("grpl-system").Get(context.TODO(), "grsf-init-external-secrets-webhook", v1.GetOptions{})
	if err == nil {

		// Wait for webhook deployment to be ready
		for attempts := 0; attempts < 30; attempts++ {
			deployment, err := kubeClient.AppsV1().Deployments("grpl-system").Get(context.TODO(), "grsf-init-external-secrets-webhook", v1.GetOptions{})
			if err != nil {
				utils.InfoMessage("Waiting for external-secrets webhook deployment...")
				time.Sleep(10 * time.Second)
				continue
			}

			if deployment.Status.AvailableReplicas == *deployment.Spec.Replicas {
				utils.SuccessMessage("External-secrets webhook deployment is available")
				break
			}

			utils.InfoMessage("Waiting for external-secrets webhook replicas to be ready...")
			time.Sleep(10 * time.Second)
		}
	}

	return nil
}

func waitForGrsf(kubeClient apiv1.Interface, ns string) error {
	// Cast the interface back to a *apiv1.Clientset so we can use RESTClient().
	cs, ok := kubeClient.(*apiv1.Clientset)
	if !ok {
		return fmt.Errorf("kubeClient is not a *apiv1.Clientset; got %T", kubeClient)
	}

	// Sleep 10 seconds before checking providers
	time.Sleep(10 * time.Second)

	// STEP 1: Check if provider-civo deployment exists
	_, err := cs.AppsV1().Deployments(ns).Get(context.Background(), "provider-civo", v1.GetOptions{})
	if err == nil {
		// Wait for provider-civo to be healthy
		for attempts := 0; attempts < 30; attempts++ {
			provider, err := cs.RESTClient().Get().
				AbsPath("apis/pkg.crossplane.io/v1/providers/provider-civo").
				Do(context.Background()).
				Raw()
			if err != nil {
				time.Sleep(10 * time.Second)
				continue
			}

			var unstr unstructured.Unstructured
			if err := json.Unmarshal(provider, &unstr); err != nil {
				return fmt.Errorf("failed to unmarshal provider: %w", err)
			}

			conditions, found, err := unstructured.NestedSlice(unstr.Object, "status", "conditions")
			if err != nil || !found {
				time.Sleep(10 * time.Second)
				continue
			}

			healthy := false
			for _, c := range conditions {
				condition := c.(map[string]interface{})
				if condition["type"] == "Healthy" && condition["status"] == "True" {
					healthy = true
					break
				}
			}

			if healthy {
				utils.InfoMessage("Provider-civo is healthy")
				break
			}

			time.Sleep(10 * time.Second)
		}

		// Wait for provider-civo CRD
		utils.InfoMessage("Waiting for provider-civo CRD...")
		for attempts := 0; attempts < 30; attempts++ {
			_, resources, err := cs.Discovery().ServerGroupsAndResources()
			if err != nil {
				time.Sleep(1 * time.Second)
				continue
			}

			found := false
			for _, list := range resources {
				for _, r := range list.APIResources {
					if r.Name == "providerconfigs.civo.crossplane.io" {
						found = true
						utils.InfoMessage("Provider-civo CRD is available")
						break
					}
				}
				if found {
					break
				}
			}

			if found {
				break
			}
			time.Sleep(1 * time.Second)
		}
	}

	// STEP 2: Wait for all packages to be healthy
	pkgs, err := cs.RESTClient().Get().
		AbsPath(fmt.Sprintf("apis/pkg.crossplane.io/v1/namespaces/%s/providers", ns)).
		Do(context.Background()).
		Raw()
	if err == nil {
		var pkgList unstructured.UnstructuredList
		if err := json.Unmarshal(pkgs, &pkgList); err != nil {
			return fmt.Errorf("failed to unmarshal packages: %w", err)
		}

		for _, pkg := range pkgList.Items {
			for attempts := 0; attempts < 30; attempts++ {
				conditions, found, err := unstructured.NestedSlice(pkg.Object, "status", "conditions")
				if err != nil || !found {
					time.Sleep(10 * time.Second)
					continue
				}

				healthy := false
				for _, c := range conditions {
					condition := c.(map[string]interface{})
					if condition["type"] == "Healthy" && condition["status"] == "True" {
						healthy = true
						break
					}
				}

				if healthy {
					break
				}
				time.Sleep(10 * time.Second)
			}
		}
	}

	// STEP 3: Check for provider-helm CRD if deployment exists
	_, err = cs.AppsV1().Deployments(ns).Get(context.Background(), "provider-helm", v1.GetOptions{})
	if err == nil {
		utils.InfoMessage("Waiting for provider-helm CRD...")
		for attempts := 0; attempts < 30; attempts++ {
			_, resources, err := cs.Discovery().ServerGroupsAndResources()
			if err != nil {
				time.Sleep(1 * time.Second)
				continue
			}

			found := false
			for _, list := range resources {
				for _, r := range list.APIResources {
					if r.Name == "providerconfigs.helm.crossplane.io" {
						found = true
						utils.InfoMessage("Provider-helm CRD is available")
						break
					}
				}
				if found {
					break
				}
			}

			if found {
				break
			}
			time.Sleep(1 * time.Second)
		}
	}

	// STEP 4: Check for provider-kubernetes CRD if deployment exists
	_, err = cs.AppsV1().Deployments(ns).Get(context.Background(), "provider-kubernetes", v1.GetOptions{})
	if err == nil {
		utils.InfoMessage("Waiting for provider-kubernetes CRD...")
		for attempts := 0; attempts < 30; attempts++ {
			_, resources, err := cs.Discovery().ServerGroupsAndResources()
			if err != nil {
				time.Sleep(1 * time.Second)
				continue
			}

			found := false
			for _, list := range resources {
				for _, r := range list.APIResources {
					if r.Name == "providerconfigs.apiv1.crossplane.io" {
						found = true
						utils.InfoMessage("Provider-kubernetes CRD is available")
						break
					}
				}
				if found {
					break
				}
			}

			if found {
				break
			}
			time.Sleep(1 * time.Second)
		}
	}

	return nil
}

// waitForGrsfConfig checks for CRDs, XRDs, etc.
// waitForGrsfConfig waits for specific CRDs to be available and waits for all XRDs to reach the "Offered" condition

func waitForGrsfConfig(kubeClient apiv1.Interface, restConfig *rest.Config) error {
	discoveryClient := kubeClient.Discovery()

	var requiredKinds = []string{
		"CompositeManagedApi",
		"CompositeManagedUIModule",
		"CompositeManagedDataSource",
	}

	// 1) Wait for the CRDs to show up via discovery
	found := make(map[string]bool)
	for attempts := 0; attempts < 30; attempts++ {
		// Grab the full list of server groups/resources
		_, resourceLists, err := discoveryClient.ServerGroupsAndResources()
		if err != nil {
			// On an error, just wait and retry
			time.Sleep(time.Second)
			continue
		}

		// Look for each required "kind" in the returned resources
		for _, list := range resourceLists {
			for _, r := range list.APIResources {
				// If the resource's Kind is one of our required ones, mark it found
				if utils.Contains(requiredKinds, r.Kind) {
					found[r.Kind] = true
				}
			}
		}

		// Check if we've found all required kinds
		// Checks if for every requiredKind we have found[kind] == true
		allFound := true
		for _, r := range requiredKinds {
			if !found[r] {
				allFound = false
				break
			}
		}
		if allFound {
			log.Println("All required CRDs are available!")
			break
		}

		log.Println("Waiting for required CRDs to appear...")
		time.Sleep(time.Second)

		// If we've hit the last attempt and not all are found, error
		if attempts == 29 {
			// Checks if for every requiredKind we have found[kind] == true
			allFound := true
			for _, r := range requiredKinds {
				if !found[r] {
					allFound = false
					break
				}
			}
			if !allFound {
				return fmt.Errorf("timeout waiting for all required CRDs to appear")
			}
		}
	}

	// Wait for all XRDs to reach "Offered" condition
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Get list of XRDs
	xrds, err := dynamicClient.Resource(schema.GroupVersionResource{
		Group:    "apiextensions.crossplane.io",
		Version:  "v1",
		Resource: "compositeresourcedefinitions",
	}).List(context.Background(), v1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list XRDs: %w", err)
	}

	// Wait for each XRD to reach "Offered" condition
	for _, xrd := range xrds.Items {
		err = waitForCondition(dynamicClient, xrd.GetName(), "Offered")
		if err != nil {
			return fmt.Errorf("failed waiting for XRD %s: %w", xrd.GetName(), err)
		}
	}

	log.Println("All required CRDs and XRDs are available!")
	return nil
}

func waitForCondition(client dynamic.Interface, xrdName string, condition string) error {
	for attempts := 0; attempts < 30; attempts++ {
		xrd, err := client.Resource(schema.GroupVersionResource{
			Group:    "apiextensions.crossplane.io",
			Version:  "v1",
			Resource: "compositeresourcedefinitions",
		}).Get(context.Background(), xrdName, v1.GetOptions{})

		if err != nil {
			return err
		}

		conditions, found, err := unstructured.NestedSlice(xrd.Object, "status", "conditions")
		if err != nil || !found {
			time.Sleep(time.Second)
			continue
		}

		for _, c := range conditions {
			cond := c.(map[string]interface{})
			if cond["type"] == condition && cond["status"] == "True" {
				return nil
			}
		}

		time.Sleep(time.Second)
	}

	return fmt.Errorf("timeout waiting for condition %s on XRD %s", condition, xrdName)
}

func createClusterIssuer(kubeClient apiv1.Interface) error {
	// Apply clusterissuer.yaml if SSL is enabled
	if sslEnable {
		utils.InfoMessage("Applying SSL cluster issuer configuration...")

		// Read and apply the cluster issuer manifest
		issuerBytes, err := os.ReadFile("files/clusterissuer.yaml")
		if err != nil {
			return fmt.Errorf("failed to read cluster issuer manifest: %w", err)
		}

		// Apply using dynamic client
		config, err := kubeClient.Discovery().RESTClient().Get().RequestURI("/api/v1").DoRaw(context.TODO())
		if err != nil {
			return fmt.Errorf("failed to get REST config: %w", err)
		}

		dynamicClient, err := dynamic.NewForConfig(&rest.Config{Host: string(config)})
		if err != nil {
			return fmt.Errorf("failed to create dynamic client: %w", err)
		}

		decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(issuerBytes), 4096)
		var obj unstructured.Unstructured
		if err := decoder.Decode(&obj); err != nil {
			return fmt.Errorf("failed to decode cluster issuer manifest: %w", err)
		}

		_, err = dynamicClient.Resource(schema.GroupVersionResource{
			Group:    "cert-manager.io",
			Version:  "v1",
			Resource: "clusterissuers",
		}).Create(context.TODO(), &obj, v1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to apply cluster issuer: %w", err)
		}

		utils.SuccessMessage("Applied cluster issuer configuration")
	}

	return nil
}

// waitForGrsfIntegration final checks
func waitForGrsfIntegration(restConfig *rest.Config) error {
	// Wait for all Crossplane packages to be healthy
	utils.InfoMessage("Checking Crossplane package health...")

	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {

		dynamicClient, err := dynamic.NewForConfig(restConfig)
		if err != nil {
			return fmt.Errorf("failed to create dynamic client: %w", err)
		}

		// Try to list all types of packages (providers, configurations, functions)
		gvrs := []schema.GroupVersionResource{
			{Group: "pkg.crossplane.io", Version: "v1", Resource: "providers"},
			{Group: "pkg.crossplane.io", Version: "v1", Resource: "configurations"},
			{Group: "pkg.crossplane.io", Version: "v1beta1", Resource: "functions"},
		}

		var allPackages unstructured.UnstructuredList
		for _, gvr := range gvrs {
			pkgList, err := dynamicClient.Resource(gvr).List(context.TODO(), v1.ListOptions{})
			if err != nil {
				if !strings.Contains(err.Error(), "the server could not find the requested resource") {
					utils.ErrorMessage(fmt.Sprintf("Failed to list Crossplane %s: %v", gvr.Resource, err))
					return err
				}
				continue
			}
			allPackages.Items = append(allPackages.Items, pkgList.Items...)
		}

		packages := &allPackages

		if len(packages.Items) == 0 {
			utils.InfoMessage("No Crossplane packages found yet...")
			time.Sleep(10 * time.Second)
			continue
		}

		allHealthy := true
		for _, pkg := range packages.Items {
			utils.InfoMessage(fmt.Sprintf("Checking package %s", pkg.GetName()))
			conditions, found, err := unstructured.NestedSlice(pkg.Object, "status", "conditions")
			if err != nil || !found {
				allHealthy = false
				break
			}

			isHealthy := false
			for _, condition := range conditions {
				conditionMap := condition.(map[string]interface{})
				if conditionMap["type"] == "Healthy" && conditionMap["status"] == "True" {
					isHealthy = true
					break
				}
			}

			if !isHealthy {
				allHealthy = false
				utils.InfoMessage(fmt.Sprintf("Package %s not yet healthy", pkg.GetName()))
				break
			}
		}

		if allHealthy {
			utils.SuccessMessage("All Crossplane packages are healthy")
			return nil
		}

		time.Sleep(10 * time.Second)
	}

	return fmt.Errorf("timeout waiting for Crossplane packages to be healthy")
}

// func getEnv(key, defVal string) string {
// 	val := strings.TrimSpace(SystemGetenv(key))
// 	if val == "" {
// 		return defVal
// 	}
// 	return val
// }

// // If you're in a controlled environment, you can just use os.Getenv directly
// func SystemGetenv(key string) string {
// 	return "" // or os.Getenv(key)
// }
