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

	"github.com/grapple-solution/grapple_cli/utils"
)

// ToolProvider defines the interface for fetching tools and prompts
type ToolProvider interface {
	GetAvailableTools() ([]map[string]interface{}, error)
	GetAvailablePrompts() ([]map[string]interface{}, error)
	CallTool(name string, arguments map[string]interface{}) (string, error)
}

type AIConfig struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
}

type AISession interface {
	Chat(prompt string) (string, error)
	GetModel() string
}

// Helper: Extract YAML blocks from a string
func extractYAMLBlocks(s string) []string {
	// This regex matches code blocks with yaml/yml or indented yaml
	re := regexp.MustCompile("(?s)```(?:yaml|yml)?\\s*([\\s\\S]*?)```")
	matches := re.FindAllStringSubmatch(s, -1)
	var yamls []string
	for _, m := range matches {
		yaml := strings.TrimSpace(m[1])
		if yaml != "" {
			yamls = append(yamls, yaml)
		}
	}
	// Also try to find standalone YAML (starts with apiVersion: or kind:)
	if len(yamls) == 0 {
		lines := strings.Split(s, "\n")
		var buf []string
		inYaml := false
		for _, line := range lines {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "apiVersion:") || strings.HasPrefix(trim, "kind:") {
				inYaml = true
			}
			if inYaml {
				buf = append(buf, line)
			}
			// End YAML block if we hit an empty line after starting
			if inYaml && trim == "" && len(buf) > 0 {
				break
			}
		}
		if len(buf) > 0 {
			yamls = append(yamls, strings.TrimSpace(strings.Join(buf, "\n")))
		}
	}
	return yamls
}

// Helper: Suggest a filename for a YAML block
func suggestYAMLFilename(yaml string) string {
	// Try to extract kind and metadata.name
	kind := ""
	name := ""
	lines := strings.Split(yaml, "\n")
	for _, line := range lines {
		if kind == "" && strings.HasPrefix(strings.TrimSpace(line), "kind:") {
			kind = strings.TrimSpace(strings.TrimPrefix(line, "kind:"))
			kind = strings.ToLower(strings.ReplaceAll(kind, " ", ""))
		}
		if name == "" && strings.HasPrefix(strings.TrimSpace(line), "name:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			name = strings.ToLower(strings.ReplaceAll(name, " ", "-"))
		}
		if kind != "" && name != "" {
			break
		}
	}
	base := "resource"
	if kind != "" {
		base = kind
	}
	if name != "" {
		base = base + "-" + name
	}
	return base + ".yaml"
}

// Helper: Find a unique filename in the current directory
func uniqueFilename(base string) string {
	_, err := os.Stat(base)
	if os.IsNotExist(err) {
		return base
	}
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d%s", name, i, ext)
		_, err := os.Stat(candidate)
		if os.IsNotExist(err) {
			return candidate
		}
	}
	return fmt.Sprintf("%s-%d%s", name, time.Now().Unix(), ext)
}

func getConfigDir() (string, error) {
	tmpDir := os.TempDir()
	configDir := filepath.Join(tmpDir, "grpl-cli")

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", err
	}

	return configDir, nil
}

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

	return os.WriteFile(configFile, data, 0600)
}

func loadAIConfig() (*AIConfig, error) {
	configDir, err := getConfigDir()
	if err != nil {
		return nil, err
	}

	configFile := filepath.Join(configDir, "ai-config.json")

	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}

	var config AIConfig
	err = json.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

func setupAIProvider() (*AIConfig, error) {
	utils.InfoMessage("Setting up AI provider for Grapple CLI")
	fmt.Println()

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

	fmt.Println()
	apiKey, err := utils.PromptPassword(apiKeyPrompt + ":")
	if err != nil {
		return nil, err
	}

	if apiKey == "" {
		return nil, fmt.Errorf("API key cannot be empty")
	}

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

func createAISession(config *AIConfig, provider ToolProvider) (AISession, error) {
	switch config.Provider {
	case "anthropic":
		model := getEnvModel("CLAUDE_MODEL", "claude-3-5-sonnet-latest")
		return &ClaudeSession{
			APIKey:       config.APIKey,
			Model:        model,
			ToolProvider: provider,
		}, nil
	case "openai":
		model := getEnvModel("OPENAI_MODEL", "gpt-4o")
		return &OpenAISession{
			APIKey:       config.APIKey,
			Model:        model,
			ToolProvider: provider,
		}, nil
	case "gemini":
		model := getEnvModel("GEMINI_MODEL", "gemini-2.5-flash")
		return &GeminiSession{
			APIKey:       config.APIKey,
			Model:        model,
			ToolProvider: provider,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported AI provider: %s", config.Provider)
	}
}

func getEnvModel(envVar, defaultValue string) string {
	if val := os.Getenv(envVar); val != "" {
		utils.InfoMessage(fmt.Sprintf("Using model override from %s: %s", envVar, val))
		return val
	}
	return defaultValue
}

// --- Provider Sessions ---

type ClaudeResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	ToolUse    []struct {
		ID        string                 `json:"id"`
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	} `json:"tool_use,omitempty"`
}

type ClaudeSession struct {
	APIKey       string
	Model        string
	ToolProvider ToolProvider
	Messages     []map[string]interface{}
}

func (c *ClaudeSession) GetModel() string {
	return c.Model
}

func (c *ClaudeSession) Chat(prompt string) (string, error) {
	tools, err := c.ToolProvider.GetAvailableTools()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not fetch tools: %v\n", err)
		tools = []map[string]interface{}{}
	}

	prompts, err := c.ToolProvider.GetAvailablePrompts()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not fetch prompts: %v\n", err)
		prompts = []map[string]interface{}{}
	}

	if c.Messages == nil {
		c.Messages = []map[string]interface{}{}
	}

	if prompt != "" {
		c.Messages = append(c.Messages, map[string]interface{}{
			"role":    "user",
			"content": prompt,
		})
	}

	systemMsg := "You are a helpful assistant for Grapple solutions. You have access to tools and prompts that can help you interact with resources and configurations. Use these tools and prompts when appropriate to provide accurate and helpful responses."
	if len(prompts) > 0 {
		var promptTexts []string
		for _, p := range prompts {
			if text, ok := p["text"].(string); ok && text != "" {
				promptTexts = append(promptTexts, text)
			}
		}
		if len(promptTexts) > 0 {
			systemMsg += "\n\nAvailable Prompts:\n" + strings.Join(promptTexts, "\n")
		}
	}

	reqData := map[string]interface{}{
		"model":      c.Model,
		"max_tokens": 4000,
		"messages":   c.Messages,
		"system":     systemMsg,
	}

	if len(tools) > 0 {
		reqData["tools"] = tools
	}

	response, err := c.callClaudeAPI(reqData)
	if err != nil {
		return "", err
	}

	if response.StopReason == "tool_use" && len(response.ToolUse) > 0 {
		var contentParts []interface{}
		if len(response.Content) > 0 && response.Content[0].Text != "" {
			contentParts = append(contentParts, map[string]interface{}{
				"type": "text",
				"text": response.Content[0].Text,
			})
		}

		for _, toolCall := range response.ToolUse {
			contentParts = append(contentParts, map[string]interface{}{
				"type":  "tool_use",
				"id":    toolCall.ID,
				"name":  toolCall.Name,
				"input": toolCall.Arguments,
			})

			result, err := c.ToolProvider.CallTool(toolCall.Name, toolCall.Arguments)
			if err != nil {
				result = fmt.Sprintf("Error calling tool %s: %v", toolCall.Name, err)
			}

			c.Messages = append(c.Messages, map[string]interface{}{
				"role": "user",
				"content": []map[string]interface{}{
					{
						"type":        "tool_result",
						"tool_use_id": toolCall.ID,
						"content":     result,
					},
				},
			})
		}

		c.Messages = append(c.Messages, map[string]interface{}{
			"role":    "assistant",
			"content": contentParts,
		})

		return c.Chat("")
	}

	if len(response.Content) > 0 && response.Content[0].Text != "" {
		c.Messages = append(c.Messages, map[string]interface{}{
			"role":    "assistant",
			"content": response.Content[0].Text,
		})
		return response.Content[0].Text, nil
	}

	return "", fmt.Errorf("no content in Claude response")
}

func (c *ClaudeSession) callClaudeAPI(reqData map[string]interface{}) (*ClaudeResponse, error) {
	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Claude API error: status %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	var claudeResp ClaudeResponse
	if err := json.Unmarshal(bodyBytes, &claudeResp); err != nil {
		return nil, err
	}

	return &claudeResp, nil
}

type OpenAIResponse struct {
	Choices []struct {
		Message struct {
			Content      string `json:"content"`
			FunctionCall *struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function_call,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

type OpenAISession struct {
	APIKey       string
	Model        string
	ToolProvider ToolProvider
	Messages     []map[string]interface{}
}

func (o *OpenAISession) GetModel() string {
	return o.Model
}

func (o *OpenAISession) Chat(prompt string) (string, error) {
	tools, err := o.ToolProvider.GetAvailableTools()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not fetch tools: %v\n", err)
		tools = []map[string]interface{}{}
	}

	prompts, err := o.ToolProvider.GetAvailablePrompts()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not fetch prompts: %v\n", err)
		prompts = []map[string]interface{}{}
	}

	if o.Messages == nil {
		o.Messages = []map[string]interface{}{
			{
				"role":    "system",
				"content": "", // will be set below
			},
		}
	}

	if prompt != "" {
		o.Messages = append(o.Messages, map[string]interface{}{
			"role":    "user",
			"content": prompt,
		})
	}

	systemMsg := "You are a helpful assistant for Grapple solutions. You have access to tools and prompts that can help you interact with resources and configurations. Use these tools and prompts when appropriate to provide accurate and helpful responses."
	if len(prompts) > 0 {
		var promptTexts []string
		for _, p := range prompts {
			if text, ok := p["text"].(string); ok && text != "" {
				promptTexts = append(promptTexts, text)
			}
		}
		if len(promptTexts) > 0 {
			systemMsg += "\n\nAvailable Prompts:\n" + strings.Join(promptTexts, "\n")
		}
	}
	if len(o.Messages) > 0 && o.Messages[0]["role"] == "system" {
		o.Messages[0]["content"] = systemMsg
	}

	functions := []map[string]interface{}{}
	for _, tool := range tools {
		functions = append(functions, map[string]interface{}{
			"name":        tool["name"],
			"description": tool["description"],
			"parameters":  tool["inputSchema"],
		})
	}

	reqData := map[string]interface{}{
		"model":      o.Model,
		"messages":   o.Messages,
		"max_tokens": 4000,
	}
	if len(functions) > 0 {
		reqData["functions"] = functions
		reqData["function_call"] = "auto"
	}

	response, err := o.callOpenAIAPI(reqData)
	if err != nil {
		return "", err
	}

	if len(response.Choices) > 0 && response.Choices[0].Message.FunctionCall != nil {
		functionCall := response.Choices[0].Message.FunctionCall

		var args map[string]interface{}
		if err := json.Unmarshal([]byte(functionCall.Arguments), &args); err != nil {
			return "", fmt.Errorf("failed to parse function arguments: %v", err)
		}

		result, err := o.ToolProvider.CallTool(functionCall.Name, args)
		if err != nil {
			result = fmt.Sprintf("Error calling tool %s: %v", functionCall.Name, err)
		}

		o.Messages = append(o.Messages, map[string]interface{}{
			"role":          "assistant",
			"content":       nil,
			"function_call": functionCall,
		})

		o.Messages = append(o.Messages, map[string]interface{}{
			"role":    "function",
			"name":    functionCall.Name,
			"content": result,
		})

		return o.Chat("")
	}

	if len(response.Choices) > 0 && response.Choices[0].Message.Content != "" {
		content := response.Choices[0].Message.Content
		o.Messages = append(o.Messages, map[string]interface{}{
			"role":    "assistant",
			"content": content,
		})
		return content, nil
	}

	return "", fmt.Errorf("no content in OpenAI response")
}

func (o *OpenAISession) callOpenAIAPI(reqData map[string]interface{}) (*OpenAIResponse, error) {
	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.APIKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("OpenAI API error: status %d, body: %s", resp.StatusCode, string(bodyBytes))
		if resp.StatusCode == 429 {
			return nil, fmt.Errorf("OpenAI API error: rate limit exceeded (status 429). Please try again later")
		}
		return nil, fmt.Errorf(errMsg)
	}

	var openaiResp OpenAIResponse
	if err := json.Unmarshal(bodyBytes, &openaiResp); err != nil {
		return nil, err
	}

	return &openaiResp, nil
}

type GeminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text         string `json:"text,omitempty"`
				FunctionCall *struct {
					Name string                 `json:"name"`
					Args map[string]interface{} `json:"args"`
				} `json:"functionCall,omitempty"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
}

type GeminiSession struct {
	APIKey       string
	Model        string
	ToolProvider ToolProvider
	History      []map[string]interface{}
}

func (g *GeminiSession) GetModel() string {
	return g.Model
}

func (g *GeminiSession) Chat(prompt string) (string, error) {
	tools, err := g.ToolProvider.GetAvailableTools()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not fetch tools: %v\n", err)
		tools = []map[string]interface{}{}
	}

	prompts, err := g.ToolProvider.GetAvailablePrompts()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not fetch prompts: %v\n", err)
		prompts = []map[string]interface{}{}
	}

	if g.History == nil {
		g.History = []map[string]interface{}{}
	}

	if prompt != "" {
		g.History = append(g.History, map[string]interface{}{
			"role": "user",
			"parts": []map[string]interface{}{
				{"text": prompt},
			},
		})
	}

	geminiTools := []map[string]interface{}{}
	if len(tools) > 0 {
		functionDeclarations := []map[string]interface{}{}
		for _, tool := range tools {
			functionDeclarations = append(functionDeclarations, map[string]interface{}{
				"name":        tool["name"],
				"description": tool["description"],
				"parameters":  tool["inputSchema"],
			})
		}
		geminiTools = append(geminiTools, map[string]interface{}{
			"functionDeclarations": functionDeclarations,
		})
	}

	systemMsg := "You are a helpful assistant for Grapple solutions. You have access to tools and prompts that can help you interact with resources and configurations. Use these tools and prompts when appropriate to provide accurate and helpful responses."
	if len(prompts) > 0 {
		var promptTexts []string
		for _, p := range prompts {
			if text, ok := p["text"].(string); ok && text != "" {
				promptTexts = append(promptTexts, text)
			}
		}
		if len(promptTexts) > 0 {
			systemMsg += "\n\nAvailable Prompts:\n" + strings.Join(promptTexts, "\n")
		}
	}

	reqData := map[string]interface{}{
		"contents": g.History,
		"systemInstruction": map[string]interface{}{
			"parts": []map[string]interface{}{
				{
					"text": systemMsg,
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"maxOutputTokens": 4000,
		},
	}
	if len(geminiTools) > 0 {
		reqData["tools"] = geminiTools
	}

	response, err := g.callGeminiAPI(reqData)
	if err != nil {
		return "", err
	}

	if len(response.Candidates) > 0 && len(response.Candidates[0].Content.Parts) > 0 {
		candidate := response.Candidates[0]
		var assistantParts []map[string]interface{}
		var functionCalls []map[string]interface{}
		var finalText string
		var hasFunctionCalls bool

		for _, part := range candidate.Content.Parts {
			if part.FunctionCall != nil {
				assistantParts = append(assistantParts, map[string]interface{}{
					"functionCall": part.FunctionCall,
				})
				functionCalls = append(functionCalls, map[string]interface{}{
					"name": part.FunctionCall.Name,
					"args": part.FunctionCall.Args,
				})
				hasFunctionCalls = true
			} else if part.Text != "" {
				assistantParts = append(assistantParts, map[string]interface{}{
					"text": part.Text,
				})
				finalText = part.Text
			}
		}

		if len(assistantParts) > 0 {
			g.History = append(g.History, map[string]interface{}{
				"role":  "model",
				"parts": assistantParts,
			})
		}

		if hasFunctionCalls {
			var functionResponses []map[string]interface{}
			for _, fc := range functionCalls {
				name := fc["name"].(string)
				args := fc["args"].(map[string]interface{})

				result, err := g.ToolProvider.CallTool(name, args)
				if err != nil {
					result = fmt.Sprintf("Error calling tool %s: %v", name, err)
				}

				functionResponses = append(functionResponses, map[string]interface{}{
					"functionResponse": map[string]interface{}{
						"name": name,
						"response": map[string]interface{}{
							"result": result,
						},
					},
				})
			}

			g.History = append(g.History, map[string]interface{}{
				"role":  "function",
				"parts": functionResponses,
			})

			return g.Chat("")
		}

		return finalText, nil
	}

	return "", fmt.Errorf("no content in Gemini response")
}

func (g *GeminiSession) callGeminiAPI(reqData map[string]interface{}) (*GeminiResponse, error) {
	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", g.Model, g.APIKey)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Gemini API error: status %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	var geminiResp GeminiResponse
	if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
		return nil, err
	}

	return &geminiResp, nil
}
