// deploy.go
// Copyright © 2025 NAME HERE <EMAIL ADDRESS>
package resource

import (
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

	// Helm Go SDK packages
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/registry"
	// Kubernetes client libraries
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
	DeployCmd.Flags().StringVar(&GRASName, "name", "", "Name of the GRAS resource")
	DeployCmd.Flags().StringVar(&GRASTemplate, "template", "", "Template type to use")
	DeployCmd.Flags().StringVar(&DBType, "db-type", "", "Database type (internal or external)")
	DeployCmd.Flags().StringVar(&ModelsInput, "models", "", "Models input (if not interactive)")
	DeployCmd.Flags().StringVar(&RelationsInput, "relations", "", "Relations input (if not interactive)")
	DeployCmd.Flags().StringVar(&DatasourcesInput, "datasources", "", "Datasources input (if not interactive)")
	DeployCmd.Flags().StringVar(&DiscoveriesInput, "discoveries", "", "Discoveries input (if not interactive)")
	DeployCmd.Flags().StringVar(&DatabaseSchema, "database-schema", "", "Database schema")
	DeployCmd.Flags().BoolVar(&AutoDiscovery, "auto-discovery", false, "Auto discovery flag")
	DeployCmd.Flags().StringVar(&SourceData, "source-data", "", "Data source URL")
	DeployCmd.Flags().BoolVar(&EnableGRUIM, "enable-gruim", false, "Enable GRUIM")
	DeployCmd.Flags().StringVar(&DBFilePath, "db-file-path", "", "Path to DB file")
	DeployCmd.Flags().StringVar(&KubeContext, "kube-context", "", "Kubernetes context to use")
	DeployCmd.Flags().StringVar(&KubeNS, "namespace", "grpl-system", "Kubernetes namespace to use")
}

// runDeploy is the main function for the deploy command.
func runDeploy(cmd *cobra.Command, args []string) error {

	logFile, _, logOnCliAndFileStart := utils.GetLogWriters("/tmp/grpl_resource_deploy.log")

	var err error

	defer func() {
		logFile.Sync() // Ensure logs are flushed before closing
		logFile.Close()
		if err != nil {
			utils.ErrorMessage("Failed to connect to cluster, please run cat /tmp/grpl_civo_connect.log for more details")
		}
	}()

	logOnCliAndFileStart()

	// // 2. Validate connectivity to the cluster and select the proper context.
	// restConfig, clientset, err := utils.GetKubernetesConfig()
	// if err != nil {
	// 	utils.ErrorMessage(fmt.Sprintf("Failed to connect to cluster: %v", err))
	// 	return err
	// }
	// 3. Copy a base template file (from GRPL_WORKDIR) to our working file.
	if err := prepareTemplateFile(); err != nil {
		return err
	}

	// 4. Process inputs – if models/datasources/discoveries/relations were passed via CLI, transform them.
	// Otherwise, invoke interactive functions.
	if ModelsInput != "" {
		if err := transformModelInputToYAML(ModelsInput, templateFileDest); err != nil {
			return err
		}
	} else {
		if err := takeModelInputFromCLI(templateFileDest); err != nil {
			return err
		}
	}

	if DatasourcesInput != "" {
		if err := transformDatasourcesInputToYAML(DatasourcesInput, templateFileDest); err != nil {
			return err
		}
	} else if GRASTemplate == DB_MYSQL_MODEL_BASED || GRASTemplate == DB_MYSQL_DISCOVERY_BASED {
		if err := takeDatasourceInputFromCLI("mysql", templateFileDest); err != nil {
			return err
		}
	}

	if GRASTemplate == DB_MYSQL_DISCOVERY_BASED {
		if DiscoveriesInput != "" {
			if err := transformDiscoveriesInputToYAML(DiscoveriesInput, templateFileDest); err != nil {
				return err
			}
		} else {
			if err := takeDiscoveryInputFromCLI(templateFileDest); err != nil {
				return err
			}
		}
	}

	if RelationsInput != "" {
		if err := transformRelationInputToYAML(RelationsInput, templateFileDest); err != nil {
			return err
		}
	} else {
		if err := takeRelationInputFromCLI(templateFileDest); err != nil {
			return err
		}
	}

	// 5. Ask for GRUIM enablement (interactive or by flag)
	if err := askGRUIMEnablement(templateFileDest); err != nil {
		return err
	}

	// 6. If using the "DB_FILE" template then ask for DB file path.
	if GRASTemplate == DB_FILE {
		if err := takeDBFilePath(); err != nil {
			return err
		}
		// (You can update your YAML template with the DB file path here.)
	}

	// Handle database schema and init containers
	if GRASTemplate == DB_MYSQL_MODEL_BASED || GRASTemplate == DB_MYSQL_DISCOVERY_BASED {
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

		// Prompt for source data if not provided
		if SourceData == "" {
			prompt := promptui.Prompt{
				Label:     "Enter SOURCE_DATA",
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
				"spec": map[string]interface{}{
					"image":   "mysql",
					"command": initScript,
				},
			},
		}

		tmpl["grapi"] = grapi
		newData, err := yaml.Marshal(tmpl)
		if err != nil {
			return err
		}
		if err := os.WriteFile(templateFileDest, newData, 0644); err != nil {
			return err
		}
	}

	// 7. Substitute environment variables in the template (using os.ExpandEnv).
	if err := substituteEnvVarsInTemplate(templateFileDest); err != nil {
		return err
	}

	// 8. Finally, deploy the template using the Helm Go SDK.
	if err := deployTemplate(templateFileDest, GRASName, KubeNS); err != nil {
		return err
	}

	// 9. Optionally, clean up the temporary file.
	_ = os.Remove(templateFileDest)

	fmt.Println("Resource deployed successfully!")
	return nil
}

// prepareTemplateFile copies a base template file from GRPL_WORKDIR into our working file.
func prepareTemplateFile() error {
	var src string
	if GRASTemplate == DB_FILE {
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

func transformDatasourcesInputToYAML(ds string, tmplFile string) error {
	parts := strings.Split(ds, "|")
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
	var dsList []interface{}
	if dl, ok := grapi["datasources"].([]interface{}); ok {
		dsList = dl
	}
	for _, part := range parts {
		part = strings.ReplaceAll(part, "'", "\"")
		subParts := strings.SplitN(part, ":", 2)
		if len(subParts) != 2 {
			continue
		}
		dsName := subParts[0]
		propsJSON := subParts[1]
		var props map[string]interface{}
		if err := json.Unmarshal([]byte(propsJSON), &props); err != nil {
			log.Printf("failed to unmarshal datasource properties: %v", err)
			continue
		}
		dsEntry := map[string]interface{}{
			"name": dsName,
			"spec": props,
		}
		dsList = append(dsList, dsEntry)
	}
	grapi["datasources"] = dsList
	newData, err := yaml.Marshal(tmpl)
	if err != nil {
		return err
	}
	return os.WriteFile(tmplFile, newData, 0644)
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

func takeDatasourceInputFromCLI(dsType string, tmplFile string) error {
	dsName, err := utils.PromptInput("Enter datasource name", utils.DefaultValue, utils.EmptyValueRegex)
	if err != nil {
		return err
	}

	host, err := utils.PromptInput("Enter host", utils.DefaultValue, utils.EmptyValueRegex)
	if err != nil {
		return err
	}

	port, err := utils.PromptInput("Enter port", utils.DefaultValue, utils.EmptyValueRegex)
	if err != nil {
		return err
	}

	user, err := utils.PromptInput("Enter user", utils.DefaultValue, utils.EmptyValueRegex)
	if err != nil {
		return err
	}

	password, err := utils.PromptPassword("Enter password")
	if err != nil {
		return err
	}

	url, err := utils.PromptInput("Enter datasource URL (optional)", utils.DefaultValue, utils.EmptyValueRegex)
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

	var dsList []interface{}
	if dl, ok := grapi["datasources"].([]interface{}); ok {
		dsList = dl
	}

	dsEntry := map[string]interface{}{
		"name": dsName,
		"spec": map[string]interface{}{
			"type":     dsType,
			"host":     host,
			"port":     port,
			"user":     user,
			"password": password,
			"url":      url,
		},
	}
	dsList = append(dsList, dsEntry)
	grapi["datasources"] = dsList

	newData, err := yaml.Marshal(tmpl)
	if err != nil {
		return err
	}
	return os.WriteFile(tmplFile, newData, 0644)
}

func takeDiscoveryInputFromCLI(tmplFile string) error {
	prompt := promptui.Select{
		Label: "Do you want discovery to be created automatically?",
		Items: []string{"Yes", "No"},
	}
	_, choice, err := prompt.Run()
	if err != nil {
		return err
	}
	auto := (choice == "Yes")
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
		prompt := promptui.Prompt{
			Label: "Enter discovery name",
		}
		discoveryName, err := prompt.Run()
		if err != nil {
			return err
		}
		discEntry = map[string]interface{}{
			"name": discoveryName,
			"spec": map[string]interface{}{
				"all":        false,
				"optionalId": false,
				"schema":     DatabaseSchema,
				"dataSource": DatabaseSchema,
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
func askGRUIMEnablement(tmplFile string) error {
	prompt := promptui.Select{
		Label: "Do you want to enable GRUIM?",
		Items: []string{"Yes", "No"},
	}
	_, choice, err := prompt.Run()
	if err != nil {
		return err
	}
	enable := (choice == "Yes")
	data, err := os.ReadFile(tmplFile)
	if err != nil {
		return err
	}
	var tmpl map[string]interface{}
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		return err
	}
	if !enable {
		if grapi, ok := tmpl["grapi"].(map[interface{}]interface{}); ok {
			delete(grapi, "gruims")
		}
	}
	newData, err := yaml.Marshal(tmpl)
	if err != nil {
		return err
	}
	return os.WriteFile(tmplFile, newData, 0644)
}

// takeDBFilePath prompts the user for a file path for the DB file.
func takeDBFilePath() error {
	prompt := promptui.Prompt{
		Label:   "Enter DB file path",
		Default: "/data/db.json",
	}
	path, err := prompt.Run()
	if err != nil {
		return err
	}
	DBFilePath = path
	os.Setenv("db_file", DBFilePath)
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
		var parsedVals map[string]interface{}
		if err := yaml.Unmarshal(fileVals, &parsedVals); err == nil {
			vals = parsedVals
		} else {
			log.Printf("warning: could not parse values from %s: %v", tmplFile, err)
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
