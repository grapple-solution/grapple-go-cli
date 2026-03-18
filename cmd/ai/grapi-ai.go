package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
)

type GrapiConfig struct {
	ServerURL string `json:"server_url"`
}

type GrapiClient struct {
	ServerURL string
	Client    *http.Client
	Meta      map[string]map[string]string // Maps fn_name -> {method, path}
}

func NewGrapiClient(serverURL string) *GrapiClient {
	return &GrapiClient{
		ServerURL: serverURL,
		Client:    &http.Client{Timeout: 60 * time.Second},
		Meta:      make(map[string]map[string]string),
	}
}

func (g *GrapiClient) GetAvailablePrompts() ([]map[string]interface{}, error) {
	// Local Grapi doesn't have a /mcp/prompts endpoint yet, return empty
	return []map[string]interface{}{}, nil
}

func (g *GrapiClient) GetAvailableTools() ([]map[string]interface{}, error) {
	resp, err := g.Client.Get(g.ServerURL + "/openapi.json")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch openapi.json: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch openapi.json: status %d", resp.StatusCode)
	}

	var spec map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&spec); err != nil {
		return nil, fmt.Errorf("failed to decode openapi.json: %v", err)
	}

	tools, meta := g.buildTools(spec)
	g.Meta = meta
	return tools, nil
}

func (g *GrapiClient) buildTools(spec map[string]interface{}) ([]map[string]interface{}, map[string]map[string]string) {
	tools := []map[string]interface{}{}
	meta := make(map[string]map[string]string)

	paths, ok := spec["paths"].(map[string]interface{})
	if !ok {
		return tools, meta
	}

	for path, pathItem := range paths {
		ops, ok := pathItem.(map[string]interface{})
		if !ok {
			continue
		}

		for method, opRaw := range ops {
			if method == "parameters" {
				continue
			}
			op, ok := opRaw.(map[string]interface{})
			if !ok {
				continue
			}

			opID, _ := op["operationId"].(string)
			if opID == "" {
				opID = fmt.Sprintf("%s_%s", method, path)
			}
			fnName := g.safeName(opID)
			summary, _ := op["summary"].(string)
			if summary == "" {
				summary, _ = op["description"].(string)
			}
			if summary == "" {
				summary = fmt.Sprintf("%s %s", strings.ToUpper(method), path)
			}
			description := fmt.Sprintf("[%s %s] %s", strings.ToUpper(method), path, summary)

			props := make(map[string]interface{})
			required := []string{}

			params, _ := op["parameters"].([]interface{})
			for _, pRaw := range params {
				p, ok := pRaw.(map[string]interface{})
				if !ok {
					continue
				}
				pname, _ := p["name"].(string)
				pschema, _ := p["schema"].(map[string]interface{})
				if pschema == nil {
					if content, ok := p["content"].(map[string]interface{}); ok {
						if jsonContent, ok := content["application/json"].(map[string]interface{}); ok {
							pschema, _ = jsonContent["schema"].(map[string]interface{})
						}
					}
				}

				prop := g.convertSchema(pschema)
				if in, ok := p["in"].(string); ok {
					prop["description"] = fmt.Sprintf("(%s) %s", in, pname)
				}
				props[pname] = prop
				if req, _ := p["required"].(bool); req {
					required = append(required, pname)
				}
			}

			reqBody, _ := op["requestBody"].(map[string]interface{})
			if reqBody != nil {
				if content, ok := reqBody["content"].(map[string]interface{}); ok {
					if jsonContent, ok := content["application/json"].(map[string]interface{}); ok {
						bodySchema, _ := jsonContent["schema"].(map[string]interface{})
						bodyProp := map[string]interface{}{
							"type":        "object",
							"description": "JSON request body with relevant fields",
						}
						if propsRaw, ok := bodySchema["properties"].(map[string]interface{}); ok {
							bodyProp["properties"] = g.convertProperties(propsRaw)
						}
						props["body"] = bodyProp
						if req, _ := reqBody["required"].(bool); req {
							required = append(required, "body")
						}
					}
				}
			}

			inputSchema := map[string]interface{}{
				"type":       "object",
				"properties": props,
			}
			if len(required) > 0 {
				inputSchema["required"] = required
			}

			tools = append(tools, map[string]interface{}{
				"name":        fnName,
				"description": description,
				"inputSchema": inputSchema,
			})
			meta[fnName] = map[string]string{
				"method": method,
				"path":   path,
			}
		}
	}

	return tools, meta
}

func (g *GrapiClient) convertProperties(props map[string]interface{}) map[string]interface{} {
	res := make(map[string]interface{})
	for k, v := range props {
		if schema, ok := v.(map[string]interface{}); ok {
			res[k] = g.convertSchema(schema)
		}
	}
	return res
}

func (g *GrapiClient) convertSchema(openapiSchema map[string]interface{}) map[string]interface{} {
	if openapiSchema == nil {
		return map[string]interface{}{"type": "string"}
	}

	raw, _ := openapiSchema["type"].(string)
	typeMap := map[string]string{
		"integer": "integer",
		"number":  "number",
		"boolean": "boolean",
		"array":   "array",
		"object":  "object",
		"string":  "string",
	}

	res := map[string]interface{}{
		"type": typeMap[raw],
	}
	if res["type"] == "" {
		res["type"] = "string"
	}

	if desc, ok := openapiSchema["description"].(string); ok {
		res["description"] = desc
	}
	if enum, ok := openapiSchema["enum"].([]interface{}); ok {
		enums := []string{}
		for _, e := range enum {
			enums = append(enums, fmt.Sprintf("%v", e))
		}
		res["enum"] = enums
	}

	if raw == "object" {
		if props, ok := openapiSchema["properties"].(map[string]interface{}); ok {
			res["properties"] = g.convertProperties(props)
		}
	}

	if raw == "array" {
		if items, ok := openapiSchema["items"].(map[string]interface{}); ok {
			res["items"] = g.convertSchema(items)
		}
	}

	return res
}

func (g *GrapiClient) safeName(opID string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9_]`)
	name := re.ReplaceAllString(opID, "_")
	re2 := regexp.MustCompile(`_+`)
	name = re2.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")

	if name != "" && name[0] >= '0' && name[0] <= '9' {
		return "fn_" + name
	}
	if name == "" {
		return "unknown"
	}
	return name
}

func (g *GrapiClient) CallTool(fnName string, args map[string]interface{}) (string, error) {
	info, ok := g.Meta[fnName]
	if !ok {
		return "", fmt.Errorf("unknown function: %s", fnName)
	}

	method := strings.ToUpper(info["method"])
	pathTpl := info["path"]

	body := args["body"]
	delete(args, "body")

	url := strings.TrimSuffix(g.ServerURL, "/") + pathTpl
	query := make(map[string]string)

	for k, v := range args {
		placeholder := fmt.Sprintf("{%s}", k)
		if strings.Contains(url, placeholder) {
			url = strings.ReplaceAll(url, placeholder, fmt.Sprintf("%v", v))
		} else {
			if isComplex(v) {
				b, _ := json.Marshal(v)
				query[k] = string(b)
			} else {
				query[k] = fmt.Sprintf("%v", v)
			}
		}
	}

	// HACK: Fix for @loopback/rest-crud bug where count() throws 500 if filter is missing
	if strings.HasSuffix(pathTpl, "/count") && query["filter"] == "" && query["where"] == "" {
		query["filter"] = "{}"
	}

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return "", err
	}

	if body != nil {
		b, _ := json.Marshal(body)
		req.Body = io.NopCloser(bytes.NewBuffer(b))
		req.Header.Set("Content-Type", "application/json")
	}

	q := req.URL.Query()
	for k, v := range query {
		q.Add(k, v)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := g.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	return string(bodyBytes), nil
}

func isComplex(v interface{}) bool {
	switch v.(type) {
	case map[string]interface{}, []interface{}:
		return true
	}
	return false
}

func saveGrapiConfig(config GrapiConfig) error {
	configDir, err := getConfigDir()
	if err != nil {
		return err
	}
	configFile := filepath.Join(configDir, "grapi-config.json")
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configFile, data, 0600)
}

func loadGrapiConfig() (*GrapiConfig, error) {
	configDir, err := getConfigDir()
	if err != nil {
		return nil, err
	}
	configFile := filepath.Join(configDir, "grapi-config.json")
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}
	var config GrapiConfig
	err = json.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}
	return &config, nil
}

var GrapiAiCmd = &cobra.Command{
	Use:   "grapi",
	Short: "AI assistant for Grapi REST API",
	Run: func(cmd *cobra.Command, args []string) {
		serverURL, _ := cmd.Flags().GetString("url")

		if serverURL == "" {
			config, err := loadGrapiConfig()
			if err == nil && config.ServerURL != "" {
				serverURL = config.ServerURL
				utils.InfoMessage(fmt.Sprintf("Using saved Grapi server URL: %s", serverURL))
				utils.InfoMessage("To change it, use the --url flag.")
			} else {
				serverURL = "http://localhost:3333"
				utils.InfoMessage(fmt.Sprintf("No Grapi server URL found. Using default: %s", serverURL))
			}
		} else {
			err := saveGrapiConfig(GrapiConfig{ServerURL: serverURL})
			if err != nil {
				utils.ErrorMessage(fmt.Sprintf("Failed to save Grapi config: %v", err))
			} else {
				utils.SuccessMessage(fmt.Sprintf("Saved Grapi server URL: %s", serverURL))
			}
		}

		provider, _ := cmd.Flags().GetString("provider")
		aiConfig, err := setupAIProvider(provider)
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Error setting up AI provider: %v", err))
			return
		}

		model, _ := cmd.Flags().GetString("model")
		if model != "" {
			aiConfig.Model = model
		}

		grapiClient := NewGrapiClient(serverURL)

		aiSession, err := createAISession(aiConfig, grapiClient)
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Error creating AI session: %v", err))
			return
		}

		utils.SuccessMessage(fmt.Sprintf("Grapi AI assistant ready! Using %s (%s)", aiConfig.Provider, aiSession.GetModel()))
		utils.InfoMessage("Type 'exit' or 'quit' to end the session")
		fmt.Println("=" + strings.Repeat("=", 50))
		fmt.Println()

		renderer, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(80),
		)
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Error initializing renderer: %v", err))
		}

		for {
			prompt, err := utils.PromptInput("You", "", "^.+$")
			if err != nil {
				utils.ErrorMessage(fmt.Sprintf("Error reading input: %v", err))
				continue
			}

			if strings.ToLower(strings.TrimSpace(prompt)) == "exit" ||
				strings.ToLower(strings.TrimSpace(prompt)) == "quit" {
				utils.InfoMessage("Goodbye!")
				break
			}

			if strings.TrimSpace(prompt) == "" {
				continue
			}

			utils.InfoMessage(fmt.Sprintf("%s:", strings.Title(aiConfig.Provider)))
			fmt.Println(strings.Repeat("-", 50))

			response, err := aiSession.Chat(prompt)
			if err != nil {
				utils.ErrorMessage(fmt.Sprintf("Error from AI: %v", err))
				fmt.Println()
				continue
			}

			rendered, err := renderer.Render(response)
			if err != nil {
				utils.ErrorMessage(fmt.Sprintf("Error rendering response: %v", err))
				continue
			}
			fmt.Println(rendered)
		}
	},
}

func init() {
	GrapiAiCmd.Flags().String("url", "", "Grapi server URL (e.g. http://localhost:3333)")
	GrapiAiCmd.Flags().StringP("model", "m", "", "AI model to use (overrides defaults and env vars)")
	GrapiAiCmd.Flags().StringP("provider", "p", "", "AI provider to use (anthropic, openai, gemini)")
}
