package utils

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/briandowns/spinner"
	"github.com/manifoldco/promptui"
	"golang.org/x/exp/rand"
	"gopkg.in/yaml.v2"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/registry"
	"helm.sh/helm/v3/pkg/repo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Print success message in green
func SuccessMessage(message string) {
	log.Printf("%s%s%s\n", ColorGreen, message, ColorReset)
}

// Print info message in yellow
func InfoMessage(message string) {
	log.Printf("%s%s%s\n", ColorYellow, message, ColorReset)
}

// Print error message in red
func ErrorMessage(message string) {
	log.Printf("%s%s%s\n", ColorRed, message, ColorReset)
}

// Prompt user for input if not provided via flags
func PromptInput(prompt string, defaultValue string, validationRegex string) (string, error) {
	if validationRegex == "" {
		return "", fmt.Errorf("validation regex is required")
	}
	promptUI := promptui.Prompt{
		Label:   prompt,
		Default: defaultValue,
		Validate: func(input string) error {
			matched, err := regexp.MatchString(validationRegex, input)
			if err != nil {
				return fmt.Errorf("invalid regex pattern: %v", err)
			}
			if !matched {
				return fmt.Errorf("input does not match required pattern")
			}
			return nil
		},
	}
	result, err := promptUI.Run()
	if err != nil {
		return "", err
	}
	return result, nil
}

func PromptSelect(label string, items []string) (string, error) {
	prompt := promptui.Select{
		Label: label,
		Items: items,
	}

	_, result, err := prompt.Run()
	if err != nil {
		return "", err
	}
	return result, nil
}

func PromptConfirm(message string) (bool, error) {
	prompt := promptui.Prompt{
		Label:     message,
		IsConfirm: true,
	}

	result, err := prompt.Run()
	if err != nil {
		if err == promptui.ErrAbort {
			return false, nil
		}
		return false, err
	}

	return strings.ToLower(result) == "y", nil
}

func PromptPassword(prompt string) (string, error) {
	promptUI := promptui.Prompt{
		Label: prompt,
		Mask:  '*',
		Validate: func(input string) error {
			if input == "" {
				return fmt.Errorf("password cannot be empty")
			}
			return nil
		},
	}
	result, err := promptUI.Run()
	if err != nil {
		return "", err
	}
	return result, nil
}

// Define grappleDomain variable
// extractDomain extracts the domain name from a DNS string
func ExtractDomain(dns string) string {
	// Split on dots and take last two parts if they exist
	parts := strings.Split(dns, ".")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], ".")
	}
	return dns
}

// isResolvable checks if a domain name can be resolved
func IsResolvable(domain string) bool {
	_, err := net.LookupHost(domain)
	return err == nil
}

var s *spinner.Spinner

// StartSpinner starts a spinner with the given message
func StartSpinner(message string) {
	s = spinner.New(spinner.CharSets[9], 100*time.Millisecond)
	s.Suffix = " " + message
	s.Start()
}

// StopSpinner stops the current spinner
func StopSpinner() {
	if s != nil {
		s.Stop()
	}
}

func GetLogWriters(logFilePath string) (*os.File, func(), func()) {
	// Open the log file (create if not exists, append mode)
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Fatalf("failed to open log file: %v", err)
	}

	// Create a multi-writer to log both to console (stdout) and file
	multiWriter := io.MultiWriter(os.Stdout, logFile)

	logOnFileStart := func() {
		log.SetOutput(logFile)
	}

	logOnCliAndFileStart := func() {
		log.SetOutput(multiWriter)
	}

	return logFile, logOnFileStart, logOnCliAndFileStart
}

func Contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// installKubeBlocksOnCluster installs the KubeBlocks chart using Helm.
func InstallKubeBlocksOnCluster(
	restConfig *rest.Config,
) error {

	helmCfg, err := GetHelmConfig(restConfig, "kb-system")
	if err != nil {
		return fmt.Errorf("failed to get helm config: %w", err)
	}

	// Check if KubeBlocks release already exists in any namespace
	client := action.NewList(helmCfg)
	client.AllNamespaces = true // Search across all namespaces
	releases, err := client.Run()
	if err != nil {
		return fmt.Errorf("failed to list helm releases: %w", err)
	}

	for _, release := range releases {
		if release.Name == "kubeblocks" {
			if release.Info.Status == "failed" {
				// Delete the failed release
				uninstall := action.NewUninstall(helmCfg)
				_, err := uninstall.Run(release.Name)
				if err != nil {
					return fmt.Errorf("failed to uninstall failed kubeblocks release: %w", err)
				}
				// InfoMessage("Removed failed KubeBlocks release, will attempt fresh install")
			} else {
				// InfoMessage("KubeBlocks release already exists, skipping installation")
				return nil
			}
			break
		}
	}

	// Create kb-system namespace if it doesn't exist
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	_, err = clientset.CoreV1().Namespaces().Get(context.Background(), "kb-system", v1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			ns := &corev1.Namespace{
				ObjectMeta: v1.ObjectMeta{
					Name: "kb-system",
				},
			}
			_, err = clientset.CoreV1().Namespaces().Create(context.Background(), ns, v1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create kb-system namespace: %w", err)
			}
			// InfoMessage("Created kb-system namespace")
		} else {
			return fmt.Errorf("failed to check kb-system namespace: %w", err)
		}
	}

	// 1. Create CRDs first
	// InfoMessage("Installing KubeBlocks CRDs...")

	crdsURL := "https://github.com/apecloud/kubeblocks/releases/download/v0.9.2/kubeblocks_crds.yaml"

	// Use dynamic client to create CRDs
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Fetch and apply CRDs
	resp, err := http.Get(crdsURL)
	if err != nil {
		return fmt.Errorf("failed to download CRDs yaml: %w", err)
	}
	defer resp.Body.Close()

	// Use k8syaml decoder to properly handle Kubernetes YAML
	decoder := k8syaml.NewYAMLOrJSONDecoder(resp.Body, 4096)
	for {
		var obj unstructured.Unstructured
		if err := decoder.Decode(&obj); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to decode CRD yaml: %w", err)
		}

		// Skip empty documents
		if len(obj.Object) == 0 {
			// InfoMessage("Skipping empty document")
			continue
		}

		gvr := schema.GroupVersionResource{
			Group:    "apiextensions.k8s.io",
			Version:  "v1",
			Resource: "customresourcedefinitions",
		}

		_, err = dynamicClient.Resource(gvr).Create(context.Background(), &obj, v1.CreateOptions{})
		if err != nil && !errors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create CRD %s: %w", obj.GetName(), err)
		}

		// InfoMessage(fmt.Sprintf("Created kubeblocks CRDs %s", obj.GetName()))
	}
	// Wait a bit for CRDs to be established
	time.Sleep(10 * time.Second)

	// 2. Create Helm environment settings
	settings := cli.New()
	settings.SetNamespace("kb-system")

	// 3. Add the KubeBlocks Helm repository
	repoEntry := repo.Entry{
		Name: "kubeblocks",
		URL:  "https://apecloud.github.io/helm-charts",
	}

	chartRepo, err := repo.NewChartRepository(&repoEntry, getter.All(settings))
	if err != nil {
		return fmt.Errorf("failed to create chart repository object: %w", err)
	}

	// Add repo to repositories.yaml
	repoFile := settings.RepositoryConfig
	b, err := os.ReadFile(repoFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read repository file: %w", err)
	}

	var f repo.File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("failed to unmarshal repository file: %w", err)
	}

	// Add new repo or update existing
	f.Add(&repoEntry)

	if err := f.WriteFile(repoFile, 0644); err != nil {
		return fmt.Errorf("failed to write repository file: %w", err)
	}

	_, err = chartRepo.DownloadIndexFile()
	if err != nil {
		return fmt.Errorf("failed to download repository index: %w", err)
	}

	// InfoMessage("Added and updated kubeblocks helm repository")

	// 4. Create a Helm install client
	installClient := action.NewInstall(helmCfg)

	installClient.ReleaseName = "kubeblocks"
	installClient.Namespace = "kb-system"
	installClient.CreateNamespace = true
	installClient.Timeout = 1200 * time.Second // 20 minute timeout
	installClient.Wait = true

	// 5. Locate and load the chart
	chartPath, err := installClient.ChartPathOptions.LocateChart("kubeblocks/kubeblocks", settings)
	if err != nil {
		return fmt.Errorf("failed to locate KubeBlocks chart: %w", err)
	}

	chartRequested, err := loader.Load(chartPath)
	if err != nil {
		return fmt.Errorf("failed to load chart at path [%s]: %w", chartPath, err)
	}

	// InfoMessage(fmt.Sprintf("release name: %s", installClient.ReleaseName))
	// InfoMessage(fmt.Sprintf("namespace: %s", installClient.Namespace))

	// Set values to ensure installation in kb-system namespace
	values := map[string]interface{}{}
	if _, err := installClient.Run(chartRequested, values); err != nil {
		return fmt.Errorf("failed to install the KubeBlocks chart: %w", err)
	}

	// SuccessMessage("KubeBlocks installed successfully in namespace kb-system!")
	return nil
}

func GetHelmConfig(restConfig *rest.Config, helmNamespace string) (*action.Configuration, error) {

	// Enable OCI support for Helm
	os.Setenv("HELM_EXPERIMENTAL_OCI", "1")

	// Initialize the OCI registry client
	registryClient, err := registry.NewClient()
	if err != nil {
		return nil, fmt.Errorf("failed to init helm config: %w", err)
	}

	// Build Helm action.Configuration
	helmSettings := cli.New()
	helmSettings.SetNamespace(helmNamespace)

	var helmCfg action.Configuration
	err = helmCfg.Init(
		helmSettings.RESTClientGetter(),
		helmNamespace, // default namespace
		"secret",      // can be configmap
		func(format string, v ...interface{}) {
			log.Printf(format, v...)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to init helm config: %w", err)
	}

	// Set the registry client in the Helm configuration
	helmCfg.RegistryClient = registryClient

	return &helmCfg, nil
}

// GetKubernetesConfig returns restConfig and clientset after validating the connection
func GetKubernetesConfig() (*rest.Config, *kubernetes.Clientset, error) {
	var restConfig *rest.Config
	var err error

	// Check if running inside a cluster
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		// Get in-cluster config
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get in-cluster config: %w", err)
		}
	} else {
		// Get home directory
		home := os.Getenv("HOME")
		if home == "" {
			return nil, nil, fmt.Errorf("HOME environment variable not set")
		}

		// Load kubeconfig
		kubeConfigPath := filepath.Join(home, ".kube", "config")
		if _, err := os.Stat(kubeConfigPath); err != nil {
			return nil, nil, fmt.Errorf("kubeconfig not found at %s", kubeConfigPath)
		}

		// Get REST config from kubeconfig
		restConfig, err = clientcmd.BuildConfigFromFlags("", kubeConfigPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to build REST config: %w", err)
		}
	}

	// Create clientset
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	// Verify connection by listing namespaces
	_, err = clientset.CoreV1().Namespaces().List(context.TODO(), v1.ListOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to cluster: %w", err)
	}

	SuccessMessage("Already Connected to a cluster")

	return restConfig, clientset, nil
}

func CreateExternalDBSecret(client *kubernetes.Clientset, deploymentNamespace string, grasName string) error {
	// Extract credentials from existing secret
	existingSecret, err := client.CoreV1().Secrets("grpl-system").Get(context.TODO(), "grpl-e-d-external-sec", v1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get existing secret: %w", err)
	}
	// Create new secret
	newSecret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      fmt.Sprintf("%s-conn-credential", grasName),
			Namespace: deploymentNamespace,
		},
		Data: map[string][]byte{
			"host":     []byte("aurora-mysql-test.cpfyybdyajmx.eu-central-1.rds.amazonaws.com"),
			"port":     []byte("3306"),
			"username": existingSecret.Data["username"],
			"password": existingSecret.Data["password"],
		},
	}

	_, err = client.CoreV1().Secrets(deploymentNamespace).Create(context.TODO(), newSecret, v1.CreateOptions{})
	if errors.IsAlreadyExists(err) {
		_, err = client.CoreV1().Secrets(deploymentNamespace).Update(context.TODO(), newSecret, v1.UpdateOptions{})
	}
	return err
}

func GetResourcePath(subdir string) (string, error) {
	// Get the directory where the executable is running
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %v", err)
	}

	// Resolve the directory where Homebrew installed the CLI
	installDir := filepath.Dir(filepath.Dir(execPath)) // Move up one level from bin/

	// Construct the path to the requested resource
	resourcePath := filepath.Join(installDir, "share", "grapple-go-cli", subdir)

	// Ensure the directory exists
	if _, err := os.Stat(resourcePath); os.IsNotExist(err) {
		return "", fmt.Errorf("resource path does not exist: %s", resourcePath)
	}

	return resourcePath, nil
}

func SetupCodeVerificationServer(restConfig *rest.Config, code, completeDomain, cloud string) error {

	// Create Kubernetes clientset from rest config
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Create verification-server namespace
	namespace := &corev1.Namespace{
		ObjectMeta: v1.ObjectMeta{
			Name: "verification-server",
		},
	}
	_, err = client.CoreV1().Namespaces().Create(context.TODO(), namespace, v1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create namespace: %w", err)
	}

	// Get deployment yaml path
	deploymentPath, err := GetResourcePath("files")
	if err != nil {
		return fmt.Errorf("failed to get deployment path: %w", err)
	}
	// deploymentPath := "files"
	src := filepath.Join(deploymentPath, "code-verification-server-deployment.yaml")
	// Read deployment yaml
	yamlFile, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read deployment yaml: %w", err)
	}

	// Replace variables in yaml
	yamlStr := string(yamlFile)
	yamlStr = strings.ReplaceAll(yamlStr, "$CLUSTER_ADDRESS", "verification-server."+completeDomain)

	// Parse yaml into k8s objects
	decoder := k8syaml.NewYAMLOrJSONDecoder(strings.NewReader(yamlStr), 100)
	var objects []runtime.Object

	for {
		obj := &unstructured.Unstructured{}
		if err := decoder.Decode(obj); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to decode yaml: %w", err)
		}
		objects = append(objects, obj)
	}

	// Modify ingress for AWS if needed
	if cloud == "aws" {
		for _, obj := range objects {
			if obj.GetObjectKind().GroupVersionKind().Kind == "Ingress" {
				unstructuredObj := obj.(*unstructured.Unstructured)
				if err := unstructured.SetNestedField(unstructuredObj.Object, "traefik", "spec", "ingressClassName"); err != nil {
					return fmt.Errorf("failed to set ingressClassName: %w", err)
				}
			}
		}
	}

	// Apply objects
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	for _, obj := range objects {
		gvk := obj.GetObjectKind().GroupVersionKind()
		apiResource, err := getAPIResource(client.Discovery(), gvk)
		if err != nil {
			return fmt.Errorf("failed to get API resource: %w", err)
		}

		unstructuredObj := obj.(*unstructured.Unstructured)
		_, err = dynamicClient.Resource(*apiResource).Namespace("verification-server").Create(context.TODO(), unstructuredObj, v1.CreateOptions{})
		if err != nil {
			if errors.IsAlreadyExists(err) {
				// If resource exists, try to update it instead
				_, err = dynamicClient.Resource(*apiResource).Namespace("verification-server").Update(context.TODO(), unstructuredObj, v1.UpdateOptions{})
				if err != nil {
					return fmt.Errorf("failed to update resource: %w", err)
				}
			} else {
				return fmt.Errorf("failed to create resource: %w", err)
			}
		}
	}

	// Wait for deployment to be ready
	InfoMessage("Waiting for code verification server deployment to be ready")
	err = wait.PollImmediate(5*time.Second, 300*time.Second, func() (bool, error) {
		deployment, err := client.AppsV1().Deployments("verification-server").Get(context.TODO(), "code-verification-server", v1.GetOptions{})
		if err != nil {
			return false, nil
		}
		return deployment.Status.AvailableReplicas == deployment.Status.Replicas, nil
	})
	if err != nil {
		return fmt.Errorf("timeout waiting for deployment: %w", err)
	}

	// Set CODE env var
	deployment, err := client.AppsV1().Deployments("verification-server").Get(context.TODO(), "code-verification-server", v1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get deployment: %w", err)
	}

	for i := range deployment.Spec.Template.Spec.Containers {
		deployment.Spec.Template.Spec.Containers[i].Env = append(
			deployment.Spec.Template.Spec.Containers[i].Env,
			corev1.EnvVar{Name: "CODE", Value: code},
		)
	}

	_, err = client.AppsV1().Deployments("verification-server").Update(context.TODO(), deployment, v1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update deployment: %w", err)
	}

	InfoMessage("Code verification server is ready")

	return nil
}

func RemoveCodeVerificationServer(restConfig *rest.Config) error {
	InfoMessage("Removing code verification server...")

	// Create Kubernetes clientset from rest config
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Delete the verification-server namespace and all resources in it
	err = client.CoreV1().Namespaces().Delete(context.TODO(), "verification-server", v1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete verification-server namespace: %w", err)
	}

	// Wait for namespace deletion
	InfoMessage("Waiting for verification server namespace to be deleted")
	err = wait.PollImmediate(5*time.Second, 300*time.Second, func() (bool, error) {
		_, err := client.CoreV1().Namespaces().Get(context.TODO(), "verification-server", v1.GetOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("timeout waiting for namespace deletion: %w", err)
	}

	InfoMessage("Code verification server has been removed")
	return nil
}

func UpsertDNSRecord(restConfig *rest.Config, apiURL, completeDomain, code, externalIP, hostedZoneID, recordType string) error {
	// Create Kubernetes clientset from rest config
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Delete existing pod if exists
	err = client.CoreV1().Pods("default").Delete(context.TODO(), "grpl-dns-route53-upsert", v1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete existing pod: %w", err)
	}

	// Create DNS update pod
	pod := &corev1.Pod{
		ObjectMeta: v1.ObjectMeta{
			Name:      "grpl-dns-route53-upsert",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:  "dns-upsert",
					Image: "zaialpha/grpl-route53-upsert:latest",
					Env: []corev1.EnvVar{
						{Name: "HOSTED_ZONE_ID", Value: hostedZoneID},
						{Name: "GRAPPLE_DNS", Value: "*." + completeDomain},
						{Name: "GRPL_TARGET", Value: externalIP},
						{Name: "TYPE", Value: recordType},
						{Name: "CODE", Value: code},
						{Name: "API_URL", Value: apiURL},
					},
				},
			},
		},
	}

	InfoMessage("Deploying grpl-dns-route53-upsert")
	_, err = client.CoreV1().Pods("default").Create(context.TODO(), pod, v1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create DNS update pod: %w", err)
	}

	// Wait for pod completion
	InfoMessage("Waiting for DNS update pod to complete")
	err = wait.PollImmediate(2*time.Second, 90*time.Second, func() (bool, error) {
		pod, err := client.CoreV1().Pods("default").Get(context.TODO(), "grpl-dns-route53-upsert", v1.GetOptions{})
		if err != nil {
			return false, nil
		}

		switch pod.Status.Phase {
		case corev1.PodSucceeded:
			SuccessMessage("DNS update completed successfully")
			return true, nil
		case corev1.PodFailed:
			return false, fmt.Errorf("DNS update failed")
		default:
			return false, nil
		}
	})

	if err != nil {
		return fmt.Errorf("error waiting for DNS update pod: %w", err)
	}

	return nil
}

// Helper function to get APIResource for dynamic client
func getAPIResource(discovery discovery.DiscoveryInterface, gvk schema.GroupVersionKind) (*schema.GroupVersionResource, error) {
	resources, err := discovery.ServerResourcesForGroupVersion(gvk.GroupVersion().String())
	if err != nil {
		return nil, err
	}

	for _, r := range resources.APIResources {
		if r.Kind == gvk.Kind {
			return &schema.GroupVersionResource{
				Group:    gvk.Group,
				Version:  gvk.Version,
				Resource: r.Name,
			}, nil
		}
	}

	return nil, fmt.Errorf("resource not found for GroupVersionKind %v", gvk)
}

// GenerateRandomString generates a random 32 character hex string
func GenerateRandomString() string {
	bytes := make([]byte, 16) // 16 bytes = 32 hex characters
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to less secure but still random method if crypto/rand fails
		for i := range bytes {
			bytes[i] = byte(rand.Intn(256))
		}
	}
	return hex.EncodeToString(bytes)
}
