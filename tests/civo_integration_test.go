package tests

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
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
	clusterName = "civo-integration-test"
)

var failed bool

func TestCivoIntegration(t *testing.T) {
	// Setup
	if civoAPIKey := os.Getenv("CIVO_API_TOKEN"); civoAPIKey == "" {
		t.Fatal("CIVO_API_TOKEN environment variable is required")
	}

	t.Run("Check if cluster exists", func(t *testing.T) {
		log.Println("Starting test: Check if cluster exists")
		os.Remove("/tmp/failed_flag")

		// Configure Civo CLI
		err := runCmdWithoutLogs(t, "civo", "apikey", "add", "grapple", os.Getenv("CIVO_API_TOKEN"))
		if err != nil {
			setFailed(t)
		}

		err = runCmdWithoutLogs(t, "civo", "apikey", "current", "grapple")
		if err != nil {
			setFailed(t)
		}

		err = runCmdWithoutLogs(t, "civo", "region", "use", "fra1")
		if err != nil {
			setFailed(t)
		}

		// Check if cluster exists
		utils.InfoMessage("Checking if cluster exists")
		err = runCmdWithoutLogs(t, "civo", "k8s", "show", clusterName)
		if err == nil {
			// Cluster exists, delete it
			utils.InfoMessage("Cluster exists, deleting it")
			err = runCmdWithoutLogs(t, "civo", "k8s", "delete", clusterName, "-y")
			if err != nil {
				setFailed(t)
			}
		}
	})

	t.Run("Create and Install Grapple on Cluster", func(t *testing.T) {
		log.Println("Starting test: Create and Install Grapple on Cluster")
		checkPreviousTestFailed(t)

		err := runCmd(t, "grapple", "civo", "create-install",
			"--cluster-name="+clusterName,
			"--civo-region=fra1",
			"--civo-email-address=info@grapple-solutions.com",
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
		_ = runCmd(t, "grapple", "e", "d",
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
		defer resp.Body.Close()

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
		err := runCmd(t, "grapple", "civo", "remove", "--cluster-name", clusterName, "--civo-region", "fra1", "-y")
		if err != nil {
			setFailed(t)
		}
	})

	t.Run("Check test result", func(t *testing.T) {
		log.Println("Starting test: Check test result")
		if failed {
			t.Fatal("Test suite failed")
		}
	})
}

func runCmd(t *testing.T, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runCmdWithoutLogs(t *testing.T, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Run()
}
func setFailed(t *testing.T) {
	failed = true
	err := os.WriteFile("/tmp/failed_flag", []byte("true"), 0644)
	if err != nil {
		t.Fatal(err)
	}
}

func checkPreviousTestFailed(t *testing.T) {
	if failed {
		utils.ErrorMessage("Skipping test because previous test failed")
		t.Skip("Previous test failed")
	}

	data, err := os.ReadFile("/tmp/failed_flag")
	if err == nil && string(data) == "true" {
		utils.ErrorMessage("Skipping test because previous test failed")
		t.Skip("Previous test failed")
	}
}
