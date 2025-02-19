/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package resource

import (
	"fmt"
	"os"
	"time"

	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

// renderCmd represents the render command
var RenderCmd = &cobra.Command{
	Use:   "render",
	Short: "Render a GrappleApplicationSet resource without deploying it",
	Long:  `Render command creates a GrappleApplicationSet resource YAML file without deploying it to your cluster.`,
	RunE:  runRender,
}

func init() {
	// Setup cobra flags (bind these to the global variables) - same as deploy
	RenderCmd.Flags().StringVar(&GRASName, "gras-name", "", "Name of the GRAS resource")
	RenderCmd.Flags().StringVar(&GRASTemplate, "gras-template", "", "Template type to use")
	RenderCmd.Flags().StringVar(&DBType, "db-type", "", "Database type (internal or external)")
	RenderCmd.Flags().StringVar(&ModelsInput, "models", "", "Models input (if not interactive)")
	RenderCmd.Flags().StringVar(&RelationsInput, "relations", "", "Relations input (if not interactive)")
	RenderCmd.Flags().StringVar(&DatasourcesInput, "datasources", "", "Datasources input (if not interactive)")
	RenderCmd.Flags().StringVar(&DiscoveriesInput, "discoveries", "", "Discoveries input (if not interactive)")
	RenderCmd.Flags().StringVar(&DatabaseSchema, "database-schema", "", "Database schema")
	RenderCmd.Flags().BoolVar(&AutoDiscovery, "auto-discovery", false, "Auto discovery flag")
	RenderCmd.Flags().StringVar(&SourceData, "source-data", "", "Data source URL")
	RenderCmd.Flags().BoolVar(&EnableGRUIM, "enable-gruim", false, "Enables GRUIM")
	RenderCmd.Flags().StringVar(&DBFilePath, "db-file-path", "", "Path to DB file")
	RenderCmd.Flags().StringVar(&KubeContext, "kube-context", "", "Kubernetes context to use")
	RenderCmd.Flags().StringVar(&KubeNS, "namespace", "", "Kubernetes namespace to use")
}

// runRender is the main function for the render command
func runRender(cmd *cobra.Command, args []string) error {
	// Set isRender to true before calling deploy logic
	isRender = true

	// Run deploy logic to generate template.yaml
	if err := runDeploy(cmd, args); err != nil {
		return err
	}

	// Read the generated template.yaml
	data, err := os.ReadFile("/tmp/template.yaml")
	if err != nil {
		utils.ErrorMessage(fmt.Sprintf("failed to read template file: %v", err))
		return err
	}

	var tmpl map[string]interface{}
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		utils.ErrorMessage(fmt.Sprintf("failed to parse template yaml: %v", err))
		return err
	}

	// Create GRAS manifest
	gras := map[string]interface{}{
		"apiVersion": "grsf.grpl.io/v1alpha1",
		"kind":       "GrappleApplicationSet",
		"metadata": map[string]interface{}{
			"name":      GRASName,
			"namespace": KubeNS,
		},
		"spec": map[string]interface{}{
			"name": GRASName,
			"grapis": []interface{}{
				map[string]interface{}{
					"name": GRASName,
					"spec": tmpl["grapi"],
				},
			},
		},
	}

	// Add gruims if enabled
	if EnableGRUIM {
		gras["spec"].(map[string]interface{})["gruims"] = []interface{}{
			map[string]interface{}{
				"name": GRASName,
				"spec": tmpl["gruim"],
			},
		}
	}

	// Generate output filename with current timestamp
	timestamp := time.Now().Format("2006-01-02-15-04")
	outFile := fmt.Sprintf("/tmp/gras-resource-%s.yaml", timestamp)

	// Marshal and write the GRAS manifest
	output, err := yaml.Marshal(gras)
	if err != nil {
		return fmt.Errorf("failed to marshal gras manifest: %v", err)
	}

	if err := os.WriteFile(outFile, output, 0644); err != nil {
		return fmt.Errorf("failed to write gras manifest: %v", err)
	}

	utils.SuccessMessage(fmt.Sprintf("GRAS manifest written to %s", outFile))
	return nil
}
