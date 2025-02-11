package civo

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// UninstallCmd represents the uninstall command
var UninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall Grapple from the cluster",
	Long: `Uninstall command removes all Grapple components and resources from the cluster.
This will completely remove all traces of Grapple installation including:
- All Grapple namespaces and resources
- Configuration settings
- Deployed applications
- Associated storage volumes and data`,
	RunE: runUninstall,
}

func init() {
	UninstallCmd.Flags().BoolVar(&autoConfirm, "auto-confirm", true, "If true, uninstalls grapple from the currently connected Civo cluster. If false, prompts for cluster name and civo region and removes grapple from the specified cluster. Default value of auto-confirm is true")
	UninstallCmd.Flags().StringVar(&civoRegion, "civo-region", "", "Civo region")
	UninstallCmd.Flags().StringVar(&clusterName, "cluster-name", "", "Civo cluster name")
}

func runUninstall(cmd *cobra.Command, args []string) error {
	logFile, logOnFileStart, logOnCliAndFileStart := utils.GetLogWriters("/tmp/grpl_civo_uninstall.log")

	var err error

	defer func() {
		logFile.Sync()
		logFile.Close()
		if err != nil {
			utils.ErrorMessage("Failed to uninstall grpl, please run cat /tmp/grpl_civo_uninstall.log for more details")
		}
	}()

	logOnCliAndFileStart()

	// Connect to cluster
	connectToCivoCluster := func() error {
		if autoConfirm {
			reconnect = false
		}
		err := connectToCluster(cmd, args)
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to connect to cluster: %v", err))
			return err
		}
		return nil
	}

	err = connectToCivoCluster()
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to connect to cluster: %v", err))
		return err
	}

	// Initialize Kubernetes clients
	settings := cli.New()
	config, err := settings.RESTClientGetter().ToRESTConfig()
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to get REST config: %v", err))
		return err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to create Kubernetes client: %v", err))
		return err
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to create dynamic client: %v", err))
		return err
	}

	utils.InfoMessage("Checking and deleting all Grapple resources across all namespaces...")
	logOnFileStart()

	// Get all CRDs with grpl in the name
	utils.InfoMessage("Getting all Grapple CRDs...")
	crdGVR := schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}

	crdList, err := dynamicClient.Resource(crdGVR).List(context.TODO(), v1.ListOptions{})
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to list CRDs: %v", err))
		return err
	}

	// Track unique namespaces that have Grapple resources
	namespacesToDelete := make(map[string]bool)

	// Process each CRD that contains "grpl"
	for _, crd := range crdList.Items {
		crdName := crd.GetName()
		if !strings.Contains(strings.ToLower(crdName), "grpl") {
			continue
		}

		group := crd.Object["spec"].(map[string]interface{})["group"].(string)
		versions := crd.Object["spec"].(map[string]interface{})["versions"].([]interface{})
		version := versions[0].(map[string]interface{})["name"].(string)
		resourceName := crd.Object["spec"].(map[string]interface{})["names"].(map[string]interface{})["plural"].(string)

		utils.InfoMessage(fmt.Sprintf("Found Grapple CRD: %s (Group: %s, Version: %s, Resource: %s)",
			crdName, group, version, resourceName))

		// Create GVR for this resource type
		gvr := schema.GroupVersionResource{
			Group:    group,
			Version:  version,
			Resource: resourceName,
		}

		// List all resources of this type across all namespaces
		resources, err := dynamicClient.Resource(gvr).List(context.TODO(), v1.ListOptions{})
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to list resources for %s: %v", crdName, err))
			continue
		}

		// Delete each resource and track its namespace
		for _, resource := range resources.Items {
			namespace := resource.GetNamespace()
			name := resource.GetName()

			if namespace != "" && namespace != "default" {
				namespacesToDelete[namespace] = true
			}

			utils.InfoMessage(fmt.Sprintf("Deleting %s '%s' from namespace '%s'...", resourceName, name, namespace))

			var deleteErr error
			if namespace == "" {
				deleteErr = dynamicClient.Resource(gvr).Delete(context.TODO(), name, v1.DeleteOptions{})
			} else {
				deleteErr = dynamicClient.Resource(gvr).Namespace(namespace).Delete(context.TODO(), name, v1.DeleteOptions{})
			}

			if deleteErr != nil {
				utils.ErrorMessage(fmt.Sprintf("Failed to delete %s '%s': %v", resourceName, name, deleteErr))
			} else {
				utils.SuccessMessage(fmt.Sprintf("%s '%s' deleted", resourceName, name))
			}
		}
	}

	// Delete collected namespaces
	for namespace := range namespacesToDelete {
		utils.InfoMessage(fmt.Sprintf("Deleting namespace '%s'...", namespace))
		err := clientset.CoreV1().Namespaces().Delete(context.TODO(), namespace, v1.DeleteOptions{})
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to delete namespace '%s': %v", namespace, err))
		} else {
			utils.SuccessMessage(fmt.Sprintf("Namespace '%s' deleted", namespace))
		}
	}

	logOnCliAndFileStart()
	utils.SuccessMessage("All Grapple resources deleted across all namespaces")

	utils.InfoMessage("Checking and deleting kb-system namespace if it exists...")
	logOnFileStart()

	// Check and delete kb-system namespace if it exists
	_, err = clientset.CoreV1().Namespaces().Get(context.TODO(), "kb-system", v1.GetOptions{})
	if err == nil {
		utils.InfoMessage("Found kb-system namespace, uninstalling kubeblocks...")

		// Initialize Helm for kb-system
		settings.SetNamespace("kb-system")
		actionConfig := new(action.Configuration)
		if err := actionConfig.Init(settings.RESTClientGetter(), "kb-system", os.Getenv("HELM_DRIVER"), log.Printf); err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to initialize helm config: %v", err))
		} else {
			// Uninstall kubeblocks helm release
			uninstall := action.NewUninstall(actionConfig)
			_, err := uninstall.Run("kubeblocks")
			if err != nil {
				utils.ErrorMessage(fmt.Sprintf("Failed to uninstall kubeblocks: %v", err))
			} else {
				utils.SuccessMessage("Kubeblocks uninstalled successfully")
			}
		}

		// Delete kb-system namespace
		utils.InfoMessage("Deleting kb-system namespace...")
		err = clientset.CoreV1().Namespaces().Delete(context.TODO(), "kb-system", v1.DeleteOptions{})
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to delete kb-system namespace: %v", err))
		} else {
			utils.SuccessMessage("kb-system namespace deleted")
		}
	}

	logOnCliAndFileStart()
	utils.SuccessMessage("kubeblocks uninstalled and kb-system namespace deleted successfully")

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
			utils.InfoMessage(fmt.Sprintf("Uninstalling %s...", release))
			logOnFileStart()
			uninstall := action.NewUninstall(actionConfig)
			_, err := uninstall.Run(release)
			logOnCliAndFileStart()
			if err != nil {
				utils.ErrorMessage(fmt.Sprintf("Failed to uninstall %s: %v", release, err))
				// Continue with other releases even if one fails
			} else {
				utils.SuccessMessage(fmt.Sprintf("%s uninstalled successfully", release))
			}
		}

		// Delete grpl-system namespace
		utils.InfoMessage("Deleting grpl-system namespace...")
		logOnFileStart()
		err = clientset.CoreV1().Namespaces().Delete(context.TODO(), "grpl-system", v1.DeleteOptions{})
		logOnCliAndFileStart()
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to delete namespace: %v", err))
		} else {
			utils.SuccessMessage("Namespace deleted successfully")
		}

		// Wait for namespace deletion
		utils.InfoMessage("Waiting for namespace deletion to complete...")
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			_, err := clientset.CoreV1().Namespaces().Get(context.TODO(), "grpl-system", v1.GetOptions{})
			if err != nil {
				break
			}
			time.Sleep(5 * time.Second)
		}
	} else {
		utils.InfoMessage("grpl-system namespace not found, skipping uninstallation steps")
	}

	utils.SuccessMessage("Grapple uninstallation completed!")
	return nil
}
