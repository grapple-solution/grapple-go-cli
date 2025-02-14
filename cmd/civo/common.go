package civo

import (
	"fmt"
	"time"

	"github.com/civo/civogo"
	"github.com/grapple-solution/grapple_cli/utils"
)

// Command-line flags
var (
	// Cluster creation flags
	targetPlatform string
	clusterName    string
	applications   string
	nodes          int
	size           string

	// Common flags
	autoConfirm       bool
	civoAPIKey        string
	civoRegion        string
	civoEmailAddress  string
	installKubeblocks bool

	// Installation specific flags
	grappleVersion string
	civoClusterID  string
	clusterIP      string
	grappleDNS     string
	organization   string
	waitForReady   bool
	sslEnable      bool
	sslIssuer      string
	completeDomain string
	grappleLicense string
)

const (
	secKeyEmail               = "email"
	secKeyOrganization        = "organization"
	secKeyClusterdomain       = "clusterdomain"
	secKeyGrapiversion        = "grapiversion"
	secKeyGruimversion        = "gruimversion"
	secKeyDev                 = "dev"
	secKeySsl                 = "ssl"
	secKeySslissuer           = "sslissuer"
	secKeyClusterName         = "cluster-name"
	secKeyGrapleDNS           = "grapple-dns"
	secKeyGrapleVersion       = "grapple-version"
	secKeyGrapleLicense       = "grapple-license"
	secKeyProviderClusterType = "provider-cluster-type"
	secKeyCivoClusterID       = "civo-cluster-id"
	secKeyCivoRegion          = "civo-region"
	secKeyCivoMasterIP        = "civo-master-ip"
)

const (
	providerClusterTypeCivo = "CIVO"
)

// Wait for the cluster to be ready
func waitForClusterReady(client *civogo.Client, cluster *civogo.KubernetesCluster) error {
	endTime := time.Now().Add(5 * time.Minute)

	for time.Now().Before(endTime) {
		status, err := client.GetKubernetesCluster(cluster.ID)
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Error fetching cluster status: %v", err))
			time.Sleep(10 * time.Second)
			continue
		}
		if status.Ready {
			utils.SuccessMessage("Cluster is ready.")
			return nil
		}
		time.Sleep(10 * time.Second)
	}

	utils.ErrorMessage(fmt.Sprintf("Cluster '%s' was not ready within the timeout", cluster.Name))
	return fmt.Errorf("cluster '%s' was not ready within the timeout", cluster.Name)
}
