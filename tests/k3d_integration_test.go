package tests

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/grapple-solution/grapple_cli/utils"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	k3dClusterName = "k3d-integration-test"
)

func TestK3dIntegration(t *testing.T) {
	t.Run("Check if cluster exists", func(t *testing.T) {
		utils.InfoMessage("Starting k3d integration test suite")
		log.Println("Starting test: Check if cluster exists")
		if err := os.Remove("/tmp/failed_flag"); err != nil && !os.IsNotExist(err) {
			log.Printf("Failed to remove failed_flag: %v", err)
		}

		// Check if cluster exists
		utils.InfoMessage("Checking if cluster exists")
		err := runCmdWithoutLogs("k3d", "cluster", "list", k3dClusterName)
		if err == nil {
			// Cluster exists, delete it
			utils.InfoMessage("Cluster exists, deleting it")
			err = runCmdWithoutLogs("k3d", "cluster", "delete", k3dClusterName)
			if err != nil {
				setFailed(t)
			}
		}
	})

	t.Run("Create and Install Grapple on Cluster", func(t *testing.T) {
		log.Println("Starting test: Create and Install Grapple on Cluster")
		checkPreviousTestFailed(t)

		err := runCmd("grapple", "k3d", "create-install",
			"--cluster-name="+k3dClusterName,
			"--auto-confirm",
			"--wait",
			"--install-kubeblocks")
		if err != nil {
			setFailed(t)
		}
	})

	t.Run("Wait for Grapple to be ready", func(t *testing.T) {
		log.Println("Starting test: Wait for Grapple to be ready")
		checkPreviousTestFailed(t)

		// Get kubernetes client
		config, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
		if err != nil {
			setFailed(t)
			t.Fatal(err)
		}

		err = utils.WaitForGrappleReady(config)
		if err != nil {
			setFailed(t)
			t.Fatal(err)
		}
	})

	t.Run("Deploy example application", func(t *testing.T) {
		log.Println("Starting test: Deploy example application")
		checkPreviousTestFailed(t)

		dbMysqlModelBased := utils.DB_MYSQL_MODEL_BASED
		dbInternal := utils.DB_INTERNAL

		if dbMysqlModelBased == "" || dbInternal == "" {
			setFailed(t)
			t.Skip("DB_MYSQL_MODEL_BASED or DB_INTERNAL value is not set")
		}

		fmt.Println("Deploying example application")
		_ = runCmd("grapple", "e", "d",
			"--gras-template="+dbMysqlModelBased,
			"--db-type="+dbInternal)

		fmt.Println("Waiting for example application to be ready")
		time.Sleep(10 * time.Second)
	})

	t.Run("Wait for example readiness", func(t *testing.T) {
		log.Println("Starting test: Wait for example readiness")
		checkPreviousTestFailed(t)

		config, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
		if err != nil {
			setFailed(t)
			t.Fatal(err)
		}

		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			setFailed(t)
			t.Fatal(err)
		}

		utils.InfoMessage("Waiting for grapi deployment to be ready...")
		deploymentName := fmt.Sprintf("%s-%s-grapi", "grpl-mdl-int", "gras-mysql")
		err = utils.WaitForExampleDeployment(clientset, "grpl-mdl-int", deploymentName)
		if err != nil {
			setFailed(t)
			t.Fatal(err)
		}
		utils.SuccessMessage("grapi deployment is ready")

		utils.InfoMessage("Waiting for gruim deployment to be ready...")
		deploymentName = fmt.Sprintf("%s-%s-gruim", "grpl-mdl-int", "gras-mysql")
		err = utils.WaitForExampleDeployment(clientset, "grpl-mdl-int", deploymentName)
		if err != nil {
			setFailed(t)
			t.Fatal(err)
		}
		utils.SuccessMessage("gruim deployment is ready")
	})

	t.Run("Test the UI", func(t *testing.T) {
		log.Println("Starting test: Test the UI")
		checkPreviousTestFailed(t)

		// Wait for 20 seconds before testing the UI
		utils.InfoMessage("Waiting 20 seconds before testing the UI...")
		time.Sleep(20 * time.Second)
		utils.InfoMessage("Continuing with UI testing")

		config, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
		if err != nil {
			setFailed(t)
			t.Fatal(err)
		}

		// Create dynamic client
		dynamicClient, err := dynamic.NewForConfig(config)
		if err != nil {
			setFailed(t)
			t.Fatal(err)
		}

		// Define GVR for MUIM resource
		muimGVR := schema.GroupVersionResource{
			Group:    "grsf.grpl.io",
			Version:  "v1alpha1",
			Resource: "manageduimodules",
		}

		// Get MUIM resource with retries
		var muim *unstructured.Unstructured
		maxRetries := 5
		for i := 0; i < maxRetries; i++ {
			muim, err = dynamicClient.Resource(muimGVR).Namespace("grpl-mdl-int").Get(context.TODO(), "grpl-mdl-int-gras-mysql-gruim", v1.GetOptions{})
			if err == nil {
				break
			}
			if !strings.Contains(err.Error(), "the server could not find the requested resource") {
				setFailed(t)
				t.Fatal(err)
			}
			utils.InfoMessage(fmt.Sprintf("MUIM resource not found, retrying in 10s (attempt %d/%d)", i+1, maxRetries))
			time.Sleep(10 * time.Second)
		}

		if err != nil {
			setFailed(t)
			t.Fatal("Failed to get MUIM resource after retries")
		}

		baseURL := muim.Object["spec"].(map[string]interface{})["remoteentry"].(string)
		baseURL = baseURL[:strings.LastIndex(baseURL, "/")]

		if baseURL == "" {
			setFailed(t)
			t.Fatal("base URL is empty")
		}

		// Use http client instead of curl
		client := &http.Client{}
		resp, err := client.Get(baseURL)
		if err != nil {
			setFailed(t)
			t.Fatal(err)
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				log.Printf("Failed to close response body: %v", err)
			}
		}()

		if resp.StatusCode != 200 {
			setFailed(t)
			t.Fatalf("Expected status code 200, got %d", resp.StatusCode)
		} else {
			utils.SuccessMessage("UI is ready")
		}
	})

	t.Run("Destroy the cluster", func(t *testing.T) {
		log.Println("Starting test: Destroy the cluster")
		checkPreviousTestFailed(t)
		log.Println("Destroying the cluster")
		err := runCmd("grapple", "k3d", "remove", "--cluster-name", k3dClusterName, "-y")
		if err != nil {
			setFailed(t)
		}
	})

	t.Run("Check test result", func(t *testing.T) {
		log.Println("Starting test: Check test result")
		data, err := os.ReadFile("/tmp/failed_flag")
		if err == nil && string(data) == "true" {
			utils.ErrorMessage("Test suite failed")
			t.Fatal("Test suite failed")
		}
		utils.SuccessMessage("Test suite passed")
	})
}
