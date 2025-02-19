// deploy.go
// Copyright © 2025 NAME HERE <EMAIL ADDRESS>
package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	// Helm Go SDK packages
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/registry"

	// Kubernetes client libraries

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// DeployCmd represents the deploy command.
var DeployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy a GrappleApplicationSet resource to your cluster",
	Long: `Deploy command creates and applies a GrappleApplicationSet resource to your Kubernetes cluster.

This command will:
  1. Read and update a YAML template (via interactive prompts or CLI flags)
  2. Validate prerequisites (using Go libraries instead of external CLI calls)
  3. Build a Kubernetes+Helm client and deploy your manifest to the cluster
  4. Wait for the deployment to become ready

Example:
  grpl resource deploy --name my-app --namespace default`,
	RunE: runDeploy,
}

func init() {
	// Setup cobra flags (bind these to the global variables)
	DeployCmd.Flags().StringVar(&GRASName, "gras-name", "", "Name of the GRAS resource")
	DeployCmd.Flags().StringVar(&GRASTemplate, "gras-template", "", "Template type to use")
	DeployCmd.Flags().StringVar(&DBType, "db-type", "", "Database type (internal or external)")
	DeployCmd.Flags().StringVar(&ModelsInput, "models", "", "Models input (if not interactive)")
	DeployCmd.Flags().StringVar(&RelationsInput, "relations", "", "Relations input (if not interactive)")
	DeployCmd.Flags().StringVar(&DatasourcesInput, "datasources", "", "Datasources input (if not interactive)")
	DeployCmd.Flags().StringVar(&DiscoveriesInput, "discoveries", "", "Discoveries input (if not interactive)")
	DeployCmd.Flags().StringVar(&DatabaseSchema, "database-schema", "", "Database schema")
	DeployCmd.Flags().BoolVar(&AutoDiscovery, "auto-discovery", false, "Auto discovery flag")
	DeployCmd.Flags().StringVar(&SourceData, "source-data", "", "Data source URL")
	DeployCmd.Flags().BoolVar(&EnableGRUIM, "enable-gruim", false, "Enables GRUIM")
	DeployCmd.Flags().StringVar(&DBFilePath, "db-file-path", "", "Path to DB file")
	DeployCmd.Flags().StringVar(&KubeContext, "kube-context", "", "Kubernetes context to use")
	DeployCmd.Flags().StringVar(&KubeNS, "namespace", "", "Kubernetes namespace to use")
}

var (
	restConfig *rest.Config
	clientset  *kubernetes.Clientset
)

// runDeploy is the main function for the deploy command.
func runDeploy(cmd *cobra.Command, args []string) error {

	var err error
	utils.InfoMessage("Getting Kubernetes config...")
	restConfig, clientset, err = utils.GetKubernetesConfig()
	if err != nil {
		utils.ErrorMessage("Failed to get Kubernetes config: " + err.Error())
		return err
	}

	logFile, logOnFileStart, logOnCliAndFileStart := utils.GetLogWriters("/tmp/grpl_resource_deploy.log")

	defer func() {
		logFile.Sync() // Ensure logs are flushed before closing
		logFile.Close()
		if err != nil {
			utils.ErrorMessage("Failed to connect to cluster, please run cat /tmp/grpl_civo_connect.log for more details")
		}
	}()

	logOnCliAndFileStart()

	// Validate and get GRAS name
	if GRASName != "" {
		if err := utils.ValidateResourceName(GRASName); err != nil {
			return err
		}
	} else {

	}

	utils.InfoMessage(fmt.Sprintf("gras name: %s", GRASName))

	// Check if GRAS_TEMPLATE is provided and validate it
	if GRASTemplate != "" {
		if err := utils.ValidateGrasTemplates(GRASTemplate); err != nil {
			return err
		}
	} else {
		// Prompt user to select template if not provided
		templates := utils.GrasTemplates
		prompt := promptui.Select{
			Label: "Please select template you want to create",
			Items: templates,
		}
		_, result, err := prompt.Run()
		if err != nil {
			return err
		}
		GRASTemplate = result
	}

	utils.InfoMessage(fmt.Sprintf("gras template: %s", GRASTemplate))

	err = prepareNamespaceForGrasInstallation()
	if err != nil {
		return err
	}

	// 3. Copy a base template file (from GRPL_WORKDIR) to our working file.
	if err := prepareTemplateFile(); err != nil {
		return err
	}

	if (GRASTemplate == utils.DB_MYSQL_MODEL_BASED || GRASTemplate == utils.DB_MYSQL_DISCOVERY_BASED) && DBType == utils.DB_EXTERNAL {
		var database, host, port, user, password, url string

		utils.InfoMessage("Updating resource for with datasource info")
		if DatasourcesInput != "" {
			utils.InfoMessage("Extracting datasource info...")
			database, host, port, user, password, url, err = extractDatasourceInfo(DatasourcesInput)
			if err != nil {
				return err
			}
		} else {
			utils.InfoMessage("Taking datasource info from CLI...")
			database, host, port, user, password, url, err = takeDatasourceInputFromCLI()
			if err != nil {
				return err
			}
		}

		DatabaseSchema = database
		URL = url

		// Create secret for external DB credentials
		utils.InfoMessage("Creating external db secret using collected datasource info...")

		// Create new secret
		newSecret := &corev1.Secret{
			ObjectMeta: v1.ObjectMeta{
				Name:      fmt.Sprintf("%s-conn-credential", GRASName),
				Namespace: KubeNS,
			},
			Data: map[string][]byte{
				"host":     []byte(host),
				"port":     []byte(port),
				"username": []byte(user),
				"password": []byte(password),
			},
		}

		_, err = clientset.CoreV1().Secrets(KubeNS).Create(context.TODO(), newSecret, v1.CreateOptions{})
		if k8serrors.IsAlreadyExists(err) {
			_, err = clientset.CoreV1().Secrets(KubeNS).Update(context.TODO(), newSecret, v1.UpdateOptions{})
			if err != nil {
				utils.ErrorMessage("Failed to update external db secret: " + err.Error())
				return err
			}
		}
		if err != nil {
			utils.ErrorMessage("Failed to create external db secret: " + err.Error())
			return err
		}
		utils.SuccessMessage("Created external db secret")

	} else if GRASTemplate == utils.DB_FILE {
		utils.InfoMessage("Taking DB file path...")
		if err := takeDBFilePath(); err != nil {
			return err
		}
		utils.InfoMessage("Updating resource for with datasource info")
		if err := updateTemplateForDataSourceIncaseOfDbFile(); err != nil {
			return err
		}
	}

	// 4. Process inputs – if models/datasources/discoveries/relations were passed via CLI, transform them.
	// Otherwise, invoke interactive functions.
	if GRASTemplate == utils.DB_MYSQL_MODEL_BASED {
		utils.InfoMessage("Updating resource with models info")
		if ModelsInput != "" {
			utils.InfoMessage("Transforming models input to YAML...")
			if err := transformModelInputToYAML(ModelsInput, templateFileDest); err != nil {
				return err
			}
		} else {
			utils.InfoMessage("Taking models input from CLI...")
			if err := takeModelInputFromCLI(templateFileDest); err != nil {
				return err
			}
		}
	}

	if GRASTemplate == utils.DB_MYSQL_DISCOVERY_BASED {
		utils.InfoMessage("Updating resource with discoveries info")
		if DiscoveriesInput != "" {
			utils.InfoMessage("Transforming discoveries input to YAML...")
			if err := transformDiscoveriesInputToYAML(DiscoveriesInput, templateFileDest); err != nil {
				return err
			}
		} else {
			utils.InfoMessage("Taking discoveries input from CLI...")
			if err := takeDiscoveryInputFromCLI(templateFileDest, cmd.Flags().Changed("auto-discovery")); err != nil {
				return err
			}
		}
	}

	if DBType == utils.DB_INTERNAL {
		utils.InfoMessage("Updating resource for internal DB info")
		utils.InfoMessage("Creating internal DB...")
		if err := createInternalDB(); err != nil {
			return err
		}
		utils.InfoMessage("Internal DB created")
		if err := updateTemplateForInternalDB(); err != nil {
			return err
		}
	} else if DBType == utils.DB_EXTERNAL {
		utils.InfoMessage("Updating resource for external DB info")
		if err := updateTemplateForExternalDB(); err != nil {
			return err
		}
	}

	if RelationsInput != "" {
		utils.InfoMessage("Updating resource with relations info")
		utils.InfoMessage("Transforming relations input to YAML...")
		if err := transformRelationInputToYAML(RelationsInput, templateFileDest); err != nil {
			return err
		}
	} else if !cmd.Flags().Changed("relations") {
		utils.InfoMessage("Taking relations input from CLI...")
		if err := takeRelationInputFromCLI(templateFileDest); err != nil {
			return err
		}
	}

	// 5. Ask for GRUIM enablement (interactive or by flag)
	utils.InfoMessage("Asking for GRUIM enablement...")
	if err := askGRUIMEnablement(templateFileDest, cmd.Flags().Changed("enable-gruim")); err != nil {
		return err
	}

	// Handle database schema and init containers
	utils.InfoMessage("Updating resource for init containers")
	if err := updateTemplateForInitContainers(cmd.Flags().Changed("source-data")); err != nil {
		return err
	}

	utils.InfoMessage("Updating resource for restcruds")
	if err := updateTemplateForRestcruds(); err != nil {
		return err
	}

	// 7. Substitute environment variables in the template (using os.ExpandEnv).
	utils.InfoMessage("Substituting environment variables in the template...")
	if err := substituteEnvVarsInTemplate(templateFileDest); err != nil {
		return err
	}

	if !isRender {
		// 8. Finally, deploy the template using the Helm Go SDK.
		utils.InfoMessage("Deploying the template using the Helm")
		logOnFileStart()
		if err := deployTemplate(templateFileDest, GRASName, KubeNS); err != nil {
			logOnCliAndFileStart()
			return err
		}
		logOnCliAndFileStart()

		// 9. Optionally, clean up the temporary file.
		// _ = os.Remove(templateFileDest)
	}

	utils.SuccessMessage("Resource deployed successfully!")
	return nil
}

// prepareTemplateFile copies a base template file from GRPL_WORKDIR into our working file.
func prepareTemplateFile() error {
	var src string
	if GRASTemplate == utils.DB_FILE {
		src = filepath.Join("template-files", "db-file.yaml")
	} else {
		src = filepath.Join("template-files", "db.yaml")
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read template file %s: %v", src, err)
	}
	return os.WriteFile(templateFileDest, data, 0644)
}

//
// Functions to transform the YAML template – these functions load the YAML into a map,
// update the relevant sections (such as grapi.models, grapi.datasources, etc.), and then write it back.
//

func transformModelInputToYAML(models string, tmplFile string) error {
	parts := strings.Split(models, "|")
	data, err := os.ReadFile(tmplFile)
	if err != nil {
		return err
	}
	var tmpl map[string]interface{}
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		return err
	}
	grapi, ok := tmpl["grapi"].(map[interface{}]interface{})
	if !ok {
		grapi = make(map[interface{}]interface{})
		tmpl["grapi"] = grapi
	}
	var modelsList []interface{}
	if ml, ok := grapi["models"].([]interface{}); ok {
		modelsList = ml
	}
	for _, part := range parts {
		part = strings.ReplaceAll(part, "'", "\"") // replace single quotes with double quotes
		subParts := strings.SplitN(part, ":", 2)
		if len(subParts) != 2 {
			continue
		}
		modelName := subParts[0]
		propertiesJSON := subParts[1]
		var props map[string]interface{}
		if err := json.Unmarshal([]byte(propertiesJSON), &props); err != nil {
			log.Printf("failed to unmarshal model properties: %v", err)
			continue
		}
		modelEntry := map[string]interface{}{
			"name": modelName,
			"spec": props,
		}
		modelsList = append(modelsList, modelEntry)
	}
	grapi["models"] = modelsList
	newData, err := yaml.Marshal(tmpl)
	if err != nil {
		return err
	}
	return os.WriteFile(tmplFile, newData, 0644)
}

func extractDatasourceInfo(ds string) (string, string, string, string, string, string, error) {
	parts := strings.Split(ds, "|")
	var database, host, port, user, password, url string

	for _, part := range parts {
		part = strings.ReplaceAll(part, "'", "\"")
		subParts := strings.SplitN(part, ":", 2)
		if len(subParts) != 2 {
			continue
		}
		propsJSON := subParts[1]
		var props map[string]interface{}
		if err := json.Unmarshal([]byte(propsJSON), &props); err != nil {
			log.Printf("failed to unmarshal datasource properties: %v", err)
			continue
		}

		if d, ok := props["database"].(string); ok {
			database = d
		}
		if h, ok := props["host"].(string); ok {
			host = h
		}
		if p, ok := props["port"].(string); ok {
			port = p
		}
		if u, ok := props["user"].(string); ok {
			user = u
		}
		if pw, ok := props["password"].(string); ok {
			password = pw
		}
		if u, ok := props["url"].(string); ok {
			url = u
		}

	}

	return database, host, port, user, password, url, nil
}

func transformDiscoveriesInputToYAML(discoveries string, tmplFile string) error {
	parts := strings.Split(discoveries, "|")
	data, err := os.ReadFile(tmplFile)
	if err != nil {
		return err
	}
	var tmpl map[string]interface{}
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		return err
	}
	grapi, ok := tmpl["grapi"].(map[interface{}]interface{})
	if !ok {
		grapi = make(map[interface{}]interface{})
		tmpl["grapi"] = grapi
	}
	var discList []interface{}
	if dl, ok := grapi["discoveries"].([]interface{}); ok {
		discList = dl
	}
	for _, part := range parts {
		part = strings.ReplaceAll(part, "'", "\"")
		subParts := strings.SplitN(part, ":", 2)
		if len(subParts) != 2 {
			continue
		}
		discoveryName := subParts[0]
		propsJSON := subParts[1]
		var props map[string]interface{}
		if err := json.Unmarshal([]byte(propsJSON), &props); err != nil {
			log.Printf("failed to unmarshal discovery properties: %v", err)
			continue
		}
		discEntry := map[string]interface{}{
			"name": discoveryName,
			"spec": props,
		}
		discList = append(discList, discEntry)
	}
	grapi["discoveries"] = discList
	newData, err := yaml.Marshal(tmpl)
	if err != nil {
		return err
	}
	return os.WriteFile(tmplFile, newData, 0644)
}

func transformRelationInputToYAML(relations string, tmplFile string) error {
	parts := strings.Split(relations, "|")
	data, err := os.ReadFile(tmplFile)
	if err != nil {
		return err
	}
	var tmpl map[string]interface{}
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		return err
	}
	grapi, ok := tmpl["grapi"].(map[interface{}]interface{})
	if !ok {
		grapi = make(map[interface{}]interface{})
		tmpl["grapi"] = grapi
	}
	var relList []interface{}
	if rl, ok := grapi["relations"].([]interface{}); ok {
		relList = rl
	}
	for _, part := range parts {
		part = strings.ReplaceAll(part, "'", "\"")
		subParts := strings.SplitN(part, ":", 2)
		if len(subParts) != 2 {
			continue
		}
		relationName := subParts[0]
		propsJSON := subParts[1]
		var props map[string]interface{}
		if err := json.Unmarshal([]byte(propsJSON), &props); err != nil {
			log.Printf("failed to unmarshal relation properties: %v", err)
			continue
		}
		relEntry := map[string]interface{}{
			"name": relationName,
			"spec": props,
		}
		relList = append(relList, relEntry)
	}
	grapi["relations"] = relList
	newData, err := yaml.Marshal(tmpl)
	if err != nil {
		return err
	}
	return os.WriteFile(tmplFile, newData, 0644)
}

//
// Interactive input functions using promptui.
//

func takeModelInputFromCLI(tmplFile string) error {
	for {
		// Prompt for model name
		modelName, err := utils.PromptInput("Enter model name (or leave empty to finish)", utils.DefaultValue, utils.EmptyValueRegex)
		if err != nil {
			return err
		}
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			return nil // Exit cleanly if empty model name
		}

		// Prompt for base class
		baseClass, err := utils.PromptSelect("Select model base class", []string{"Entity", "Model"})
		if err != nil {
			return err
		}

		// Prompt for properties
		properties := make(map[string]interface{})
		idSelected := false
		for {
			promptMsg := ""
			if len(properties) == 0 {
				promptMsg = "Enter property name (at least one property is required)"
			} else {
				promptMsg = "Enter property name (or leave empty to finish)"
			}

			propName, err := utils.PromptInput(promptMsg, utils.DefaultValue, utils.EmptyValueRegex)
			if err != nil {
				return err
			}
			propName = strings.TrimSpace(propName)
			if propName == "" && len(properties) == 0 {
				utils.InfoMessage("At least one property is required")
				continue
			}
			if propName == "" {
				break // Exit property loop if empty property name and we have properties
			}

			propType, err := utils.PromptSelect("Select property type", []string{
				"string", "integer", "boolean", "float", "array", "object", "date", "buffer", "geopoint", "any",
			})
			if err != nil {
				return err
			}

			propSpec := map[string]interface{}{
				"type": propType,
			}

			// Handle ID field logic
			if !idSelected {
				isID, err := utils.PromptConfirm(fmt.Sprintf("Is %s the ID property?", propName))
				if err != nil {
					return err
				}

				if isID {
					isGenerated, err := utils.PromptConfirm(fmt.Sprintf("Is %s generated automatically?", propName))
					if err != nil {
						return err
					}

					propSpec["id"] = true
					propSpec["required"] = true
					if isGenerated {
						propSpec["generated"] = true
					}
					idSelected = true
					properties[propName] = propSpec
					continue
				}
			}

			// Handle required/default value logic for non-ID fields
			required, err := utils.PromptConfirm("Is this property required?")
			if err != nil {
				return err
			}

			if required {
				propSpec["required"] = true
			} else {
				defaultValue, err := utils.PromptInput("Default value [leave blank for none]", utils.DefaultValue, utils.EmptyValueRegex)
				if err != nil {
					return err
				}
				if defaultValue != "" {
					propSpec["defaultFn"] = defaultValue
				}
			}

			properties[propName] = propSpec
		}

		// Update YAML template
		data, err := os.ReadFile(tmplFile)
		if err != nil {
			return err
		}
		var tmpl map[string]interface{}
		if err := yaml.Unmarshal(data, &tmpl); err != nil {
			return err
		}
		grapi, ok := tmpl["grapi"].(map[interface{}]interface{})
		if !ok {
			grapi = make(map[interface{}]interface{})
			tmpl["grapi"] = grapi
		}
		var modelsList []interface{}
		if ml, ok := grapi["models"].([]interface{}); ok {
			modelsList = ml
		}

		modelEntry := map[string]interface{}{
			"name": modelName,
			"spec": map[string]interface{}{
				"base":       baseClass,
				"properties": properties,
			},
		}
		modelsList = append(modelsList, modelEntry)
		grapi["models"] = modelsList

		newData, err := yaml.Marshal(tmpl)
		if err != nil {
			return err
		}
		if err := os.WriteFile(tmplFile, newData, 0644); err != nil {
			return err
		}
	}
}

func takeDatasourceInputFromCLI() (string, string, string, string, string, string, error) {
	dsName, err := utils.PromptInput("Enter datasource name", utils.DefaultValue, utils.EmptyValueRegex)
	if err != nil {
		return "", "", "", "", "", "", err
	}

	host, err := utils.PromptInput("Enter host", utils.DefaultValue, utils.EmptyValueRegex)
	if err != nil {
		return "", "", "", "", "", "", err
	}

	port, err := utils.PromptInput("Enter port", utils.DefaultValue, utils.EmptyValueRegex)
	if err != nil {
		return "", "", "", "", "", "", err
	}

	user, err := utils.PromptInput("Enter user", utils.DefaultValue, utils.EmptyValueRegex)
	if err != nil {
		return "", "", "", "", "", "", err
	}

	password, err := utils.PromptPassword("Enter password")
	if err != nil {
		return "", "", "", "", "", "", err
	}

	url, err := utils.PromptInput("Enter datasource URL (optional)", utils.DefaultValue, utils.EmptyValueRegex)
	if err != nil {
		return "", "", "", "", "", "", err
	}

	return dsName, host, port, user, password, url, nil
}

func takeDiscoveryInputFromCLI(tmplFile string, checkAutoDiscovery bool) error {
	var auto bool
	if AutoDiscovery || checkAutoDiscovery {
		auto = AutoDiscovery
	} else {
		choice, err := utils.PromptSelect("Do you want discovery to be created automatically?", []string{"Yes", "No"})
		if err != nil {
			return err
		}
		auto = (choice == "Yes")
	}
	data, err := os.ReadFile(tmplFile)
	if err != nil {
		return err
	}
	var tmpl map[string]interface{}
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		return err
	}
	grapi, ok := tmpl["grapi"].(map[interface{}]interface{})
	if !ok {
		grapi = make(map[interface{}]interface{})
		tmpl["grapi"] = grapi
	}
	var discEntry map[string]interface{}
	if auto {
		discEntry = map[string]interface{}{
			"name": DatabaseSchema,
			"spec": map[string]interface{}{
				"all":              true,
				"disableCamelCase": false,
				"schema":           DatabaseSchema,
				"dataSource":       DatabaseSchema,
			},
		}
	} else {
		discoveryName, err := utils.PromptInput("Enter discovery name", "", ".*")
		if err != nil {
			return err
		}
		utils.InfoMessage(fmt.Sprintf("discovery: %s", discoveryName))

		allModels, err := utils.PromptSelect("Discover all models without prompting users to select?", []string{"Yes", "No"})
		if err != nil {
			return err
		}

		all := false
		var models string
		if allModels == "Yes" {
			all = true
		} else {
			prompt := promptui.Prompt{
				Label: "Enter models (optional) e.g: table1,table2",
			}
			models, err = prompt.Run()
			if err != nil {
				return err
			}
			utils.InfoMessage(fmt.Sprintf("models: %s", models))
		}
		utils.InfoMessage(fmt.Sprintf("all: %v", all))

		optionalId, err := utils.PromptSelect("Mark id property as optional field?", []string{"Yes", "No"})
		if err != nil {
			return err
		}
		isOptionalId := optionalId == "Yes"
		utils.InfoMessage(fmt.Sprintf("optionalId: %v", isOptionalId))

		prompt := promptui.Prompt{
			Label: "Enter outDir (optional)",
		}
		outDir, err := prompt.Run()
		if err != nil {
			return err
		}
		utils.InfoMessage(fmt.Sprintf("outDir: %s", outDir))

		relations, err := utils.PromptSelect("Discover and create relations?", []string{"Yes", "No"})
		if err != nil {
			return err
		}
		hasRelations := relations == "Yes"
		utils.InfoMessage(fmt.Sprintf("relations: %v", hasRelations))

		views, err := utils.PromptSelect("Discover views?", []string{"Yes", "No"})
		if err != nil {
			return err
		}
		hasViews := views == "Yes"
		utils.InfoMessage(fmt.Sprintf("views: %v", hasViews))

		discEntry = map[string]interface{}{
			"name": discoveryName,
			"spec": map[string]interface{}{
				"all":              all,
				"views":            hasViews,
				"relations":        hasRelations,
				"optionalId":       isOptionalId,
				"disableCamelCase": false,
				"schema":           DatabaseSchema,
				"models":           models,
				"outDir":           outDir,
				"dataSource":       DatabaseSchema,
			},
		}
	}
	grapi["discoveries"] = []interface{}{discEntry}
	newData, err := yaml.Marshal(tmpl)
	if err != nil {
		return err
	}
	return os.WriteFile(tmplFile, newData, 0644)
}

func takeRelationInputFromCLI(tmplFile string) error {
	prompt := promptui.Prompt{
		Label: "Enter relation name",
	}
	relName, err := prompt.Run()
	if err != nil {
		return err
	}
	selectPrompt := promptui.Select{
		Label: "Select relation type",
		Items: []string{"belongsTo", "hasMany", "hasOne", "referencesMany"},
	}
	_, relType, err := selectPrompt.Run()
	if err != nil {
		return err
	}
	prompt = promptui.Prompt{
		Label: "Enter source model",
	}
	sourceModel, err := prompt.Run()
	if err != nil {
		return err
	}
	prompt = promptui.Prompt{
		Label: "Enter target model",
	}
	targetModel, err := prompt.Run()
	if err != nil {
		return err
	}
	prompt = promptui.Prompt{
		Label: "Enter foreign key name",
	}
	foreignKey, err := prompt.Run()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(tmplFile)
	if err != nil {
		return err
	}
	var tmpl map[string]interface{}
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		return err
	}
	grapi, ok := tmpl["grapi"].(map[interface{}]interface{})
	if !ok {
		grapi = make(map[interface{}]interface{})
		tmpl["grapi"] = grapi
	}
	relEntry := map[string]interface{}{
		"name": relName,
		"spec": map[string]interface{}{
			"relationType":     relType,
			"relationName":     relName,
			"sourceModel":      sourceModel,
			"destinationModel": targetModel,
			"foreignKeyName":   foreignKey,
		},
	}
	grapi["relations"] = []interface{}{relEntry}
	newData, err := yaml.Marshal(tmpl)
	if err != nil {
		return err
	}
	return os.WriteFile(tmplFile, newData, 0644)
}

// askGRUIMEnablement prompts whether to enable GRUIM and, if not, removes the "gruims" section from the template.
func askGRUIMEnablement(tmplFile string, isFlagSet bool) error {
	var enable bool
	if EnableGRUIM || isFlagSet {
		enable = EnableGRUIM
	} else {
		choice, err := utils.PromptSelect("Do you want to enable GRUIM?", []string{"Yes", "No"})
		if err != nil {
			return err
		}
		enable = (choice == "Yes")
	}

	data, err := os.ReadFile(tmplFile)
	if err != nil {
		return err
	}
	var tmpl map[string]interface{}
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		return err
	}
	if !enable {
		utils.InfoMessage("Disabling GRUIM...")
		delete(tmpl, "gruim")
	} else {
		utils.InfoMessage("Enabling GRUIM...")
	}
	newData, err := yaml.Marshal(tmpl)
	if err != nil {
		return err
	}
	return os.WriteFile(tmplFile, newData, 0644)
}

// takeDBFilePath prompts the user for a file path for the DB file.
func takeDBFilePath() error {
	path := ""
	if DBFilePath != "" {
		path = DBFilePath
	} else {
		prompt := promptui.Prompt{
			Label:   "Enter DB file path",
			Default: "/tmp/data.json",
		}
		var err error
		path, err = prompt.Run()
		if err != nil {
			return err
		}
	}
	DBFilePath = path
	os.Setenv("db_file", DBFilePath)

	return nil
}

func updateTemplateForInitContainers(sourceDataExplicitlySet bool) error {

	// Handle init containers based on source data
	data, err := os.ReadFile(templateFileDest)
	if err != nil {
		return err
	}
	var tmpl map[string]interface{}
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		return err
	}
	grapi, ok := tmpl["grapi"].(map[interface{}]interface{})
	if !ok {
		grapi = make(map[interface{}]interface{})
		tmpl["grapi"] = grapi
	}

	if GRASTemplate == utils.DB_MYSQL_MODEL_BASED || GRASTemplate == utils.DB_MYSQL_DISCOVERY_BASED {
		// Prompt for source data if not provided
		if SourceData == "" && !sourceDataExplicitlySet {
			prompt := promptui.Prompt{
				Label:     "Enter source data",
				Default:   "",
				AllowEdit: true,
			}
			sourceData, err := prompt.Run()
			if err != nil {
				return err
			}
			SourceData = sourceData
		}

		var initScript string
		if SourceData == "" {
			// Basic init container that just creates database
			initScript = fmt.Sprintf("sleep 5; while ! mysql -h $(host) -P $(port) -u $(username) -p$(password) -e \"show databases;\" 2>/dev/null; do echo -n .; sleep 2; done; mysql -h $(host) -P $(port) -u $(username) -p$(password) -e \"CREATE DATABASE IF NOT EXISTS %s;\"", DatabaseSchema)
		} else {
			// Init container that loads data from source URL
			initScript = fmt.Sprintf("sleep 5; while ! mysql -h $(host) -P $(port) -u $(username) -p$(password) -e \"show databases;\" 2>/dev/null; do echo -n .; sleep 2; done; if mysql -h $(host) -P $(port) -u $(username) -p$(password) -e \"USE %s; SET @tablename := (select table_name from information_schema.tables where table_type = 'BASE TABLE' and table_schema = '%s' limit 1); set @qry1:= concat('select * from ',@tablename,' limit 1'); prepare stmt from @qry1 ; execute stmt ;\" ; then echo \"database already exists...\"; else curl -o /tmp/%s.sql %s; mysql -h $(host) -P $(port) -u $(username) -p$(password) < /tmp/%s.sql; fi;", DatabaseSchema, DatabaseSchema, DatabaseSchema, SourceData, DatabaseSchema)
		}

		grapi["initContainers"] = []interface{}{
			map[string]interface{}{
				"name": "init-db",
				"spec": map[string]interface{}{
					"name":    "init-db",
					"image":   "mysql",
					"command": []string{"bash", "-c", initScript},
				},
			},
		}

	} else if GRASTemplate == utils.DB_FILE {

		initScript := fmt.Sprintf("if ! test -f %s; then wget -O %s %s; chmod 777 %s; fi", DBFilePath, DBFilePath, SourceData, DBFilePath)

		grapi["initContainers"] = []interface{}{
			map[string]interface{}{
				"name": "test",
				"spec": map[string]interface{}{
					"name":    "init-db",
					"image":   "busybox:1.28",
					"command": []string{"sh", "-c", initScript},
				},
			},
		}

	}

	tmpl["grapi"] = grapi
	newData, err := yaml.Marshal(tmpl)
	if err != nil {
		return err
	}
	if err := os.WriteFile(templateFileDest, newData, 0644); err != nil {
		return err
	}

	return nil
}

// substituteEnvVarsInTemplate performs environment variable substitution on the template file.
func substituteEnvVarsInTemplate(tmplFile string) error {
	data, err := os.ReadFile(tmplFile)
	if err != nil {
		return err
	}
	expanded := os.ExpandEnv(string(data))
	return os.WriteFile(tmplFile, []byte(expanded), 0644)
}

// deployTemplate uses the Helm Go SDK to install (or upgrade) the release.
func deployTemplate(tmplFile, releaseName, namespace string) error {
	// Set up Helm settings.

	utils.StartSpinner("Deploying the gras resource using the Helm\n")
	defer utils.StopSpinner()

	settings := cli.New()
	settings.SetNamespace(namespace)

	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(settings.RESTClientGetter(), namespace, os.Getenv("HELM_DRIVER"), log.Printf); err != nil {
		return fmt.Errorf("failed to initialize helm action configuration: %v", err)
	}

	// Create registry client
	registryClient, err := registry.NewClient(
		registry.ClientOptDebug(settings.Debug),
		registry.ClientOptWriter(os.Stdout),
		registry.ClientOptCredentialsFile(settings.RegistryConfig),
	)
	if err != nil {
		return fmt.Errorf("failed to create registry client: %v", err)
	}

	// OCI chart reference
	chartRef := fmt.Sprintf("oci://public.ecr.aws/%s/gras-deploy", awsRegistry)

	// Check if release already exists
	list := action.NewList(actionConfig)
	releases, err := list.Run()
	if err != nil {
		return fmt.Errorf("failed to list releases: %v", err)
	}

	for _, rel := range releases {
		if rel.Name == releaseName {
			// Delete existing release
			uninstall := action.NewUninstall(actionConfig)
			if _, err := uninstall.Run(releaseName); err != nil {
				return fmt.Errorf("failed to uninstall existing release: %v", err)
			}
			log.Printf("Existing release %q uninstalled", releaseName)
			break
		}
	}

	install := action.NewInstall(actionConfig)
	install.ReleaseName = releaseName
	install.Namespace = namespace
	install.SetRegistryClient(registryClient)

	chartPath, err := install.ChartPathOptions.LocateChart(chartRef, settings)
	if err != nil {
		return fmt.Errorf("failed to locate chart: %v", err)
	}

	chart, err := loader.Load(chartPath)
	if err != nil {
		return fmt.Errorf("failed to load chart: %v", err)
	}

	// Merge values from the template file.
	vals := map[string]interface{}{}
	if fileVals, err := os.ReadFile(tmplFile); err == nil {
		// First unmarshal into map[interface{}]interface{}
		var rawVals map[interface{}]interface{}
		if err := yaml.Unmarshal(fileVals, &rawVals); err != nil {
			log.Printf("warning: could not parse values from %s: %v", tmplFile, err)
		} else {
			// Convert to map[string]interface{} recursively
			vals = convertToStringKeysMap(rawVals)
		}
	} else {
		log.Printf("warning: could not read values from %s: %v", tmplFile, err)
	}

	rel, err := install.Run(chart, vals)
	if err != nil {
		return fmt.Errorf("failed to install helm release: %v", err)
	}

	log.Printf("Helm release %q installed in namespace %q (chart version: %s)", rel.Name, rel.Namespace, rel.Chart.Metadata.Version)
	return nil
}

// convertToStringKeysMap recursively converts a map[interface{}]interface{} to map[string]interface{}
func convertToStringKeysMap(m map[interface{}]interface{}) map[string]interface{} {
	res := make(map[string]interface{})
	for k, v := range m {
		switch v := v.(type) {
		case map[interface{}]interface{}:
			res[fmt.Sprint(k)] = convertToStringKeysMap(v)
		case []interface{}:
			res[fmt.Sprint(k)] = convertToStringKeysList(v)
		default:
			res[fmt.Sprint(k)] = v
		}
	}
	return res
}

// convertToStringKeysList recursively converts []interface{} that might contain map[interface{}]interface{}
func convertToStringKeysList(l []interface{}) []interface{} {
	res := make([]interface{}, len(l))
	for i, v := range l {
		switch v := v.(type) {
		case map[interface{}]interface{}:
			res[i] = convertToStringKeysMap(v)
		case []interface{}:
			res[i] = convertToStringKeysList(v)
		default:
			res[i] = v
		}
	}
	return res
}

func updateTemplateForInternalDB() error {

	if DatabaseSchema == "" {
		prompt := promptui.Prompt{
			Label: "Enter database schema name",
		}
		schema, err := prompt.Run()
		if err != nil {
			return err
		}
		DatabaseSchema = schema
	}

	// Handle init containers based on source data
	data, err := os.ReadFile(templateFileDest)
	if err != nil {
		return err
	}
	var tmpl map[string]interface{}
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		return err
	}
	grapi, ok := tmpl["grapi"].(map[interface{}]interface{})
	if !ok {
		grapi = make(map[interface{}]interface{})
		tmpl["grapi"] = grapi
	}

	grapi["extraSecrets"] = []string{fmt.Sprintf("%s-conn-credential", GRASName)}

	datasources, ok := grapi["datasources"].([]interface{})
	if !ok {
		datasources = make([]interface{}, 0)
	}

	datasource := map[string]interface{}{
		"name": DatabaseSchema,
		"spec": map[string]interface{}{
			"mysql": map[string]interface{}{
				"name":     DatabaseSchema,
				"host":     "$(host)",
				"port":     "$(port)",
				"user":     "$(username)",
				"password": "$(password)",
				"database": DatabaseSchema,
			},
		},
	}

	if len(datasources) == 0 {
		datasources = append(datasources, datasource)
	} else {
		datasources[0] = datasource
	}

	grapi["datasources"] = datasources

	tmpl["grapi"] = grapi
	newData, err := yaml.Marshal(tmpl)
	if err != nil {
		return err
	}
	if err := os.WriteFile(templateFileDest, newData, 0644); err != nil {
		return err
	}

	return nil
}

func updateTemplateForExternalDB() error {
	// Handle init containers based on source data
	data, err := os.ReadFile(templateFileDest)
	if err != nil {
		return err
	}
	var tmpl map[string]interface{}
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		return err
	}
	grapi, ok := tmpl["grapi"].(map[interface{}]interface{})
	if !ok {
		grapi = make(map[interface{}]interface{})
		tmpl["grapi"] = grapi
	}

	grapi["extraSecrets"] = []string{fmt.Sprintf("%s-conn-credential", GRASName)}

	datasources, ok := grapi["datasources"].([]interface{})
	if !ok {
		datasources = make([]interface{}, 0)
	}

	datasource := map[string]interface{}{
		"name": DatabaseSchema,
		"spec": map[string]interface{}{
			"mysql": map[string]interface{}{
				"name":     DatabaseSchema,
				"url":      URL,
				"host":     "$(host)",
				"port":     "$(port)",
				"user":     "$(username)",
				"password": "$(password)",
				"database": DatabaseSchema,
			},
		},
	}

	if len(datasources) == 0 {
		datasources = append(datasources, datasource)
	} else {
		datasources[0] = datasource
	}

	grapi["datasources"] = datasources

	tmpl["grapi"] = grapi
	newData, err := yaml.Marshal(tmpl)
	if err != nil {
		return err
	}
	if err := os.WriteFile(templateFileDest, newData, 0644); err != nil {
		return err
	}

	return nil
}

func updateTemplateForRestcruds() error {
	data, err := os.ReadFile(templateFileDest)
	if err != nil {
		return err
	}
	var tmpl map[string]interface{}
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		return err
	}
	grapi, ok := tmpl["grapi"].(map[interface{}]interface{})
	if !ok {
		grapi = make(map[interface{}]interface{})
		tmpl["grapi"] = grapi
	}

	if GRASTemplate == utils.DB_FILE {
		restcruds := []interface{}{
			map[string]interface{}{
				"name": "restcrud",
				"spec": map[string]interface{}{
					"datasource": "db",
				},
			},
		}
		grapi["restcruds"] = restcruds
	} else if GRASTemplate == utils.DB_MYSQL_MODEL_BASED || GRASTemplate == utils.DB_MYSQL_DISCOVERY_BASED {
		restcruds := []interface{}{
			map[string]interface{}{
				"name": DatabaseSchema,
				"spec": map[string]interface{}{
					"datasource": DatabaseSchema,
				},
			},
		}
		grapi["restcruds"] = restcruds
	}

	tmpl["grapi"] = grapi
	newData, err := yaml.Marshal(tmpl)
	if err != nil {
		return err
	}
	if err := os.WriteFile(templateFileDest, newData, 0644); err != nil {
		return err
	}

	return nil

}

func updateTemplateForDataSourceIncaseOfDbFile() error {
	data, err := os.ReadFile(templateFileDest)
	if err != nil {
		return err
	}
	var tmpl map[string]interface{}
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		return err
	}
	grapi, ok := tmpl["grapi"].(map[interface{}]interface{})
	if !ok {
		grapi = make(map[interface{}]interface{})
		tmpl["grapi"] = grapi
	}

	datasources := []interface{}{
		map[string]interface{}{
			"name": "db",
			"spec": map[string]interface{}{
				"memory": map[string]interface{}{
					"connector":    "memory",
					"name":         "db",
					"file":         DBFilePath,
					"localStorage": "db",
				},
			},
		},
	}
	grapi["datasources"] = datasources

	tmpl["grapi"] = grapi
	newData, err := yaml.Marshal(tmpl)
	if err != nil {
		return err
	}
	if err := os.WriteFile(templateFileDest, newData, 0644); err != nil {
		return err
	}

	return nil

}

func createInternalDB() error {
	// Copy the manifest file
	src := filepath.Join("files", "db.yaml")
	srcData, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read source file: %v", err)
	}
	if err := os.WriteFile(kubeblocksTemplateFileDest, srcData, 0644); err != nil {
		return fmt.Errorf("failed to write kubeblocks template file: %v", err)
	}

	// Read the template file
	data, err := os.ReadFile(kubeblocksTemplateFileDest)
	if err != nil {
		return fmt.Errorf("failed to read kubeblocks template file: %v", err)
	}

	var tmpl map[string]interface{}
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		return fmt.Errorf("failed to parse kubeblocks template YAML: %v", err)
	}

	// Update the name in metadata
	metadata := tmpl["metadata"].(map[interface{}]interface{})
	metadata["name"] = GRASName

	// Marshal back to YAML
	newData, err := yaml.Marshal(tmpl)
	if err != nil {
		return fmt.Errorf("failed to marshal updated kubeblocks template: %v", err)
	}

	// Write back to file
	if err := os.WriteFile(kubeblocksTemplateFileDest, newData, 0644); err != nil {
		return fmt.Errorf("failed to write updated kubeblocks template: %v", err)
	}
	// Read and decode the YAML file
	yamlFile, err := os.ReadFile(kubeblocksTemplateFileDest)
	if err != nil {
		return fmt.Errorf("failed to read manifest file: %v", err)
	}

	utils.InfoMessage("Checking and installing kubeblocks on cluster")
	if err := utils.InstallKubeBlocksOnCluster(restConfig); err != nil {
		utils.ErrorMessage("kubeblocks installation error: " + err.Error())
		return err
	}
	utils.InfoMessage("kubeblocks installed.")

	// Create dynamic client to handle custom resources
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %v", err)
	}

	// Define the GVR for KubeBlocks Cluster
	clusterGVR := schema.GroupVersionResource{
		Group:    "apps.kubeblocks.io",
		Version:  "v1alpha1",
		Resource: "clusters",
	}

	// First unmarshal into a map[interface{}]interface{}
	var tempObj map[interface{}]interface{}
	if err := yaml.Unmarshal(yamlFile, &tempObj); err != nil {
		return fmt.Errorf("failed to parse YAML: %v", err)
	}

	// Convert to map[string]interface{} recursively
	obj := convertToStringKeysMap(tempObj)

	unstructuredObj := &unstructured.Unstructured{Object: obj}

	// Try to create the cluster first
	_, err = dynamicClient.Resource(clusterGVR).Namespace(KubeNS).Create(
		context.Background(),
		unstructuredObj,
		v1.CreateOptions{},
	)
	if err != nil {
		// If resource already exists, get it first to obtain resourceVersion
		if k8serrors.IsAlreadyExists(err) {
			utils.InfoMessage(fmt.Sprintf("Cluster %s already exists, updating it", GRASName))

			// Get existing resource
			existing, err := dynamicClient.Resource(clusterGVR).Namespace(KubeNS).Get(
				context.Background(),
				GRASName,
				v1.GetOptions{},
			)
			if err != nil {
				return fmt.Errorf("failed to get existing cluster: %v", err)
			}

			// Set resourceVersion from existing to new object
			unstructuredObj.SetResourceVersion(existing.GetResourceVersion())

			// Update the resource
			_, err = dynamicClient.Resource(clusterGVR).Namespace(KubeNS).Update(
				context.Background(),
				unstructuredObj,
				v1.UpdateOptions{},
			)
			if err != nil {
				return fmt.Errorf("failed to update cluster: %v", err)
			}
			utils.InfoMessage(fmt.Sprintf("Cluster %s updated successfully", GRASName))
		} else {
			return fmt.Errorf("failed to create cluster: %v", err)
		}
	}

	return nil
}

func prepareNamespaceForGrasInstallation() error {
	// If namespace is empty, prompt user to choose or create
	utils.InfoMessage(fmt.Sprintf("Preparing namespace for GRAS installation in %s", KubeNS))
	if KubeNS == "" {
		// Present options to user
		options := []string{"Choose from existing namespaces", "Create new namespace"}
		result, err := utils.PromptSelect("Please select an option", options)
		if err != nil {
			return err
		}

		if result == "Choose from existing namespaces" {
			// List all namespaces
			namespaces, err := clientset.CoreV1().Namespaces().List(context.Background(), v1.ListOptions{})
			if err != nil {
				return fmt.Errorf("failed to list namespaces: %v", err)
			}

			// Extract namespace names
			var namespaceNames []string
			for _, ns := range namespaces.Items {
				namespaceNames = append(namespaceNames, ns.Name)
			}

			// Prompt user to select namespace
			KubeNS, err = utils.PromptSelect("Select namespace", namespaceNames)
			if err != nil {
				return err
			}

		} else {
			// Prompt for new namespace name
			newName, err := utils.PromptInput("Enter new namespace name", "Namespace name cannot be empty", utils.NonEmptyValueRegex)
			if err != nil {
				return err
			}
			KubeNS = newName
		}
	}

	// Check if namespace exists
	_, err := clientset.CoreV1().Namespaces().Get(context.Background(), KubeNS, v1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Create namespace if it doesn't exist
			ns := &corev1.Namespace{
				ObjectMeta: v1.ObjectMeta{
					Name: KubeNS,
				},
			}
			_, err = clientset.CoreV1().Namespaces().Create(context.Background(), ns, v1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create namespace: %v", err)
			}
			utils.InfoMessage(fmt.Sprintf("Created namespace: %s", KubeNS))
		} else {
			return fmt.Errorf("error checking namespace: %v", err)
		}
	}

	return nil
}
