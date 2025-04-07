package k3d

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/grapple-solution/grapple_cli/utils" // your logging/prompting
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	// Helm libraries
	// Kubernetes libraries
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
	InstallCmd.Flags().StringVar(&email, "email", "", "Email address")
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
			utils.ErrorMessage("No k3d clusters found, run 'grapple k3d create' to create a cluster")
			return fmt.Errorf("no k3d clusters found, run 'grapple k3d create' to create a cluster")
		}

		var clusterNames []string
		for _, cluster := range clusters {
			clusterNames = append(clusterNames, cluster.Name)
		}

		result, err := utils.PromptSelect("Select cluster to remove", clusterNames)
		if err != nil {
			utils.ErrorMessage("Cluster selection is required")
			return fmt.Errorf("cluster selection is required")
		}
		clusterName = result
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

	err = waitForK3dClusterToBeReady(restConfig)
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to wait for cluster to be ready: %v", err))
		return fmt.Errorf("failed to wait for cluster to be ready: %v", err)
	}

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
		if err := utils.InstallKubeBlocksOnCluster(restConfig); err != nil {
			utils.ErrorMessage("kubeblocks installation error: " + err.Error())
		} else {
			utils.InfoMessage("kubeblocks installed.")
		}
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
	var preloadImagesError error
	go func() {
		defer preloadImagesWg.Done()
		if err := utils.PreloadGrappleImages(restConfig, grappleVersion); err != nil {
			utils.ErrorMessage("image preload error: " + err.Error())
			preloadImagesError = err
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

	utils.InfoMessage("Waiting for grapple images to be preloaded...")
	preloadImagesWg.Wait()
	if preloadImagesError != nil {
		utils.ErrorMessage("image preload error: " + preloadImagesError.Error())
	} else {
		utils.SuccessMessage("Grapple images preloaded.")
	}

	err = setupClusterIssuer(context.TODO(), restConfig)
	if err != nil {
		return fmt.Errorf("failed to setup cluster issuer: %w", err)
	}

	utils.SuccessMessage("Grapple installation completed!")
	return nil
}

// waitForK3dClusterToBeReady waits for the coredns deployment to be ready in the k3d cluster
func waitForK3dClusterToBeReady(restConfig *rest.Config) error {
	utils.InfoMessage("Waiting for the coredns deployment to be ready...")

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	for {
		deployment, err := clientset.AppsV1().Deployments("kube-system").Get(context.TODO(), "coredns", v1.GetOptions{})
		if err != nil {
			fmt.Print(".")
			time.Sleep(5 * time.Second)
			continue
		}

		if deployment.Status.ReadyReplicas == *deployment.Spec.Replicas &&
			deployment.Status.UpdatedReplicas == *deployment.Spec.Replicas &&
			deployment.Status.AvailableReplicas == *deployment.Spec.Replicas {
			utils.SuccessMessage("coredns deployment is ready")
			return nil
		}

		fmt.Print(".")
		time.Sleep(5 * time.Second)
	}
}

// initClientsAndConfig builds a K8s client-go client
func initClientsAndConfig() (kubernetes.Interface, *rest.Config, error) {
	var k8sClient *kubernetes.Clientset
	// var restConfig *rest.Config
	var err error

	// Try to use the current context from kubeconfig
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = clientcmd.RecommendedHomeFile
	}

	// Build the config from the kubeconfig file
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		// Try in-cluster config as fallback
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create kubernetes config: %w", err)
		}
	}

	// Create the clientset
	k8sClient, err = kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Verify connection by getting server version
	_, err = k8sClient.Discovery().ServerVersion()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to kubernetes: %w", err)
	}

	utils.InfoMessage("Successfully connected to Kubernetes cluster")
	return k8sClient, config, nil
}

func prepareValuesFile() error {
	// Create values map
	values := map[string]interface{}{
		"clusterdomain": completeDomain,
		"config": map[string]interface{}{
			// Common fields
			utils.SecKeyEmail:               "test@gmail.com",
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
			utils.SecKeyProviderClusterType: utils.ProviderClusterTypeK3d,
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
		utils.InfoMessage("Going to deploy grpl on K3d with following configurations")

		utils.InfoMessage(fmt.Sprintf("cluster-name: %s", clusterName))
		utils.InfoMessage(fmt.Sprintf("cluster-ip: %s", clusterIP))
		utils.InfoMessage(fmt.Sprintf("grapple-version: %s", grappleVersion))
		utils.InfoMessage(fmt.Sprintf("grapple-dns: %s", completeDomain))
		utils.InfoMessage(fmt.Sprintf("grapple-license: %s", grappleLicense))
		utils.InfoMessage(fmt.Sprintf("organization: %s", organization))

		if confirmed, err := utils.PromptConfirm("Proceed with deployment using the values above?"); err != nil || !confirmed {
			return fmt.Errorf("failed to install grpl: user cancelled")
		}
	}

	return nil
}

// setupClusterIssuer creates and loads CA certificates into a Kubernetes secret
// and creates a ClusterIssuer for SSL certificates
func setupClusterIssuer(ctx context.Context, restConfig *rest.Config) error {
	// Define file paths and directories
	crt := "rootCA.pem"
	key := "rootCA-key.pem"
	macDir := filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "mkcert")
	linuxDir := filepath.Join(os.Getenv("HOME"), ".local", "share", "mkcert")
	namespace := "grpl-system"
	secretName := "mkcert-ca-secret"
	grplNamespace := "grpl-system"
	grplSecretName := "grsf-config"

	// Create clientset from restConfig
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes clientset: %v", err)
	}

	// Check if grpl-system namespace exists, create if not
	_, err = clientset.CoreV1().Namespaces().Get(ctx, namespace, v1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			// Create the namespace
			utils.InfoMessage(fmt.Sprintf("Creating namespace %s...", namespace))
			ns := &corev1.Namespace{
				ObjectMeta: v1.ObjectMeta{
					Name: namespace,
				},
			}
			_, err = clientset.CoreV1().Namespaces().Create(ctx, ns, v1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create namespace %s: %v", namespace, err)
			}
			utils.SuccessMessage(fmt.Sprintf("Namespace %s created successfully", namespace))
		} else {
			return fmt.Errorf("error checking for namespace %s: %v", namespace, err)
		}
	}

	// Check if secret already exists
	_, err = clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, v1.GetOptions{})
	if err == nil {
		utils.SuccessMessage(fmt.Sprintf("%s already exists", secretName))
		// Secret exists, continue to check ClusterIssuer
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("error checking for secret: %v", err)
	} else {
		// Secret doesn't exist, create it
		// Find CA files
		var caPath string
		if fileExists(filepath.Join(macDir, crt)) && fileExists(filepath.Join(macDir, key)) {
			utils.InfoMessage(fmt.Sprintf("Files found in %s", macDir))
			caPath = macDir
		} else if fileExists(filepath.Join(linuxDir, crt)) && fileExists(filepath.Join(linuxDir, key)) {
			utils.InfoMessage(fmt.Sprintf("Files found in %s", linuxDir))
			caPath = linuxDir
		} else {
			if err := askAndCreateMkcert(); err != nil {
				return fmt.Errorf("failed to create mkcert CA secret: %w", err)
			}
			if fileExists(filepath.Join(macDir, crt)) && fileExists(filepath.Join(macDir, key)) {
				utils.InfoMessage(fmt.Sprintf("Files found in %s", macDir))
				caPath = macDir
			} else if fileExists(filepath.Join(linuxDir, crt)) && fileExists(filepath.Join(linuxDir, key)) {
				utils.InfoMessage(fmt.Sprintf("Files found in %s", linuxDir))
				caPath = linuxDir
			}
		}

		// Read certificate and key files
		certData, err := os.ReadFile(filepath.Join(caPath, crt))
		if err != nil {
			return fmt.Errorf("error reading certificate file: %v", err)
		}

		keyData, err := os.ReadFile(filepath.Join(caPath, key))
		if err != nil {
			return fmt.Errorf("error reading key file: %v", err)
		}

		// Create the Kubernetes secret
		utils.InfoMessage(fmt.Sprintf("Creating Kubernetes secret in namespace %s...", namespace))
		secret := &corev1.Secret{
			ObjectMeta: v1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
			Type: corev1.SecretTypeTLS,
			Data: map[string][]byte{
				"tls.crt": certData,
				"tls.key": keyData,
			},
		}

		_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, v1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create secret %s in namespace %s: %v", secretName, namespace, err)
		}
		utils.SuccessMessage(fmt.Sprintf("Secret %s successfully created in namespace %s", secretName, namespace))
	}

	// Create dynamic client for ClusterIssuer
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %v", err)
	}

	clusterIssuerGVR := schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1",
		Resource: "clusterissuers",
	}

	// Check if ClusterIssuer already exists
	_, err = dynamicClient.Resource(clusterIssuerGVR).Get(ctx, "mkcert-ca-issuer", v1.GetOptions{})
	if err == nil {
		utils.SuccessMessage("ClusterIssuer mkcert-ca-issuer already exists")
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("error checking for ClusterIssuer: %v", err)
	} else {
		// Create ClusterIssuer if it doesn't exist
		clusterIssuer := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "cert-manager.io/v1",
				"kind":       "ClusterIssuer",
				"metadata": map[string]interface{}{
					"name": "mkcert-ca-issuer",
				},
				"spec": map[string]interface{}{
					"ca": map[string]interface{}{
						"secretName": secretName,
					},
				},
			},
		}

		_, err = dynamicClient.Resource(clusterIssuerGVR).Create(ctx, clusterIssuer, v1.CreateOptions{})
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to create ClusterIssuer mkcert-ca-issuer: %v", err))
			return fmt.Errorf("failed to create ClusterIssuer: %v", err)
		}

		utils.SuccessMessage("ClusterIssuer mkcert-ca-issuer created successfully!")
	}

	// Update the grsf-config secret with SSL settings
	utils.InfoMessage("Updating grsf-config secret with SSL settings")

	// Check if grpl-system namespace exists
	_, err = clientset.CoreV1().Namespaces().Get(ctx, grplNamespace, v1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("namespace %s does not exist, cannot update secret", grplNamespace)
		}
		return fmt.Errorf("error checking for namespace %s: %v", grplNamespace, err)
	}

	// Check if grsf-config secret exists
	grsfSecret, err := clientset.CoreV1().Secrets(grplNamespace).Get(ctx, grplSecretName, v1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("secret %s in namespace %s does not exist, cannot update", grplSecretName, grplNamespace)
		}
		return fmt.Errorf("error checking for secret %s: %v", grplSecretName, err)
	}

	// Create a copy of the secret data
	if grsfSecret.Data == nil {
		grsfSecret.Data = make(map[string][]byte)
	}

	// Update the SSL settings
	grsfSecret.Data["ssl"] = []byte("true")
	grsfSecret.Data["sslissuer"] = []byte("mkcert-ca-issuer")

	// Update the secret
	_, err = clientset.CoreV1().Secrets(grplNamespace).Update(ctx, grsfSecret, v1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update secret %s in namespace %s: %v", grplSecretName, grplNamespace, err)
	}

	utils.SuccessMessage(fmt.Sprintf("Successfully updated secret '%s' with ssl=true and sslissuer=mkcert-ca-issuer", grplSecretName))

	return nil
}

func askAndCreateMkcert() error {
	utils.InfoMessage("Mkcert secrets not found. Need to install mkcert (if not present) and create new secrets for ClusterIssuer setup.")

	if !autoConfirm {
		confirmMsg := "Do you want to proceed with mkcert installation and setup? (y/N): "
		confirmed, err := utils.PromptInput(confirmMsg, "n", "^[yYnN]$")
		if err != nil {
			return err
		}
		if strings.ToLower(confirmed) != "y" {
			return fmt.Errorf("failed to setup cluster issuer: user cancelled")
		}
	}

	// Install mkcert if not already installed
	if err := utils.InstallMkcert(); err != nil {
		return fmt.Errorf("failed to install mkcert: %w", err)
	}

	// Generate root CA and key using mkcert
	utils.InfoMessage("Generating mkcert root CA and key...")
	cmd := exec.Command("mkcert", "-install")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to generate mkcert root CA: %w", err)
	}
	utils.SuccessMessage("Generated mkcert root CA and key successfully")

	return nil
}
