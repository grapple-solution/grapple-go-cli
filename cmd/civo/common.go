package civo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
	key               string
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
	secKeyClusterName         = "CLUSTER_NAME"
	secKeyGrapleDNS           = "GRAPPLE_DNS"
	secKeyGrapleVersion       = "GRAPPLE_VERSION"
	secKeyGrapleLicense       = "GRAPPLE_LICENSE"
	secKeyProviderClusterType = "PROVIDER_CLUSTER_TYPE"
	secKeyCivoClusterID       = "CIVO_CLUSTER_ID"
	secKeyCivoRegion          = "CIVO_REGION"
	secKeyCivoMasterIP        = "CIVO_MASTER_IP"
)

const (
	providerClusterTypeCivo = "CIVO"
)

var (
	connectToCivoCluster = true
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

func getCivoAPIKey() string {
	key = os.Getenv("CIVO_API_TOKEN")
	if key == "" {
		key, _ = getCivoKeyFromConfig()

		if key == "" {
			// Prompt user for API key if not found
			utils.InfoMessage("No Civo API key found. Please enter your API key:")
			apiKey, err := utils.PromptInput("Civo API Key", "", "^[A-Za-z0-9]+$")
			if err != nil {
				utils.ErrorMessage("Failed to get API key input")
				return ""
			}
			key = apiKey
		}
	}

	// Set the API key as environment variable
	os.Setenv("CIVO_API_TOKEN", key)

	return key
}

func getCivoKeyFromConfig() (string, error) {

	// Check if API key exists in local config
	homeDir, err := os.UserHomeDir()
	if err != nil {
		utils.ErrorMessage("Failed to get home directory")
		return "", err
	}

	key := ""
	civoConfigPath := filepath.Join(homeDir, ".civo.json")
	if _, err := os.Stat(civoConfigPath); err == nil {
		// File exists, check for API key
		configData, err := os.ReadFile(civoConfigPath)
		if err != nil {
			utils.ErrorMessage("Failed to read Civo config file")
			return "", err
		}

		var config struct {
			APIKeys map[string]string `json:"apikeys"`
			Meta    struct {
				CurrentAPIKey string `json:"current_apikey"`
			} `json:"meta"`
		}

		if err := json.Unmarshal(configData, &config); err != nil {
			utils.ErrorMessage("Failed to parse Civo config file")
			return "", err
		}

		if len(config.APIKeys) > 0 {
			var apiKeyNames []string

			// Add all API key names to slice
			for name := range config.APIKeys {
				apiKeyNames = append(apiKeyNames, name)
			}
			// Prompt user to select API key
			selectedKey, err := utils.PromptSelect("Select API key to use", apiKeyNames)
			if err != nil {
				utils.ErrorMessage("API key selection is required")
				return "", err
			}

			key = config.APIKeys[selectedKey]
			utils.InfoMessage("Using selected API key from local Civo config")
		}
	}

	return key, nil
}

func getCivoRegion(key string) []string {

	// Create HTTP client
	client := &http.Client{}
	req, err := http.NewRequest("GET", "https://api.civo.com/v2/regions", nil)
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to create request: %v", err))
		return []string{"nyc1", "phx1", "fra1", "lon1"} // Return default regions on error
	}

	// Add authorization header
	req.Header.Add("Authorization", "bearer "+key)

	// Make request
	resp, err := client.Do(req)
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to get regions: %v", err))
		return []string{} // Return default regions on error
	}
	defer resp.Body.Close()

	// Parse response
	var regions []struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&regions); err != nil {
		utils.ErrorMessage(fmt.Sprintf("Failed to parse regions: %v", err))
		return []string{} // Return default regions on error
	}

	// Extract region codes
	var regionCodes []string
	for _, region := range regions {
		regionCodes = append(regionCodes, region.Code)
	}

	return regionCodes
}
