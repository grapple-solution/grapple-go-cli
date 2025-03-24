package k3d

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grapple-solution/grapple_cli/utils"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
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

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	for {
		deployment, err := clientset.AppsV1().Deployments("kube-system").Get(context.TODO(), "coredns", metav1.GetOptions{})
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
	for attempts := 0; attempts < 50; attempts++ {
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
		if attempts == 49 {
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
	_, err = clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			// Create the namespace
			utils.InfoMessage(fmt.Sprintf("Creating namespace %s...", namespace))
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespace,
				},
			}
			_, err = clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create namespace %s: %v", namespace, err)
			}
			utils.SuccessMessage(fmt.Sprintf("Namespace %s created successfully", namespace))
		} else {
			return fmt.Errorf("error checking for namespace %s: %v", namespace, err)
		}
	}

	// Check if secret already exists
	_, err = clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
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
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
			Type: corev1.SecretTypeTLS,
			Data: map[string][]byte{
				"tls.crt": certData,
				"tls.key": keyData,
			},
		}

		_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
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
	_, err = dynamicClient.Resource(clusterIssuerGVR).Get(ctx, "mkcert-ca-issuer", metav1.GetOptions{})
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

		_, err = dynamicClient.Resource(clusterIssuerGVR).Create(ctx, clusterIssuer, metav1.CreateOptions{})
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to create ClusterIssuer mkcert-ca-issuer: %v", err))
			return fmt.Errorf("failed to create ClusterIssuer: %v", err)
		}

		utils.SuccessMessage("ClusterIssuer mkcert-ca-issuer created successfully!")
	}

	// Update the grsf-config secret with SSL settings
	utils.InfoMessage("Updating grsf-config secret with SSL settings")

	// Check if grpl-system namespace exists
	_, err = clientset.CoreV1().Namespaces().Get(ctx, grplNamespace, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("namespace %s does not exist, cannot update secret", grplNamespace)
		}
		return fmt.Errorf("error checking for namespace %s: %v", grplNamespace, err)
	}

	// Check if grsf-config secret exists
	grsfSecret, err := clientset.CoreV1().Secrets(grplNamespace).Get(ctx, grplSecretName, metav1.GetOptions{})
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
	_, err = clientset.CoreV1().Secrets(grplNamespace).Update(ctx, grsfSecret, metav1.UpdateOptions{})
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
