package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
)

const (
	MCPServerURL = "https://7de4tnscxa.execute-api.eu-central-1.amazonaws.com/grapple-ai-mcp-server"
)

type RemoteMCPClient struct {
	ServerURL string
	Client    *http.Client
}

func NewRemoteMCPClient(serverURL string) *RemoteMCPClient {
	return &RemoteMCPClient{
		ServerURL: serverURL,
		Client:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Add GetAvailablePrompts to RemoteMCPClient
func (m *RemoteMCPClient) GetAvailablePrompts() ([]map[string]interface{}, error) {
	eventPayload := map[string]interface{}{
		"httpMethod": "GET",
		"path":       "/mcp/prompts",
		"headers": map[string]interface{}{
			"Content-Type": "application/json",
		},
		"queryStringParameters": nil,
	}

	eventBytes, err := json.Marshal(eventPayload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", m.ServerURL, bytes.NewBuffer(eventBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := m.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get prompts: %s", string(body))
	}

	var result struct {
		Prompts []map[string]interface{} `json:"prompts"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Prompts, nil
}

func (m *RemoteMCPClient) GetAvailableTools() ([]map[string]interface{}, error) {
	eventPayload := map[string]interface{}{
		"httpMethod": "GET",
		"path":       "/mcp/tools",
		"headers": map[string]interface{}{
			"Content-Type": "application/json",
		},
		"queryStringParameters": nil,
	}

	eventBytes, err := json.Marshal(eventPayload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", m.ServerURL, bytes.NewBuffer(eventBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := m.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get tools: %s", string(body))
	}

	var result struct {
		Tools []map[string]interface{} `json:"tools"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Tools, nil
}

func (m *RemoteMCPClient) CallTool(toolName string, arguments map[string]interface{}) (string, error) {
	bodyPayload := map[string]interface{}{
		"name":      toolName,
		"arguments": arguments,
	}
	bodyBytes, err := json.Marshal(bodyPayload)
	if err != nil {
		return "", err
	}

	eventPayload := map[string]interface{}{
		"httpMethod": "POST",
		"path":       "/mcp/tools/call",
		"headers": map[string]interface{}{
			"Content-Type": "application/json",
		},
		"body":                  string(bodyBytes),
		"queryStringParameters": nil,
	}

	eventBytes, err := json.Marshal(eventPayload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", m.ServerURL, bytes.NewBuffer(eventBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := m.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("tool call failed: %s", string(body))
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Content) > 0 {
		return result.Content[0].Text, nil
	}

	return "", fmt.Errorf("no content in tool response")
}

var AiCmd = &cobra.Command{
	Use:   "ai",
	Short: "AI-powered assistant for Grapple CRDs",
	Long: `Get AI assistance for creating and understanding Grapple Custom Resource Definitions (CRDs).
The AI assistant can help you:
- Create new CRDs for your applications
- Understand existing CRD specifications
- Troubleshoot configuration issues
- Generate complete application manifests`,
	Run: func(cmd *cobra.Command, args []string) {
		config, err := setupAIProvider()
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Error setting up AI provider: %v", err))
			return
		}

		mcpClient := NewRemoteMCPClient(MCPServerURL)

		aiSession, err := createAISession(config, mcpClient)
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Error creating AI session: %v", err))
			return
		}

		utils.SuccessMessage(fmt.Sprintf("AI assistant ready! Using %s (%s)", config.Provider, aiSession.GetModel()))
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

			utils.InfoMessage(fmt.Sprintf("%s:", strings.Title(config.Provider)))
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

			// --- YAML detection and save prompt ---
			yamlBlocks := extractYAMLBlocks(response)
			if len(yamlBlocks) > 0 {
				for _, yaml := range yamlBlocks {
					fmt.Println()
					utils.InfoMessage("YAML detected in the response.")
					suggested := suggestYAMLFilename(yaml)
					filename := uniqueFilename(suggested)
					save, err := utils.PromptInput(fmt.Sprintf("Do you want to save the YAML to '%s'? (y/n)", filename), "y", "^[yYnN]$")
					if err != nil {
						utils.ErrorMessage(fmt.Sprintf("Error reading input: %v", err))
						continue
					}
					if strings.ToLower(save) == "y" {
						err := os.WriteFile(filename, []byte(yaml), 0644)
						if err != nil {
							utils.ErrorMessage(fmt.Sprintf("Failed to save YAML: %v", err))
						} else {
							utils.SuccessMessage(fmt.Sprintf("YAML saved to %s", filename))
						}
					}
				}
			}
		}
	},
}

func init() {
	AiCmd.Flags().StringP("provider", "p", "", "Force specific AI provider (anthropic, openai, gemini)")
	AiCmd.AddCommand(GrapiAiCmd)
}
