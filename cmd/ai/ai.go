package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
)

// Configuration
const (
	MCPServerURL = "https://your-lambda-url.amazonaws.com"
)

// AI Provider configuration
type AIConfig struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
}

// MCP Client (simplified without auth)
type MCPClient struct {
	ServerURL string
	Client    *http.Client
}

// AI Session interface
type AISession interface {
	Chat(prompt string) (string, error)
}

// NewMCPClient creates a new MCP client
func NewMCPClient(serverURL string) *MCPClient {
	return &MCPClient{
		ServerURL: serverURL,
		Client:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Main AI command implementation
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
		// Setup AI provider and get configuration
		config, err := setupAIProvider()
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Error setting up AI provider: %v", err))
			return
		}

		// Create MCP client (no auth needed)
		mcpClient := NewMCPClient(MCPServerURL)

		// Create AI session
		aiSession, err := createAISession(config, mcpClient)
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Error creating AI session: %v", err))
			return
		}

		utils.SuccessMessage(fmt.Sprintf("AI assistant ready! Using %s", config.Provider))
		utils.InfoMessage("Type 'exit' or 'quit' to end the session")
		fmt.Println("=" + strings.Repeat("=", 50))
		fmt.Println()

		// Main conversation loop
		for {
			// Get user prompt
			prompt, err := utils.PromptInput("You", "", "^.+$")
			if err != nil {
				utils.ErrorMessage(fmt.Sprintf("Error reading input: %v", err))
				continue
			}

			// Check for exit commands
			if strings.ToLower(strings.TrimSpace(prompt)) == "exit" ||
				strings.ToLower(strings.TrimSpace(prompt)) == "quit" {
				utils.InfoMessage("Goodbye!")
				break
			}

			if strings.TrimSpace(prompt) == "" {
				continue
			}

			// Send to AI
			utils.InfoMessage(fmt.Sprintf("%s:", strings.Title(config.Provider)))
			fmt.Println(strings.Repeat("-", 50))

			response, err := aiSession.Chat(prompt)
			if err != nil {
				utils.ErrorMessage(fmt.Sprintf("Error from AI: %v", err))
				fmt.Println()
				continue
			}

			fmt.Println(response)
			fmt.Println()
		}
	},
}

func init() {

	// Optional flags for advanced users
	AiCmd.Flags().StringP("provider", "p", "", "Force specific AI provider (anthropic, openai, gemini)")
	// Examples in help
	AiCmd.Example = `  # Start interactive AI session
  grpl ai
  
  # Force specific provider
  grpl ai --provider anthropic`
}

// CallTool calls an MCP tool
func (m *MCPClient) CallTool(toolName string, arguments map[string]interface{}) (string, error) {
	payload := map[string]interface{}{
		"name":      toolName,
		"arguments": arguments,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", m.ServerURL+"/mcp/tools/call", bytes.NewBuffer(payloadBytes))
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

// ==================== CLAUDE IMPLEMENTATION ====================
type ClaudeSession struct {
	APIKey    string
	MCPClient *MCPClient
}

func (c *ClaudeSession) Chat(prompt string) (string, error) {
	// Simplified Claude implementation
	reqData := map[string]interface{}{
		"model":      "claude-3-sonnet-20240229",
		"max_tokens": 4000,
		"messages": []map[string]interface{}{
			{
				"role":    "user",
				"content": fmt.Sprintf("You are a helpful assistant for Grapple CRDs. Help the user with: %s", prompt),
			},
		},
	}

	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var claudeResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&claudeResp); err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Claude API error: status %d", resp.StatusCode)
	}

	if len(claudeResp.Content) > 0 {
		return claudeResp.Content[0].Text, nil
	}

	return "", fmt.Errorf("no content in Claude response")
}

// ==================== OPENAI IMPLEMENTATION ====================
type OpenAISession struct {
	APIKey    string
	MCPClient *MCPClient
}

func (o *OpenAISession) Chat(prompt string) (string, error) {
	reqData := map[string]interface{}{
		"model": "gpt-4-turbo-preview",
		"messages": []map[string]interface{}{
			{
				"role":    "system",
				"content": "You are a helpful assistant for Grapple CRDs.",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"max_tokens": 4000,
	}

	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.APIKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var openaiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&openaiResp); err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OpenAI API error: status %d", resp.StatusCode)
	}

	if len(openaiResp.Choices) > 0 {
		return openaiResp.Choices[0].Message.Content, nil
	}

	return "", fmt.Errorf("no content in OpenAI response")
}

// ==================== GEMINI IMPLEMENTATION ====================
type GeminiSession struct {
	APIKey    string
	MCPClient *MCPClient
}

func (g *GeminiSession) Chat(prompt string) (string, error) {
	reqData := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{
						"text": fmt.Sprintf("You are a helpful assistant for Grapple CRDs. Help the user with: %s", prompt),
					},
				},
			},
		},
	}

	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-1.5-pro-latest:generateContent?key=%s", g.APIKey)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Gemini API error: status %d", resp.StatusCode)
	}

	if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
		return geminiResp.Candidates[0].Content.Parts[0].Text, nil
	}

	return "", fmt.Errorf("no content in Gemini response")
}

// Get config directory path
func getConfigDir() (string, error) {
	tmpDir := os.TempDir()
	configDir := filepath.Join(tmpDir, "grpl-cli")

	// Create directory if it doesn't exist
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", err
	}

	return configDir, nil
}

// Save AI configuration
func saveAIConfig(config AIConfig) error {
	configDir, err := getConfigDir()
	if err != nil {
		return err
	}

	configFile := filepath.Join(configDir, "ai-config.json")

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configFile, data, 0600) // Secure permissions for API keys
}

// Load AI configuration
func loadAIConfig() (*AIConfig, error) {
	configDir, err := getConfigDir()
	if err != nil {
		return nil, err
	}

	configFile := filepath.Join(configDir, "ai-config.json")

	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err // File doesn't exist or can't be read
	}

	var config AIConfig
	err = json.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

// Setup AI provider and credentials
func setupAIProvider() (*AIConfig, error) {
	utils.InfoMessage("Setting up AI provider for Grapple CLI")
	fmt.Println()

	// Check if config already exists
	existingConfig, err := loadAIConfig()
	if err == nil && existingConfig.Provider != "" && existingConfig.APIKey != "" {
		utils.InfoMessage(fmt.Sprintf("Found existing configuration for %s", existingConfig.Provider))
		useExisting, err := utils.PromptInput("Use existing configuration? (y/n)", "y", "^[yYnN]$")
		if err != nil {
			return nil, err
		}

		if strings.ToLower(useExisting) == "y" {
			return existingConfig, nil
		}
	}

	// Select AI provider
	providers := []string{
		"Anthropic (Claude)",
		"OpenAI (GPT)",
		"Google (Gemini)",
	}

	providerChoice, err := utils.PromptSelect("Select AI provider", providers)
	if err != nil {
		return nil, err
	}

	var provider string
	var apiKeyPrompt string

	switch providerChoice {
	case "Anthropic (Claude)":
		provider = "anthropic"
		apiKeyPrompt = "Enter your Anthropic API key"
	case "OpenAI (GPT)":
		provider = "openai"
		apiKeyPrompt = "Enter your OpenAI API key"
	case "Google (Gemini)":
		provider = "gemini"
		apiKeyPrompt = "Enter your Google AI API key"
	default:
		return nil, fmt.Errorf("invalid provider choice")
	}

	// Get API key
	fmt.Println()
	apiKey, err := utils.PromptInput(apiKeyPrompt+":", "", "^.+$")
	if err != nil {
		return nil, err
	}

	if apiKey == "" {
		return nil, fmt.Errorf("API key cannot be empty")
	}

	// Save configuration
	config := AIConfig{
		Provider: provider,
		APIKey:   apiKey,
	}

	if err := saveAIConfig(config); err != nil {
		return nil, fmt.Errorf("failed to save configuration: %v", err)
	}

	utils.SuccessMessage(fmt.Sprintf("Configuration saved for %s", provider))
	fmt.Println()
	return &config, nil
}

// Create AI session based on provider
func createAISession(config *AIConfig, mcpClient *MCPClient) (AISession, error) {
	switch config.Provider {
	case "anthropic":
		return &ClaudeSession{
			APIKey:    config.APIKey,
			MCPClient: mcpClient,
		}, nil
	case "openai":
		return &OpenAISession{
			APIKey:    config.APIKey,
			MCPClient: mcpClient,
		}, nil
	case "gemini":
		return &GeminiSession{
			APIKey:    config.APIKey,
			MCPClient: mcpClient,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported AI provider: %s", config.Provider)
	}
}
