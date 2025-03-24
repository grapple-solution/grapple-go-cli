package k3d

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/grapple-solution/grapple_cli/utils"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	apiv1 "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	clusterName  string
	nodes        int
	waitForReady bool
)

// waitForK3dClusterToBeReady waits for the coredns deployment to be ready in the k3d cluster
func waitForK3dClusterToBeReady(restConfig *rest.Config) error {
	utils.InfoMessage("Waiting for the coredns deployment to be ready...")

	clientset, err := apiv1.NewForConfig(restConfig)
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
func initClientsAndConfig() (apiv1.Interface, *rest.Config, error) {
	var k8sClient *apiv1.Clientset
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
	k8sClient, err = apiv1.NewForConfig(config)
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
			secKeyEmail:               "info@grapple-solution.com",
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
			secKeyProviderClusterType: providerClusterTypeK3d,
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
	clientset, err := apiv1.NewForConfig(restConfig)
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
			return fmt.Errorf("error: CA files not found in both directories. Aborting process of creating cluster-issuer")
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

// fileExists checks if a file exists and is not a directory
func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
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
