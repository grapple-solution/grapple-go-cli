package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/cli/values"
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
	apiv1 "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// helmDeployReleaseWithRetry tries to install/upgrade a Helm chart up to 3 times
func HelmDeployGrplReleasesWithRetry(kubeClient apiv1.Interface, releaseName, namespace, version string, valuesFiles []string) error {
	const maxRetries = 3
	var err error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		err = helmInstallOrUpgradeGrpl(kubeClient, releaseName, namespace, version, valuesFiles)
		if err == nil {
			return nil
		}
		InfoMessage(fmt.Sprintf("Attempt %d/%d for %s failed: %v", attempt, maxRetries, releaseName, err))

		// The Bash script logs out of ECR registry if it fails.
		// There's no direct "helm registry logout" equivalent in the Helm Go SDK.
		// This is just a placeholder if you have custom logic to re-auth with the registry.
		if attempt < maxRetries {
			InfoMessage("Retrying after re-auth (placeholder).")
			// e.g. re-auth to registry here
		}
	}
	return fmt.Errorf("helm deploy of %s failed after %d attempts: %w", releaseName, maxRetries, err)
}

func helmInstallOrUpgradeGrpl(kubeClient apiv1.Interface, releaseName, namespace, chartVersion string, valuesFiles []string) error {

	StartSpinner(fmt.Sprintf("Installing/upgrading release %s...", releaseName))
	defer StopSpinner()

	// check and create namespace if it doesn't exist
	CheckAndCreateNamespace(kubeClient, namespace)

	// Mirrors the Bash variables
	awsRegistry := "p7h7z5g3"

	// Construct the OCI chart reference without version in URL
	// Example: "oci://public.ecr.aws/p7h7z5g3/grsf-init"
	chartRef := fmt.Sprintf("oci://public.ecr.aws/%s/%s", awsRegistry, releaseName)

	InfoMessage(fmt.Sprintf("chartRef: %s", chartRef))

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
	LogoutHelmRegistry(regClient)
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

		InfoMessage(fmt.Sprintf("Chartpath %v", chartPath))

		// Load the chart from the local path
		chartLoaded, err := loader.Load(chartPath)
		if err != nil {
			return fmt.Errorf("failed to load chart: %v", err)
		}

		// Merge values from the file (like '/tmp/values-override.yaml')
		valueOpts := &values.Options{
			ValueFiles: valuesFiles,
		}
		vals, err := valueOpts.MergeValues(getter.All(settings))
		if err != nil {
			return fmt.Errorf("failed to merge values from %q: %v", valuesFiles, err)
		}

		InfoMessage("Values from file:")
		for key, value := range vals {
			switch v := value.(type) {
			case map[string]interface{}:
				InfoMessage(fmt.Sprintf("%s:", key))
				for subKey, subValue := range v {
					InfoMessage(fmt.Sprintf("  %s: %v", subKey, subValue))
				}
			default:
				InfoMessage(fmt.Sprintf("%s: %v", key, value))
			}
		}

		// Run the install
		rel, err := installClient.Run(chartLoaded, vals)
		if err != nil {
			return fmt.Errorf("failed to install chart %q: %v", chartRef, err)
		}

		SuccessMessage(fmt.Sprintf("\nSuccessfully installed release %q in namespace %q, chart version: %s",
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
			ValueFiles: valuesFiles,
		}
		vals, err := valueOpts.MergeValues(getter.All(settings))
		if err != nil {
			return fmt.Errorf("failed to merge values from %q: %v", valuesFiles, err)
		}

		InfoMessage("Values from file:")
		for key, value := range vals {
			switch v := value.(type) {
			case map[string]interface{}:
				InfoMessage(fmt.Sprintf("%s:", key))
				for subKey, subValue := range v {
					InfoMessage(fmt.Sprintf("  %s: %v", subKey, subValue))
				}
			default:
				InfoMessage(fmt.Sprintf("%s: %v", key, value))
			}
		}
		// Run the upgrade
		rel, err := upgradeClient.Run(releaseName, chartLoaded, vals)
		if err != nil {
			return fmt.Errorf("failed to upgrade chart %q: %v", chartRef, err)
		}

		SuccessMessage(fmt.Sprintf("\nSuccessfully upgraded release %q in namespace %q, chart version: %s",
			rel.Name, rel.Namespace, rel.Chart.Metadata.Version))

	}
	return nil
}

func CheckAndCreateNamespace(kubeClient apiv1.Interface, namespace string) error {
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
func WaitForGrsfInit(kubeClient apiv1.Interface) error {

	// STEP 1: Check if traefik is installed in kube-system namespace
	_, err := kubeClient.AppsV1().Deployments("kube-system").Get(context.TODO(), "traefik", v1.GetOptions{})
	if err == nil {
		// Wait for Middleware CRD if traefik exists
		InfoMessage("Waiting for Middleware CRD...")
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
						SuccessMessage("Middleware CRD is available")
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

			InfoMessage("Waiting for Middleware CRD...")
			time.Sleep(time.Second)
		}
	}

	// STEP 2: Check if cert-manager is installed in grpl-system namespace
	for attempts := 0; attempts < 30; attempts++ {
		deployment, err := kubeClient.AppsV1().Deployments("grpl-system").Get(context.TODO(), "grsf-init-cert-manager", v1.GetOptions{})
		if err != nil {
			InfoMessage("Waiting for cert-manager deployment...")
			time.Sleep(10 * time.Second)
			continue
		}

		if deployment.Status.AvailableReplicas == *deployment.Spec.Replicas {
			SuccessMessage("Cert-manager deployment is available")
			break
		}

		InfoMessage("Waiting for cert-manager replicas to be ready...")
		time.Sleep(10 * time.Second)
	}

	// Wait for ClusterIssuer CRD
	discoveryClient := kubeClient.Discovery()
	for attempts := 0; attempts < 30; attempts++ {
		_, resources, err := discoveryClient.ServerGroupsAndResources()
		if err != nil {
			InfoMessage("Waiting for ClusterIssuer CRD...")
			time.Sleep(10 * time.Second)
			continue
		}

		crdFound := false
		for _, list := range resources {
			for _, r := range list.APIResources {
				if r.Kind == "ClusterIssuer" {
					crdFound = true
					SuccessMessage("ClusterIssuer CRD is available")
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

		InfoMessage("Waiting for ClusterIssuer CRD...")
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
				InfoMessage("Waiting for Provider CRD...")
				time.Sleep(10 * time.Second)
				continue
			}

			crdFound := false
			for _, list := range resources {
				for _, r := range list.APIResources {
					if r.Kind == "Provider" {
						crdFound = true
						SuccessMessage("Provider CRD is available")
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

			InfoMessage("Waiting for Provider CRD...")
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
				InfoMessage("Waiting for external-secrets webhook deployment...")
				time.Sleep(10 * time.Second)
				continue
			}

			if deployment.Status.AvailableReplicas == *deployment.Spec.Replicas {
				SuccessMessage("External-secrets webhook deployment is available")
				break
			}

			InfoMessage("Waiting for external-secrets webhook replicas to be ready...")
			time.Sleep(10 * time.Second)
		}
	}

	return nil
}

func WaitForGrsf(kubeClient apiv1.Interface, ns string) error {
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
				InfoMessage("Provider-civo is healthy")
				break
			}

			time.Sleep(10 * time.Second)
		}

		// Wait for provider-civo CRD
		InfoMessage("Waiting for provider-civo CRD...")
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
						InfoMessage("Provider-civo CRD is available")
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
		InfoMessage("Waiting for provider-helm CRD...")
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
						InfoMessage("Provider-helm CRD is available")
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
		InfoMessage("Waiting for provider-kubernetes CRD...")
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
						InfoMessage("Provider-kubernetes CRD is available")
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
func WaitForGrsfConfig(kubeClient apiv1.Interface, restConfig *rest.Config) error {
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
				if Contains(requiredKinds, r.Kind) {
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

func CreateClusterIssuer(kubeClient apiv1.Interface, sslEnable bool) error {
	// Apply clusterissuer.yaml if SSL is enabled
	if sslEnable {
		InfoMessage("Applying SSL cluster issuer configuration...")

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

		SuccessMessage("Applied cluster issuer configuration")
	}

	return nil
}

// waitForGrsfIntegration final checks
func WaitForGrsfIntegration(restConfig *rest.Config) error {
	// Wait for all Crossplane packages to be healthy
	InfoMessage("Checking Crossplane package health...")

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
					ErrorMessage(fmt.Sprintf("Failed to list Crossplane %s: %v", gvr.Resource, err))
					return err
				}
				continue
			}
			allPackages.Items = append(allPackages.Items, pkgList.Items...)
		}

		packages := &allPackages

		if len(packages.Items) == 0 {
			InfoMessage("No Crossplane packages found yet...")
			time.Sleep(10 * time.Second)
			continue
		}

		allHealthy := true
		for _, pkg := range packages.Items {
			InfoMessage(fmt.Sprintf("Checking package %s", pkg.GetName()))
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
				InfoMessage(fmt.Sprintf("Package %s not yet healthy", pkg.GetName()))
				break
			}
		}

		if allHealthy {
			SuccessMessage("All Crossplane packages are healthy")
			return nil
		}

		time.Sleep(10 * time.Second)
	}

	return fmt.Errorf("timeout waiting for Crossplane packages to be healthy")
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
			} else {
				return nil
			}
			break
		}
	}

	// Create kb-system namespace if it doesn't exist
	clientset, err := apiv1.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	InfoMessage("Checking if kb-system namespace exists...")
	_, err = clientset.CoreV1().Namespaces().Get(context.Background(), "kb-system", v1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			ns := &corev1.Namespace{
				ObjectMeta: v1.ObjectMeta{
					Name: "kb-system",
				},
			}
			InfoMessage("Creating kb-system namespace...")
			_, err = clientset.CoreV1().Namespaces().Create(context.Background(), ns, v1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create kb-system namespace: %w", err)
			}
		} else {
			return fmt.Errorf("failed to check kb-system namespace: %w", err)
		}
	}

	InfoMessage("Installing KubeBlocks CRDs...")
	// 1. Create CRDs first
	crdsURL := "https://github.com/apecloud/kubeblocks/releases/download/v0.9.1/kubeblocks_crds.yaml"

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
	}

	InfoMessage("Waiting for CRDs to be established...")
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

	// Suppress wait-related logs
	// helmCfg.Log = func(format string, v ...interface{}) {}

	// 4. Create a Helm install client
	installClient := action.NewInstall(helmCfg)

	installClient.ReleaseName = "kubeblocks"
	installClient.Namespace = "kb-system"
	installClient.CreateNamespace = true
	installClient.Timeout = 1200 * time.Second // 20 minute timeout
	installClient.Version = "0.9.1"
	// installClient.Wait = true
	installClient.Description = "Installing KubeBlocks"

	// 5. Locate and load the chart
	InfoMessage("Locating KubeBlocks chart...")
	chartPath, err := installClient.ChartPathOptions.LocateChart("kubeblocks/kubeblocks", settings)
	if err != nil {
		return fmt.Errorf("failed to locate KubeBlocks chart: %w", err)
	}

	InfoMessage("Loading KubeBlocks chart...")
	chartRequested, err := loader.Load(chartPath)
	if err != nil {
		return fmt.Errorf("failed to load chart at path [%s]: %w", chartPath, err)
	}

	// Set values to ensure installation in kb-system namespace
	values := map[string]interface{}{
		"image": map[string]interface{}{
			"registry":   "docker.io",
			"repository": "apecloud/kubeblocks",
		},
		"dataScriptImage": map[string]interface{}{
			"registry":   "docker.io",
			"repository": "apecloud/kubeblocks-datascript",
		},
		"toolImage": map[string]interface{}{
			"registry":   "docker.io",
			"repository": "apecloud/kubeblocks-tools",
		},
	}
	InfoMessage("Installing KubeBlocks chart...")
	if _, err := installClient.Run(chartRequested, values); err != nil {
		return fmt.Errorf("failed to install the KubeBlocks chart: %w", err)
	}

	return nil
}

func WaitForGrappleReady(restConfig *rest.Config) error {
	// Wait for all Crossplane packages to be healthy
	InfoMessage("Waiting for grpl to be ready")

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
				ErrorMessage(fmt.Sprintf("Failed to list Crossplane %s for grpl: %v", gvr.Resource, err))
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

		InfoMessage(fmt.Sprintf("Checking package %s", grplPackage.GetName()))
		conditions, found, err := unstructured.NestedSlice(grplPackage.Object, "status", "conditions")
		if err != nil || !found {
			InfoMessage(fmt.Sprintf("Package %s not yet healthy", grplPackage.GetName()))
			continue
		}

		isHealthy := false
		for _, condition := range conditions {
			conditionMap := condition.(map[string]interface{})
			if conditionMap["type"] == "Healthy" && conditionMap["status"] == "True" {
				SuccessMessage("grpl is ready")
				return nil
			}
		}

		if !isHealthy {
			InfoMessage(fmt.Sprintf("Package %s not yet healthy", grplPackage.GetName()))
			continue
		}

		time.Sleep(10 * time.Second)
	}

	return fmt.Errorf("timeout waiting for Crossplane packages to be healthy")
}

// waitForDeployment waits for a deployment to be ready
func WaitForDeployment(kubeClient *apiv1.Clientset, namespace, name string) error {
	for {
		deployment, err := kubeClient.AppsV1().Deployments(namespace).Get(context.TODO(), name, v1.GetOptions{})
		if err != nil {
			return err
		}

		if deployment.Status.ReadyReplicas == *deployment.Spec.Replicas {
			return nil
		}

		InfoMessage(fmt.Sprintf("Waiting for deployment %s in namespace %s to be ready...", name, namespace))
		time.Sleep(5 * time.Second)
	}
}

func UninstallGrapple(connectToCluster func() error, logOnFileStart, logOnCliAndFileStart func()) error {

	// Initialize Kubernetes clients
	settings := cli.New()

	config, clientset, err := GetKubernetesConfig()
	if err != nil {
		InfoMessage("No existing connection found")
		err = connectToCluster()
		if err != nil {
			ErrorMessage(fmt.Sprintf("Failed to connect to cluster: %v", err))
			return err
		}

		config, err = settings.RESTClientGetter().ToRESTConfig()
		if err != nil {
			ErrorMessage(fmt.Sprintf("Failed to get REST config: %v", err))
			return err
		}

		clientset, err = apiv1.NewForConfig(config)
		if err != nil {
			ErrorMessage(fmt.Sprintf("Failed to create Kubernetes client: %v", err))
			return err
		}
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		ErrorMessage(fmt.Sprintf("Failed to create dynamic client: %v", err))
		return err
	}

	InfoMessage("Checking and deleting all Grapple resources across all namespaces...")
	logOnFileStart()

	// Get all CRDs with grpl in the name
	InfoMessage("Getting all Grapple CRDs...")
	crdGVR := schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}

	crdList, err := dynamicClient.Resource(crdGVR).List(context.TODO(), v1.ListOptions{})
	if err != nil {
		ErrorMessage(fmt.Sprintf("Failed to list CRDs: %v", err))
		return err
	}

	// Track unique namespaces that have Grapple resources
	namespacesToDelete := make(map[string]bool)

	// Delete all CRDs with grpl in the name
	for _, crd := range crdList.Items {
		name := crd.GetName()
		if strings.Contains(strings.ToLower(name), "grpl") {
			InfoMessage(fmt.Sprintf("Deleting CRD '%s'...", name))
			err := dynamicClient.Resource(crdGVR).Delete(context.TODO(), name, v1.DeleteOptions{})
			if err != nil {
				ErrorMessage(fmt.Sprintf("Failed to delete CRD '%s': %v", name, err))
			} else {
				SuccessMessage(fmt.Sprintf("CRD '%s' deleted", name))
			}
		}
	}

	// Delete collected namespaces
	for namespace := range namespacesToDelete {
		InfoMessage(fmt.Sprintf("Deleting namespace '%s'...", namespace))
		err := clientset.CoreV1().Namespaces().Delete(context.TODO(), namespace, v1.DeleteOptions{})
		if err != nil {
			ErrorMessage(fmt.Sprintf("Failed to delete namespace '%s': %v", namespace, err))
		} else {
			SuccessMessage(fmt.Sprintf("Namespace '%s' deleted", namespace))
		}
	}

	logOnCliAndFileStart()
	SuccessMessage("All Grapple resources deleted across all namespaces")

	InfoMessage("Checking and deleting kb-system namespace if it exists...")
	logOnFileStart()

	// Check and delete kb-system namespace if it exists
	_, err = clientset.CoreV1().Namespaces().Get(context.TODO(), "kb-system", v1.GetOptions{})
	if err == nil {
		InfoMessage("Found kb-system namespace, uninstalling kubeblocks...")

		// Initialize Helm for kb-system
		settings.SetNamespace("kb-system")
		actionConfig := new(action.Configuration)
		if err := actionConfig.Init(settings.RESTClientGetter(), "kb-system", os.Getenv("HELM_DRIVER"), log.Printf); err != nil {
			ErrorMessage(fmt.Sprintf("Failed to initialize helm config: %v", err))
		} else {
			// Uninstall kubeblocks helm release
			uninstall := action.NewUninstall(actionConfig)
			_, err := uninstall.Run("kubeblocks")
			if err != nil {
				ErrorMessage(fmt.Sprintf("Failed to uninstall kubeblocks: %v", err))
			} else {
				SuccessMessage("Kubeblocks uninstalled successfully")
			}
		}

		// Delete kb-system namespace
		InfoMessage("Deleting kb-system namespace...")
		err = clientset.CoreV1().Namespaces().Delete(context.TODO(), "kb-system", v1.DeleteOptions{})
		if err != nil {
			ErrorMessage(fmt.Sprintf("Failed to delete kb-system namespace: %v", err))
		} else {
			SuccessMessage("kb-system namespace deleted")
		}
	}

	logOnCliAndFileStart()
	SuccessMessage("kubeblocks uninstalled and kb-system namespace deleted successfully")

	// Check if grpl-system namespace exists
	_, err = clientset.CoreV1().Namespaces().Get(context.TODO(), "grpl-system", v1.GetOptions{})
	if err == nil {
		// Initialize Helm for grpl-system
		settings.SetNamespace("grpl-system")
		actionConfig := new(action.Configuration)
		if err := actionConfig.Init(settings.RESTClientGetter(), "grpl-system", os.Getenv("HELM_DRIVER"), log.Printf); err != nil {
			return fmt.Errorf("failed to initialize helm config: %w", err)
		}

		// Uninstall Helm releases in reverse order
		releases := []string{"grsf-integration", "grsf-config", "grsf", "grsf-init"}
		for _, release := range releases {
			InfoMessage(fmt.Sprintf("Uninstalling %s...", release))
			logOnFileStart()
			uninstall := action.NewUninstall(actionConfig)
			_, err := uninstall.Run(release)
			logOnCliAndFileStart()
			if err != nil {
				ErrorMessage(fmt.Sprintf("Failed to uninstall %s: %v", release, err))
				// Continue with other releases even if one fails
			} else {
				SuccessMessage(fmt.Sprintf("%s uninstalled successfully", release))
			}
		}

		// Delete grpl-system namespace
		InfoMessage("Deleting grpl-system namespace...")
		logOnFileStart()
		err = clientset.CoreV1().Namespaces().Delete(context.TODO(), "grpl-system", v1.DeleteOptions{})
		logOnCliAndFileStart()
		if err != nil {
			ErrorMessage(fmt.Sprintf("Failed to delete namespace: %v", err))
		} else {
			SuccessMessage("Namespace deleted successfully")
		}

		// Wait for namespace deletion
		InfoMessage("Waiting for namespace deletion to complete...")
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			_, err := clientset.CoreV1().Namespaces().Get(context.TODO(), "grpl-system", v1.GetOptions{})
			if err != nil {
				break
			}
			time.Sleep(5 * time.Second)
		}
	} else {
		InfoMessage("grpl-system namespace not found, skipping uninstallation steps")
	}

	SuccessMessage("Grapple uninstallation completed!")
	return nil
}
