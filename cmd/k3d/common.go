package k3d

import (
	"os"
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
	sslEnable             bool
	sslIssuer             string
	grappleLicense        string
	completeDomain        string
	clusterName           string
	waitForReady          bool
	skipConfirmation      bool
	additionalValuesFiles []string
	server                int
	agent                 int
	httpLoadBalancer      string
	httpsLoadBalancer     string
	apiPort               string
)

// fileExists checks if a file exists and is not a directory
func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}
