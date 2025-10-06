package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"go.temporal.io/sdk/workflow"
)

// AgenticScrapeGitHubProfileWorkflow is a workflow that uses an agentic approach to scrape GitHub profile data.
func AgenticScrapeGitHubProfileWorkflow(ctx workflow.Context, prompt string) (GitHubProfile, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting agentic GitHub profile scrape workflow")

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 1 * time.Minute,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	conversation := []string{}
	err := workflow.SetQueryHandler(ctx, "GetConversationState", func() ([]string, error) {
		return conversation, nil
	})
	if err != nil {
		return GitHubProfile{}, fmt.Errorf("failed to set query handler: %w", err)
	}

	submitTool := Tool{
		Name:        "submit_github_profile",
		Description: "Submit the final GitHub profile information.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"username":             map[string]string{"type": "string"},
				"bio":                  map[string]string{"type": "string"},
				"location":             map[string]string{"type": "string"},
				"website":              map[string]string{"type": "string"},
				"public_repos":         map[string]string{"type": "integer"},
				"original_repos":       map[string]string{"type": "integer"},
				"forked_repos":         map[string]string{"type": "integer"},
				"languages":            map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
				"top_repositories":     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"name": map[string]string{"type": "string"}, "description": map[string]string{"type": "string"}, "language": map[string]string{"type": "string"}, "stars": map[string]string{"type": "integer"}, "forks": map[string]string{"type": "integer"}, "is_fork": map[string]string{"type": "boolean"}}, "required": []string{"name", "description", "language", "stars", "forks", "is_fork"}, "additionalProperties": false}},
				"contribution_graph":   map[string]interface{}{"type": "object", "properties": map[string]interface{}{"total_contributions": map[string]string{"type": "integer"}, "streak": map[string]string{"type": "integer"}, "contributions": map[string]interface{}{"type": "object", "additionalProperties": map[string]string{"type": "integer"}}}, "required": []string{"total_contributions", "streak"}, "additionalProperties": false},
				"professional_summary": map[string]string{"type": "string"},
				"code_snippets":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"repository": map[string]string{"type": "string"}, "file_path": map[string]string{"type": "string"}, "content": map[string]string{"type": "string"}, "language": map[string]string{"type": "string"}}, "required": []string{"repository", "file_path", "content", "language"}, "additionalProperties": false}},
			},
			"required":             []string{"username", "bio", "location", "website", "public_repos", "original_repos", "forked_repos", "languages", "top_repositories", "contribution_graph", "professional_summary", "code_snippets"},
			"additionalProperties": false,
		},
	}

	ghTool := Tool{
		Name:        "gh",
		Description: "Execute a GitHub CLI command. Examples: `gh api users/USERNAME`, `gh repo list USERNAME --source --no-forks --json name,pushedAt`, `gh commit list --repo OWNER/REPO -L 5`, `gh commit view SHA --repo OWNER/REPO --patch`, `gh repo view OWNER/REPO --json name,pushedAt`",
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
	var githubProfile GitHubProfile

	for i := 0; i < maxTurns; i++ {
		logger.Info("Agent turn", "turn", i+1)
		var turnResult GenerateResponsesTurnResult
		var actErr error

		cfg := OpenAIConfig{
			APIKey: os.Getenv("RESEARCH_ORCHESTRATOR_LLM_API_KEY"),
			Model:  os.Getenv("RESEARCH_ORCHESTRATOR_LLM_MODEL"),
		}

		if previousResponseID == "" {
			err := workflow.ExecuteActivity(ctx, GenerateResponsesTurnActivity, cfg, previousResponseID, prompt, tools, nil).Get(ctx, &turnResult)
			if err != nil {
				actErr = err
			}
		} else {
			err := workflow.ExecuteActivity(ctx, GenerateResponsesTurnActivity, cfg, previousResponseID, "", tools, pendingOutputs).Get(ctx, &turnResult)
			if err != nil {
				actErr = err
			}
		}

		if actErr != nil {
			logger.Error("LLM activity failed", "error", actErr)
			return GitHubProfile{}, actErr
		}
		previousResponseID = turnResult.ID
		pendingOutputs = map[string]string{}
		conversation = append(conversation, fmt.Sprintf("Turn %d: Assistant Response: %s", i+1, turnResult.Assistant))

		if len(turnResult.Calls) > 0 {
			logger.Info("LLM requested tool calls", "calls", turnResult.Calls)
			conversation = append(conversation, fmt.Sprintf("Turn %d: Tool Calls: %+v", i+1, turnResult.Calls))
			for _, toolCall := range turnResult.Calls {
				var toolResult string
				switch toolCall.Name {
				case "submit_github_profile":
					var profile GitHubProfile
					if err := json.Unmarshal([]byte(toolCall.Arguments), &profile); err != nil {
						toolResult = fmt.Sprintf(`{"error": "failed to parse arguments: %v"}`, err)
					} else {
						githubProfile = profile
						logger.Info("Exiting agentic loop with profile")
						return githubProfile, nil
					}
				case "gh":
					var args struct {
						Command string `json:"command"`
					}
					if err := json.Unmarshal([]byte(toolCall.Arguments), &args); err != nil {
						toolResult = fmt.Sprintf(`{"error": "failed to parse arguments: %v"}`, err)
					} else {
						var result string
						err := workflow.ExecuteActivity(ctx, ExecuteGhCommandActivity, args.Command).Get(ctx, &result)
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
				conversation = append(conversation, fmt.Sprintf("Turn %d: Tool Result for %s: %s", i+1, toolCall.ID, truncatedResult))

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
