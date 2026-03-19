package ai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	Token     string
}

func NewGrapiClient(serverURL string, token string) *GrapiClient {
	return &GrapiClient{
		ServerURL: serverURL,
		Client:    &http.Client{Timeout: 60 * time.Second},
		Token:     token,
	}
}

func (g *GrapiClient) mcpRequest(method string, params interface{}) (map[string]interface{}, error) {
	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
	}
	if params != nil {
		payload["params"] = params
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := strings.TrimSuffix(g.ServerURL, "/") + "/mcp"
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}

	resp, err := g.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("MCP request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	contentType := resp.Header.Get("Content-Type")

	if strings.Contains(contentType, "text/event-stream") {
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "data:") {
				jsonData := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				var temp map[string]interface{}
				if err := json.Unmarshal([]byte(jsonData), &temp); err == nil {
					if _, hasResult := temp["result"]; hasResult {
						result = temp
						break
					}
					if _, hasError := temp["error"]; hasError {
						result = temp
						break
					}
				}
			}
		}
		if result == nil {
			return nil, fmt.Errorf("failed to parse valid JSON-RPC out of event-stream")
		}
	} else {
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, err
		}
	}

	if errObj, ok := result["error"].(map[string]interface{}); ok && errObj != nil {
		return nil, fmt.Errorf("MCP error: %v", errObj)
	}

	resultObj, ok := result["result"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid MCP response format")
	}

	return resultObj, nil
}

func (g *GrapiClient) GetAvailablePrompts() ([]map[string]interface{}, error) {
	res, err := g.mcpRequest("prompts/list", nil)
	if err != nil {
		// Fallback to empty if not implemented
		return []map[string]interface{}{}, nil
	}

	promptsIface, ok := res["prompts"].([]interface{})
	if !ok {
		return []map[string]interface{}{}, nil
	}

	prompts := []map[string]interface{}{}
	for _, p := range promptsIface {
		if promptMap, ok := p.(map[string]interface{}); ok {
			prompts = append(prompts, promptMap)
		}
	}
	return prompts, nil
}

func (g *GrapiClient) GetAvailableTools() ([]map[string]interface{}, error) {
	var allTools []map[string]interface{}
	cursor := ""

	for {
		params := map[string]interface{}{}
		if cursor != "" {
			params["cursor"] = cursor
		}

		res, err := g.mcpRequest("tools/list", params)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch tools: %v", err)
		}

		toolsIface, ok := res["tools"].([]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid MCP tools response format")
		}

		for _, t := range toolsIface {
			if toolMap, ok := t.(map[string]interface{}); ok {
				if inputSchema, ok := toolMap["inputSchema"].(map[string]interface{}); ok {
					sanitizeSchema(inputSchema)
				}
				allTools = append(allTools, toolMap)
			}
		}

		next, ok := res["nextCursor"].(string)
		if !ok || next == "" {
			break
		}
		cursor = next
	}

	return allTools, nil
}

func sanitizeSchema(schema map[string]interface{}) {
	if schema == nil {
		return
	}
	delete(schema, "$schema")
	delete(schema, "$id")
	for _, v := range schema {
		if subMap, ok := v.(map[string]interface{}); ok {
			sanitizeSchema(subMap)
		} else if subArr, ok := v.([]interface{}); ok {
			for _, item := range subArr {
				if itemMap, ok := item.(map[string]interface{}); ok {
					sanitizeSchema(itemMap)
				}
			}
		}
	}
}

func (g *GrapiClient) CallTool(fnName string, args map[string]interface{}) (string, error) {
	params := map[string]interface{}{
		"name":      fnName,
		"arguments": args,
	}

	res, err := g.mcpRequest("tools/call", params)
	if err != nil {
		return "", err
	}

	contentIface, ok := res["content"].([]interface{})
	if !ok || len(contentIface) == 0 {
		return "", fmt.Errorf("no content in tool response")
	}

	var resultParts []string

	for _, item := range contentIface {
		part, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		switch part["type"] {
		case "text":
			if text, _ := part["text"].(string); text != "" {
				resultParts = append(resultParts, text)
			}
		case "resource_link":
			if uri, _ := part["uri"].(string); uri != "" {
				desc, _ := part["description"].(string)
				resultParts = append(resultParts, fmt.Sprintf("Resource Link: %s (%s)", uri, desc))
			}
		}
	}

	// Check for tool execution errors (MCP spec 2025-11-25)
	if isError, _ := res["isError"].(bool); isError {
		return "", fmt.Errorf("tool error: %s", strings.Join(resultParts, "\n"))
	}

	if len(resultParts) == 0 {
		// Fallback: use structuredContent if text is missing
		if sc, ok := res["structuredContent"]; ok {
			b, _ := json.MarshalIndent(sc, "", "  ")
			return string(b), nil
		}
		// Final fallback: dump response data
		b, _ := json.MarshalIndent(res, "", "  ")
		return string(b), nil
	}

	return strings.Join(resultParts, "\n"), nil
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

		token, _ := cmd.Flags().GetString("token")
		grapiClient := NewGrapiClient(serverURL, token)

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
	GrapiAiCmd.Flags().StringP("token", "t", "", "Auth token for MCP endpoint if required")
}
