package k3d

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/grapple-solution/grapple_cli/utils"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiv1 "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Variables for command flags
var (
	grappleVersion string
	autoConfirm    bool
	// clusterName       string
	clusterIP         string
	grappleDNS        string
	organization      string
	email             string
	installKubeblocks bool
	// waitForReady      bool
	sslEnable        bool
	sslIssuer        string
	grappleLicense   string
	completeDomain   string
	clusterName      string
	nodes            int
	waitForReady     bool
	skipConfirmation bool
)

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
