package utils

import (
	"context"
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
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ANSI color codes
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
)

// Regex constants
const (
	NonEmptyValueRegex = "^.+$"
	EmptyValueRegex    = ".*"
	EmailRegex         = "^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}$"
)

// Default values
const (
	DefaultValue = ""
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
				InfoMessage("Removed failed KubeBlocks release, will attempt fresh install")
			} else {
				InfoMessage("KubeBlocks release already exists, skipping installation")
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
			InfoMessage("Created kb-system namespace")
		} else {
			return fmt.Errorf("failed to check kb-system namespace: %w", err)
		}
	}

	// 1. Create CRDs first
	InfoMessage("Installing KubeBlocks CRDs...")

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
			InfoMessage("Skipping empty document")
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

		InfoMessage(fmt.Sprintf("Created kubeblocks CRDs %s", obj.GetName()))
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

	InfoMessage("Added and updated kubeblocks helm repository")

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

	InfoMessage(fmt.Sprintf("release name: %s", installClient.ReleaseName))
	InfoMessage(fmt.Sprintf("namespace: %s", installClient.Namespace))

	// Set values to ensure installation in kb-system namespace
	values := map[string]interface{}{}
	if _, err := installClient.Run(chartRequested, values); err != nil {
		return fmt.Errorf("failed to install the KubeBlocks chart: %w", err)
	}

	SuccessMessage("KubeBlocks installed successfully in namespace kb-system!")
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
