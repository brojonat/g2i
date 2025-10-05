package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/chai2010/webp"
	"github.com/nfnt/resize"
	"go.temporal.io/sdk/activity"
	"google.golang.org/genai"
)

// AgenticScrapeGitHubProfile uses an agentic approach to scrape GitHub profile data. The general idea
// is to use the OpenAI Responses API and provide a single tool call to the agent: the GitHub CLI.
// The agent consists of a for loop that runs until the agent has enough information to stop
// looping and generate a prompt for the image generation.
func AgenticScrapeGitHubProfile(ctx context.Context, prompt string) (GitHubProfile, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Starting agentic GitHub profile scrape")
	// This implements an agentic approach where we use GitHub CLI in a loop
	// until we're satisfied we have sufficient data.

	submitTool := Tool{
		Name:        "submit_github_profile",
		Description: "Submit the final GitHub profile information.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"username":      map[string]string{"type": "string"},
				"bio":           map[string]string{"type": "string"},
				"location":      map[string]string{"type": "string"},
				"website":       map[string]string{"type": "string"},
				"publicRepos":   map[string]string{"type": "integer"},
				"originalRepos": map[string]string{"type": "integer"},
				"forkedRepos":   map[string]string{"type": "integer"},
				"languages":     map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
				"topRepositories": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"name":        map[string]string{"type": "string"},
							"description": map[string]string{"type": "string"},
							"language":    map[string]string{"type": "string"},
							"stars":       map[string]string{"type": "integer"},
							"forks":       map[string]string{"type": "integer"},
							"is_fork":     map[string]string{"type": "boolean"},
						},
						"required":             []string{"name", "description", "language", "stars", "forks", "is_fork"},
						"additionalProperties": false,
					},
				},
				"contributionGraph": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"total_contributions": map[string]string{"type": "integer"},
						"streak":              map[string]string{"type": "integer"},
						"contributions": map[string]interface{}{
							"type":                 "object",
							"additionalProperties": map[string]string{"type": "integer"},
						},
					},
					"required":             []string{"total_contributions", "streak"},
					"additionalProperties": false,
				},
				"professional_summary": map[string]string{"type": "string"},
				"code_snippets": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"repository": map[string]string{"type": "string"},
							"file_path":  map[string]string{"type": "string"},
							"content":    map[string]string{"type": "string"},
							"language":   map[string]string{"type": "string"},
						},
						"required":             []string{"repository", "file_path", "content", "language"},
						"additionalProperties": false,
					},
				},
			},
			"required":             []string{"username", "bio", "location", "website", "publicRepos", "originalRepos", "forkedRepos", "languages", "topRepositories", "contributionGraph", "professional_summary", "code_snippets"},
			"additionalProperties": false,
		},
	}

	ghTool := Tool{
		Name:        "gh",
		Description: "Execute a GitHub CLI command. Examples: `gh api users/USERNAME`, `gh repo list --owner USERNAME --source --no-forks --json name,pushedAt`, `gh commit list --repo OWNER/REPO -L 5`, `gh commit view SHA --repo OWNER/REPO --patch`, `gh repo view OWNER/REPO --json name,pushedAt`",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]string{"type": "string", "description": "The `gh` command arguments to execute. Do not include 'gh' in the command."},
			},
			"required":             []string{"command"},
			"additionalProperties": false,
		},
	}
	tools := []Tool{submitTool, ghTool}

	previousResponseID := ""
	pendingOutputs := map[string]string{}
	maxTurns := 20

	for i := 0; i < maxTurns; i++ {
		logger.Info("Agent turn", "turn", i+1)
		var turnResult GenerateResponsesTurnResult
		var actErr error

		cfg := OpenAIConfig{
			APIKey: os.Getenv("RESEARCH_ORCHESTRATOR_LLM_API_KEY"),
			Model:  os.Getenv("RESEARCH_ORCHESTRATOR_LLM_MODEL"),
		}

		if previousResponseID == "" {
			text, calls, id, err := generateResponsesTurn(ctx, cfg, previousResponseID, prompt, tools, nil)
			if err != nil {
				actErr = err
			} else {
				turnResult = GenerateResponsesTurnResult{Assistant: text, Calls: calls, ID: id}
			}
		} else {
			text, calls, id, err := generateResponsesTurn(ctx, cfg, previousResponseID, "", tools, pendingOutputs)
			if err != nil {
				actErr = err
			} else {
				turnResult = GenerateResponsesTurnResult{Assistant: text, Calls: calls, ID: id}
			}
		}

		if actErr != nil {
			logger.Error("LLM activity failed", "error", actErr)
			return GitHubProfile{}, actErr
		}
		previousResponseID = turnResult.ID
		pendingOutputs = map[string]string{}

		if len(turnResult.Calls) > 0 {
			logger.Info("LLM requested tool calls", "calls", turnResult.Calls)
			for _, toolCall := range turnResult.Calls {
				var toolResult string
				switch toolCall.Name {
				case "submit_github_profile":
					var profile GitHubProfile
					if err := json.Unmarshal([]byte(toolCall.Arguments), &profile); err != nil {
						toolResult = fmt.Sprintf(`{"error": "failed to parse arguments: %v"}`, err)
					} else {
						// Success, we can exit the loop.
						return profile, nil
					}
				case "gh":
					var args struct {
						Command string `json:"command"`
					}
					if err := json.Unmarshal([]byte(toolCall.Arguments), &args); err != nil {
						toolResult = fmt.Sprintf(`{"error": "failed to parse arguments: %v"}`, err)
					} else {
						result, err := executeGhCommand(ctx, args.Command)
						if err != nil {
							toolResult = fmt.Sprintf(`{"error": "failed to execute tool: %v"}`, err)
						} else {
							toolResult = result
						}
					}
				default:
					toolResult = `{"error": "unknown tool requested"}`
				}
				pendingOutputs[toolCall.ID] = toolResult
				const maxLogLength = 512
				truncatedResult := toolResult
				if len(truncatedResult) > maxLogLength {
					truncatedResult = truncatedResult[:maxLogLength] + "..."
				}
				logger.Info("Tool call result", "call_id", toolCall.ID, "name", toolCall.Name, "result", truncatedResult)
			}
			continue
		}
		if strings.TrimSpace(turnResult.Assistant) == "" {
			logger.Warn("No tool calls and no assistant content; ending conversation")
			break
		}
		logger.Info("LLM responded with text", "text", turnResult.Assistant)
	}

	return GitHubProfile{}, fmt.Errorf("agentic loop finished without submitting a profile")
}

func executeGhCommand(ctx context.Context, command string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", strings.Fields(command)...)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("error executing gh command: %w\nstderr: %s", err, stderr.String())
	}
	return out.String(), nil
}

type OpenAIConfig struct {
	APIKey    string
	Model     string
	MaxTokens int
}

func generateResponsesTurn(ctx context.Context, p OpenAIConfig, previousResponseID string, userInput string, tools []Tool, functionOutputs map[string]string) (string, []ToolCall, string, error) {
	if p.MaxTokens == 0 {
		p.MaxTokens = 4096
	}

	req := map[string]interface{}{
		"model":             p.Model,
		"store":             true,
		"max_output_tokens": p.MaxTokens,
	}

	if previousResponseID != "" {
		req["previous_response_id"] = previousResponseID
		inputs := make([]map[string]interface{}, 0, len(functionOutputs))
		for callID, output := range functionOutputs {
			inputs = append(inputs, map[string]interface{}{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  output,
			})
		}
		req["input"] = inputs
	} else {
		req["input"] = userInput
	}

	if len(tools) > 0 {
		toolList := make([]map[string]interface{}, 0, len(tools))
		for _, t := range tools {
			toolList = append(toolList, map[string]interface{}{
				"type":        "function",
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
				"strict":      true,
			})
		}
		req["tools"] = toolList
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to marshal responses request: %w", err)
	}
	apiURL := "https://api.openai.com/v1/responses"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to create responses request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)

	client := &http.Client{}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to send responses request: %w", err)
	}
	defer httpResp.Body.Close()
	body, _ := io.ReadAll(httpResp.Body)
	if httpResp.StatusCode != http.StatusOK {
		return "", nil, "", fmt.Errorf("responses api returned status %d: %s", httpResp.StatusCode, string(body))
	}

	assistantText, toolCalls, responseID, err := parseResponsesOutput(body)
	if err != nil {
		return "", nil, "", err
	}
	return assistantText, toolCalls, responseID, nil
}

func parseResponsesOutput(body []byte) (assistantText string, toolCalls []ToolCall, responseID string, err error) {
	var root struct {
		ID     string          `json:"id"`
		Output json.RawMessage `json:"output"`
	}
	if e := json.Unmarshal(body, &root); e != nil {
		return "", nil, "", fmt.Errorf("failed to decode responses body: %w", e)
	}
	responseID = root.ID

	var items []map[string]any
	if e := json.Unmarshal(root.Output, &items); e != nil {
		var alt struct {
			Output []map[string]any `json:"output"`
		}
		if e2 := json.Unmarshal(body, &alt); e2 == nil && len(alt.Output) > 0 {
			items = alt.Output
		} else {
			// It might be a single object, not an array
			var singleItem map[string]any
			if e3 := json.Unmarshal(root.Output, &singleItem); e3 == nil {
				items = []map[string]any{singleItem}
			} else {
				return "", nil, responseID, fmt.Errorf("unexpected responses output format: %v", e)
			}
		}
	}

	var textBuilder []string
	var calls []ToolCall
	for _, it := range items {
		t, _ := it["type"].(string)
		switch t {
		case "message":
			if content, ok := it["content"].([]any); ok {
				for _, c := range content {
					if cm, ok := c.(map[string]any); ok {
						if cm["type"] == "output_text" {
							if txt, _ := cm["text"].(string); txt != "" {
								textBuilder = append(textBuilder, txt)
							}
						}
					}
				}
			}
			if mtc, ok := it["tool_calls"].([]any); ok {
				for _, raw := range mtc {
					if m, ok := raw.(map[string]any); ok {
						id, _ := m["id"].(string)
						if fn, ok := m["function"].(map[string]any); ok {
							name, _ := fn["name"].(string)
							args, _ := fn["arguments"].(string)
							if id != "" && name != "" {
								calls = append(calls, ToolCall{ID: id, Name: name, Arguments: args})
							}
						}
					}
				}
			}
		case "function_call":
			id, _ := it["call_id"].(string)
			name, _ := it["name"].(string)
			var argsStr string
			if s, ok := it["arguments"].(string); ok {
				argsStr = s
			} else if obj, ok := it["arguments"].(map[string]any); ok {
				if b, e := json.Marshal(obj); e == nil {
					argsStr = string(b)
				}
			}
			if id != "" && name != "" {
				calls = append(calls, ToolCall{ID: id, Name: name, Arguments: argsStr})
			}
		}
	}

	return strings.TrimSpace(strings.Join(textBuilder, "\n")), calls, responseID, nil
}

// GeneratePrompt creates a "report card" prompt for content generation based on GitHub profile
func GenerateContentGenerationPrompt(ctx context.Context, profile GitHubProfile, systemPrompt string) (string, error) {
	// Build a comprehensive "report card" prompt that grounds the profile in cultural context
	prompt := fmt.Sprintf(`
Create a visual representation that grounds this developer's profile in modern cultural context. Think of this as their "report card" but make it culturally relevant and meme-worthy.

**Developer Report Card:**
- Username: %s
- Bio: %s
- Location: %s
- Languages: %s
- Public Repos: %d (Original: %d, Forked: %d)
- Professional Score: %.1f/10

**Top Repositories:**
%s

**Code Style Indicators:**
%s

**Professional Assessment:**
- Safety Flags: %s
- Contribution Activity: %d total contributions, %d day streak

**Cultural Context Instructions:**
Based on their profile, create a visual that puts them in modern cultural context. For example:
- If they're a high-achiever: "Three Dragons" meme (the one who's clearly the best)
- If they're average: Bell curve meme (sitting comfortably in the middle)
- If they're struggling: "This is fine" dog meme
- If they're a language polyglot: "I know 20 languages" flex meme
- If they're a minimalist: "Less is more" aesthetic meme
- If they're a documentation enthusiast: "Read the docs" energy meme

Create a visual that captures their essence as a developer through the lens of internet culture and memes. Make it relatable, funny, and culturally grounded.
`,
		profile.Username,
		profile.Bio,
		profile.Location,
		strings.Join(profile.Languages, ", "),
		profile.PublicRepos,
		profile.OriginalRepos,
		profile.ForkedRepos,
		profile.ProfessionalScore,
		formatRepositories(profile.TopRepositories),
		formatCodeSnippets(profile.CodeSnippets),
		strings.Join(profile.SafetyFlags, ", "),
		profile.ContributionGraph.TotalContributions,
		profile.ContributionGraph.Streak,
	)

	return prompt, nil
}

// GenerateContentOutput holds the return values for the GenerateContent activity
type GenerateContentOutput struct {
	ImageData   []byte `json:"image_data"`
	ContentType string `json:"content_type"`
}

// GenerateContent uses a frontier model to generate content and optionally convert it
func GenerateContent(ctx context.Context, prompt, modelName, imageFormat string, imageWidth, imageHeight int) (GenerateContentOutput, error) {
	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		return GenerateContentOutput{}, fmt.Errorf("GOOGLE_API_KEY environment variable not set")
	}

	// Initialize Gemini client. It will use the GOOGLE_API_KEY environment variable if it is set.
	client, err := genai.NewClient(ctx, nil)
	if err != nil {
		return GenerateContentOutput{}, fmt.Errorf("failed to create genai client: %w", err)
	}

	// Generate the image
	result, err := client.Models.GenerateContent(
		ctx,
		modelName,
		genai.Text(prompt),
		nil,
	)
	if err != nil {
		return GenerateContentOutput{}, fmt.Errorf("failed to generate content: %w", err)
	}

	if result.Candidates == nil || len(result.Candidates) == 0 || result.Candidates[0].Content == nil || len(result.Candidates[0].Content.Parts) == 0 {
		return GenerateContentOutput{}, fmt.Errorf("no content returned from API")
	}

	var originalImageData []byte
	for _, part := range result.Candidates[0].Content.Parts {
		if part.InlineData != nil {
			originalImageData = part.InlineData.Data
			break
		}
	}

	if originalImageData == nil {
		return GenerateContentOutput{}, fmt.Errorf("no image data returned")
	}

	// If no format or dimensions are specified, return the original image
	if imageFormat == "" && imageWidth == 0 && imageHeight == 0 {
		return GenerateContentOutput{
			ImageData:   originalImageData,
			ContentType: "image/png", // Assuming default is png
		}, nil
	}

	img, _, err := image.Decode(bytes.NewReader(originalImageData))
	if err != nil {
		return GenerateContentOutput{}, fmt.Errorf("failed to decode image: %w", err)
	}

	// Resize the image if dimensions are provided
	if imageWidth > 0 || imageHeight > 0 {
		img = resize.Resize(uint(imageWidth), uint(imageHeight), img, resize.Lanczos3)
	}

	var buf bytes.Buffer
	var contentType string

	switch strings.ToLower(imageFormat) {
	case "jpeg", "jpg":
		contentType = "image/jpeg"
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80})
	case "webp":
		contentType = "image/webp"
		err = webp.Encode(&buf, img, &webp.Options{Quality: 75})
	case "png":
		contentType = "image/png"
		err = png.Encode(&buf, img)
	default:
		// If an unsupported format is specified, return the original image
		return GenerateContentOutput{
			ImageData:   originalImageData,
			ContentType: "image/png",
		}, nil
	}

	if err != nil {
		return GenerateContentOutput{}, fmt.Errorf("failed to encode image to %s: %w", imageFormat, err)
	}

	return GenerateContentOutput{
		ImageData:   buf.Bytes(),
		ContentType: contentType,
	}, nil
}

// StoreContent stores content in object storage using the generic interface
func StoreContent(ctx context.Context, data []byte, provider, bucket, key, username, contentType string) (string, error) {
	if provider == "" {
		// This case should be handled by the caller; if no provider, don't call this.
		// For now, we'll return an error.
		return "", fmt.Errorf("storage provider cannot be empty")
	}

	// Generate a key if none provided
	if key == "" {
		key = generateStorageKey(username, contentType)
	}

	// Create storage instance
	storage := NewObjectStorage(provider)

	// Store the content
	return storage.Store(ctx, data, bucket, key, contentType)
}

// Helper functions for formatting
func formatRepositories(repos []Repository) string {
	var formatted []string
	for _, repo := range repos {
		formatted = append(formatted, fmt.Sprintf("- %s (%s): %s - %d stars",
			repo.Name, repo.Language, repo.Description, repo.Stars))
	}
	return strings.Join(formatted, "\n")
}

func formatCodeSnippets(snippets []CodeSnippet) string {
	var formatted []string
	for _, snippet := range snippets {
		formatted = append(formatted, fmt.Sprintf("- %s/%s (%s): %s",
			snippet.Repository, snippet.FilePath, snippet.Language, snippet.Content))
	}
	return strings.Join(formatted, "\n")
}
