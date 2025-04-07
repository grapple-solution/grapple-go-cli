package example

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5" // Go-git package
	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	grasTemplate string
	dbType       string
	kubeContext  string
	wait         bool
)

// DeployCmd represents the deploy command
var DeployCmd = &cobra.Command{
	Use:     "deploy",
	Aliases: []string{"d"},
	Short:   "Deploy Grapple example resources",
	Long: `Deploy Grapple example resources from templates.
Available templates:
- db-file
- db-cache-redis  
- db-mysql-model-based
- db-mysql-discovery-based

For database resources, you can choose between internal or external databases.`,
	RunE: runDeploy,
}

func init() {
	DeployCmd.Flags().StringVar(&grasTemplate, "gras-template", "", "Grapple Application Set template to use")
	DeployCmd.Flags().StringVar(&dbType, "db-type", "", "Database type (internal/external)")
	DeployCmd.Flags().StringVar(&kubeContext, "kube-context", "", "Kubernetes context")
	DeployCmd.Flags().BoolVar(&wait, "wait", false, "Wait for deployment to be ready")
}

func runDeploy(cmd *cobra.Command, args []string) error {
	// Setup logging
	logFile, logOnFileStart, logOnCliAndFileStart := utils.GetLogWriters("/tmp/grpl_example_deploy.log")
	defer logFile.Close()

	logOnCliAndFileStart()

	// Check cluster accessibility
	restConfig, err := clientcmd.BuildConfigFromFlags("", filepath.Join(os.Getenv("HOME"), ".kube", "config"))
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	utils.InfoMessage("Waiting for Grapple to be ready...")
	logOnFileStart()
	err = utils.WaitForGrappleReady(restConfig)
	logOnCliAndFileStart()
	if err != nil {
		return fmt.Errorf("failed to wait for grapple to be ready: %w", err)
	}
	utils.SuccessMessage("Grapple is ready!")

	// Clone examples repo
	repoPath := "/tmp/grpl-gras-examples"
	if err := cloneExamplesRepo(repoPath); err != nil {
		return err
	}

	if grasTemplate != "" {
		if err := utils.ValidateGrasTemplates(grasTemplate); err != nil {
			return err
		}
	}

	// If template not specified, prompt user to select one
	if grasTemplate == "" {
		result, err := utils.PromptSelect("Select template type", utils.GrasTemplates)
		if err != nil {
			return fmt.Errorf("template selection failed: %w", err)
		}
		grasTemplate = result
	}

	switch grasTemplate {
	case utils.DB_MYSQL_MODEL_BASED, utils.DB_MYSQL_DISCOVERY_BASED:
		if dbType == "" {
			result, err := utils.PromptSelect("Select database type", utils.GrasDBType)
			if err != nil {
				return fmt.Errorf("database type selection failed: %w", err)
			}
			dbType = result
		}
	}

	// Handle different template types
	switch grasTemplate {
	case utils.DB_FILE:
		return deployDBFile(clientset, restConfig, repoPath)
	case utils.DB_CACHE_REDIS:
		return deployDBCacheRedis(clientset, restConfig, repoPath, logOnCliAndFileStart, logOnFileStart)
	case utils.DB_MYSQL_MODEL_BASED:
		return deployDBMySQL(clientset, restConfig, repoPath, "model", dbType, logOnCliAndFileStart, logOnFileStart)
	case utils.DB_MYSQL_DISCOVERY_BASED:
		return deployDBMySQL(clientset, restConfig, repoPath, "discovery", dbType, logOnCliAndFileStart, logOnFileStart)
	default:
		return fmt.Errorf("invalid template type: %s", grasTemplate)
	}
}

func cloneExamplesRepo(path string) error {
	// Remove existing repo directory if it exists
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("failed to clean existing repo: %w", err)
	}

	utils.InfoMessage("Cloning examples repository...")

	// Clone the repository using go-git
	_, err := git.PlainClone(path, false, &git.CloneOptions{
		URL:      "https://github.com/grapple-solution/grpl-gras-examples.git",
		Progress: os.Stdout,
	})
	if err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}

	return nil
}

func deployDBFile(client *kubernetes.Clientset, restConfig *rest.Config, repoPath string) error {
	manifestPath := filepath.Join(repoPath, "db-file/resource.yaml")
	return applyManifest(client, restConfig, manifestPath)
}

func deployDBCacheRedis(client *kubernetes.Clientset, restConfig *rest.Config, repoPath string, logOnCliAndFileStart, logOnFileStart func()) error {
	manifestPath := filepath.Join(repoPath, "db-cache-redis/resource.yaml")
	// check and install kubeblocks first
	utils.InfoMessage("Checking and installing kubeblocks, it may take a while...")
	logOnFileStart()
	if err := utils.InstallKubeBlocksOnCluster(restConfig); err != nil {
		logOnCliAndFileStart()
		return err
	}
	logOnCliAndFileStart()
	utils.SuccessMessage("Checked kubeblocks installation")
	return applyManifest(client, restConfig, manifestPath)
}

func deployDBMySQL(client *kubernetes.Clientset, restConfig *rest.Config, repoPath string, dbStyle string, dbType string, logOnCliAndFileStart, logOnFileStart func()) error {
	var manifestPath string
	if dbType == utils.DB_INTERNAL {
		manifestPath = filepath.Join(repoPath, fmt.Sprintf("db-mysql-%s-based/internal_resource.yaml", dbStyle))
		utils.InfoMessage("Checking and installing kubeblocks, it may take a while...")
		logOnFileStart()
		if err := utils.InstallKubeBlocksOnCluster(restConfig); err != nil {
			logOnCliAndFileStart()
			return err
		}
		logOnCliAndFileStart()
		utils.SuccessMessage("Checked kubeblocks installation")
		return applyManifest(client, restConfig, manifestPath)

	} else if dbType == utils.DB_EXTERNAL {
		manifestPath = filepath.Join(repoPath, fmt.Sprintf("db-mysql-%s-based/external_resource.yaml", dbStyle))
		if err := applyManifest(client, restConfig, manifestPath); err != nil {
			return err
		}

		utils.InfoMessage("Creating external db secret...")
		logOnFileStart()
		if err := utils.CreateExternalDBSecret(client, DeploymentNamespace, GrasName); err != nil {
			logOnCliAndFileStart()
			return err
		}
		logOnCliAndFileStart()
		utils.SuccessMessage("Created external db secret")
	}

	return nil
}

func applyManifest(client *kubernetes.Clientset, restConfig *rest.Config, manifestPath string) error {
	// Read the manifest file
	yamlFile, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read manifest file: %w", err)
	}

	// Create dynamic client for applying manifests
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Split the YAML into individual documents
	decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(yamlFile), 4096)
	for {
		var obj unstructured.Unstructured
		if err := decoder.Decode(&obj); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to decode manifest: %w", err)
		}

		// Skip empty documents
		if len(obj.Object) == 0 {
			utils.InfoMessage("Skipping empty document")
			continue
		}

		// Get namespace from manifest and create if needed
		namespace := obj.GetNamespace()
		if namespace != "" {
			_, err := client.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					utils.InfoMessage(fmt.Sprintf("Creating namespace '%s'", namespace))
					ns := &corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name: namespace,
						},
					}
					_, err = client.CoreV1().Namespaces().Create(context.TODO(), ns, metav1.CreateOptions{})
					if err != nil {
						return fmt.Errorf("failed to create namespace: %w", err)
					}
				} else {
					return fmt.Errorf("failed to check namespace: %w", err)
				}
			}
		}
		DeploymentNamespace = namespace
		GrasName = obj.GetName()

		// Get GVR for the resource
		gvr := schema.GroupVersionResource{
			Group:    obj.GetObjectKind().GroupVersionKind().Group,
			Version:  obj.GetObjectKind().GroupVersionKind().Version,
			Resource: strings.ToLower(obj.GetKind()) + "s",
		}

		// Apply the resource
		utils.InfoMessage(fmt.Sprintf("Applying %s '%s' in namespace '%s'",
			obj.GetKind(),
			obj.GetName(),
			namespace))

		var dr dynamic.ResourceInterface
		if namespace != "" {
			dr = dynamicClient.Resource(gvr).Namespace(namespace)
		} else {
			dr = dynamicClient.Resource(gvr)
		}

		// Try to get existing resource first
		existing, err := dr.Get(context.TODO(), obj.GetName(), metav1.GetOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to check existing resource: %w", err)
		}

		if errors.IsNotFound(err) {
			// Resource doesn't exist, create it
			_, err = dr.Create(context.TODO(), &obj, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to deploy GrappleApplicationSet resource: %w", err)
			}
			utils.SuccessMessage(fmt.Sprintf("Created %s '%s' in namespace '%s'", obj.GetKind(), obj.GetName(), namespace))
		} else {
			// Resource exists, update it
			// Set the resourceVersion to ensure we're updating the latest version
			obj.SetResourceVersion(existing.GetResourceVersion())
			_, err = dr.Update(context.TODO(), &obj, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("failed to update resource: %w", err)
			}
			utils.SuccessMessage(fmt.Sprintf("Updated %s '%s' in namespace '%s'", obj.GetKind(), obj.GetName(), namespace))
		}

		// Wait a bit for the resource to be processed
		time.Sleep(2 * time.Second)

		// Verify the resource exists
		_, err = dr.Get(context.TODO(), obj.GetName(), metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to verify resource creation: %w", err)
		}
	}

	// Check if wait flag is set to true
	if wait {
		utils.InfoMessage("Waiting for grapi deployment to be ready...")
		deploymentName := fmt.Sprintf("%s-%s-grapi", DeploymentNamespace, GrasName)
		utils.WaitForExampleDeployment(client, DeploymentNamespace, deploymentName)
		utils.SuccessMessage("grapi deployment is ready")

		utils.InfoMessage("Waiting for gruim deployment to be ready...")
		deploymentName = fmt.Sprintf("%s-%s-gruim", DeploymentNamespace, GrasName)
		utils.WaitForExampleDeployment(client, DeploymentNamespace, deploymentName)
		utils.SuccessMessage("gruim deployment is ready")
	}

	// Get cluster domain from environment or use default
	clusterDomain, err := utils.ExtractDomainFromGrplConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to extract cluster domain: %w", err)
	}

	sslEnabled, err := utils.IsSSLEnabled(restConfig)
	if err != nil {
		return fmt.Errorf("failed to check SSL status: %w", err)
	}

	// Display deployment details
	displayDeploymentDetails(DeploymentNamespace, GrasName, clusterDomain, sslEnabled)

	return nil
}
func waitForExampleDeployment(client *kubernetes.Clientset, namespace, deploymentName string) error {
	// Watch deployment status
	watcher, err := client.AppsV1().Deployments(namespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", deploymentName),
	})
	if err != nil {
		return fmt.Errorf("failed to watch deployment: %w", err)
	}
	defer watcher.Stop()

	// Wait for deployment to be ready
	for event := range watcher.ResultChan() {
		deployment, ok := event.Object.(*appsv1.Deployment)
		if !ok {
			continue
		}

		// Check if deployment is ready
		// Ensure all replicas are ready, updated, and available
		if deployment.Status.ReadyReplicas == deployment.Status.Replicas &&
			deployment.Status.UpdatedReplicas == deployment.Status.Replicas &&
			deployment.Status.AvailableReplicas == deployment.Status.Replicas {

			// Check if all pods are ready by verifying conditions
			allPodsReady := true

			// Get all pods for this deployment
			selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
			if err != nil {
				return fmt.Errorf("failed to parse selector: %w", err)
			}

			pods, err := client.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
				LabelSelector: selector.String(),
			})
			if err != nil {
				return fmt.Errorf("failed to list pods: %w", err)
			}

			// Check each pod to ensure all containers are ready
			for _, pod := range pods.Items {
				if pod.Status.Phase != corev1.PodRunning {
					allPodsReady = false
					break
				}

				// Check if all containers in the pod are ready
				for _, containerStatus := range pod.Status.ContainerStatuses {
					if !containerStatus.Ready {
						allPodsReady = false
						break
					}
				}

				if !allPodsReady {
					break
				}
			}

			if allPodsReady {
				utils.SuccessMessage("Deployment is ready")
				break
			}
		}
	}
	return nil
}

func displayDeploymentDetails(namespace, resourceName, clusterDomain string, sslEnabled bool) {

	if !wait {
		utils.InfoMessage("It will take a few minutes for the deployment to be ready")
	}

	httpPrefix := "http"
	if sslEnabled {
		httpPrefix = "https"
	}

	if clusterDomain != "" {
		utils.InfoMessage("Deployment Details")
		utils.InfoMessage(fmt.Sprintf("Following resources are deployed in %s namespace", namespace))
		utils.InfoMessage(fmt.Sprintf("Resource Name: grapi can be accessed at %s://%s-%s-grapi.%s",
			httpPrefix, namespace, resourceName, clusterDomain))
		utils.InfoMessage(fmt.Sprintf("Resource Name: gruim can be accessed at %s://%s-%s-gruim.%s",
			httpPrefix, namespace, resourceName, clusterDomain))
	}
}
