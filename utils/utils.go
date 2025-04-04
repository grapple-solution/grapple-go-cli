package utils

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/briandowns/spinner"
	"github.com/manifoldco/promptui"
	"golang.org/x/exp/rand"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/registry"
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
	apiv1 "k8s.io/client-go/kubernetes"
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

	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
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

		InfoMessage(fmt.Sprintf("Deploying grpl-dns-route53-upsert (Attempt %d/%d)", attempt, maxRetries))
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
				SuccessMessage("DNS update verified successfully")
				return true, nil
			case corev1.PodFailed:
				return false, fmt.Errorf("DNS update failed")
			default:
				return false, nil
			}
		})

		if err == nil {
			return nil // Success, exit the function
		}

		if attempt < maxRetries {
			InfoMessage(fmt.Sprintf("DNS update failed, retrying... (Attempt %d/%d)", attempt+1, maxRetries))
		} else {
			ErrorMessage(fmt.Sprintf("DNS update failed after %d attempts", maxRetries))
			return fmt.Errorf("DNS update failed after %d attempts: %w", maxRetries, err)
		}
	}

	return nil // Should never reach here due to error return in last iteration
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

// PreloadGrappleImages downloads and caches Grapple images in the cluster
func PreloadGrappleImages(restConfig *rest.Config, version string) error {
	// Create the clientset
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Define the images to preload
	images := []string{
		fmt.Sprintf("grpl/grapi:%s", version),
		fmt.Sprintf("grpl/gruim:%s", version),
	}

	// Create pods to pull images
	for _, image := range images {
		// Create a unique name for the pod by replacing invalid characters
		podName := fmt.Sprintf("image-preload-%s-%s",
			strings.ReplaceAll(strings.Split(image, ":")[0], "/", "-"),
			strings.ReplaceAll(strings.Split(image, ":")[1], ".", "-"))

		// Check if pod already exists
		_, err := clientset.CoreV1().Pods("default").Get(context.Background(), podName, v1.GetOptions{})
		if err == nil {
			// Pod exists, skip to next image
			// InfoMessage(fmt.Sprintf("Pod %s already exists, skipping image preload for %s", podName, image))
			continue
		} else if !errors.IsNotFound(err) {
			// Error other than "not found"
			return fmt.Errorf("failed to check for existing pod %s: %w", podName, err)
		}

		pod := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name: podName,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "preload",
						Image: image,
						Command: []string{
							"sleep",
							"1", // Sleep briefly just to pull the image
						},
					},
				},
				RestartPolicy: corev1.RestartPolicyNever,
			},
		}

		// Create the pod
		_, err = clientset.CoreV1().Pods("default").Create(context.Background(), pod, v1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create image preload pod for %s: %w", image, err)
		}

		// Wait for pod to complete
		err = wait.PollImmediate(time.Second, time.Minute*5, func() (bool, error) {
			pod, err := clientset.CoreV1().Pods("default").Get(context.Background(), podName, v1.GetOptions{})
			if err != nil {
				return false, err
			}

			switch pod.Status.Phase {
			case corev1.PodSucceeded:
				return true, nil
			case corev1.PodFailed:
				return false, fmt.Errorf("pod failed")
			default:
				return false, nil
			}
		})

		if err != nil {
			return fmt.Errorf("error waiting for image preload pod %s: %w", podName, err)
		}

		// // Clean up the pod
		// err = clientset.CoreV1().Pods("default").Delete(context.Background(), podName, v1.DeleteOptions{})
		// if err != nil {
		// 	return fmt.Errorf("failed to delete image preload pod %s: %w", podName, err)
		// }
	}

	return nil
}

// LogoutHelmRegistry logs out from a Helm registry
func LogoutHelmRegistry(registryClient *registry.Client) error {

	// Perform the logout
	registryURL := "public.ecr.aws"
	if err := registryClient.Logout(registryURL); err != nil {
		return fmt.Errorf("failed to logout from registry %s: %w", registryURL, err)
	}

	return nil
}

func ExtractDomainFromGrplConfig(restClient *rest.Config) (string, error) {
	clientset, err := kubernetes.NewForConfig(restClient)
	if err != nil {
		return "", fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	secret, err := clientset.CoreV1().Secrets("grpl-system").Get(context.TODO(), "grsf-config", v1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get secret: %w", err)
	}

	clusterdomain, exists := secret.Data["clusterdomain"]
	if !exists {
		return "", nil
	}

	return string(clusterdomain), nil
}

func IsSSLEnabled(restClient *rest.Config) (bool, error) {
	clientset, err := kubernetes.NewForConfig(restClient)
	if err != nil {
		return false, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	secret, err := clientset.CoreV1().Secrets("grpl-system").Get(context.TODO(), "grsf-config", v1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to get secret: %w", err)
	}

	sslEnabled, exists := secret.Data["ssl"]
	if !exists {
		return false, nil
	}

	return string(sslEnabled) == "true", nil
}

func GetClusterProviderType(clientset *kubernetes.Clientset) (string, error) {

	// Try to get grsf-config secret
	secret, err := clientset.CoreV1().Secrets("grpl-system").Get(context.TODO(), "grsf-config", v1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get secret: %w", err)
	}

	return string(secret.Data[SecKeyProviderClusterType]), nil
}

// getClusterExternalIP waits for and retrieves the external IP of a LoadBalancer service
func GetClusterExternalIP(restConfig *rest.Config, namespace, serviceName string) (string, error) {
	// Maximum wait time and interval
	maxWait := 300 * time.Second
	interval := 5 * time.Second
	deadline := time.Now().Add(maxWait)

	InfoMessage(fmt.Sprintf("Waiting for the external IP of LoadBalancer '%s' in namespace '%s'", serviceName, namespace))

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
				InfoMessage(fmt.Sprintf("External IP for LoadBalancer '%s': %s", serviceName, externalIP))
				return externalIP, nil
			}
		}

		fmt.Print(".")
		time.Sleep(interval)
	}

	return "", fmt.Errorf("timeout: external IP not assigned for service '%s' in namespace '%s' within %v",
		serviceName, namespace, maxWait)
}
