package main

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/workflow"
)

// RunContentGenerationWorkflow is the main workflow for generating content from GitHub profiles
func RunContentGenerationWorkflow(ctx workflow.Context, input AppInput) (AppOutput, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting content generation workflow", "username", input.GitHubUsername)

	state := WorkflowState{Status: "Initializing..."}
	err := workflow.SetQueryHandler(ctx, "getStatus", func() (WorkflowState, error) {
		return state, nil
	})
	if err != nil {
		logger.Error("Failed to set query handler", "error", err)
		return AppOutput{}, err
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Step 1: Scrape GitHub profile
	state.Status = "Analyzing GitHub profile..."
	var githubProfile GitHubProfile
	agentSystemPrompt := input.ResearchAgentSystemPrompt
	agentSystemPrompt += fmt.Sprintf("\n\nScrape this info from the GitHub profile for the user: %s", input.GitHubUsername)

	// The agentic scrape activity can take much longer, so we'll give it a separate, longer timeout.
	agentActivityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 15 * time.Minute,
	}
	agentCtx := workflow.WithActivityOptions(ctx, agentActivityOptions)
	err = workflow.ExecuteActivity(agentCtx, AgenticScrapeGitHubProfile, agentSystemPrompt).Get(ctx, &githubProfile)
	if err != nil {
		logger.Error("Failed to scrape GitHub profile", "error", err)
		return AppOutput{}, err
	}

	// Step 2: Generate content generation prompt
	state.Status = "Generating prompt..."
	var contentGenerationPrompt string
	err = workflow.ExecuteActivity(ctx, GenerateContentGenerationPrompt, githubProfile, input.ContentGenerationSystemPrompt).Get(ctx, &contentGenerationPrompt)
	if err != nil {
		logger.Error("Failed to generate content generation prompt", "error", err)
		return AppOutput{}, err
	}

	// Step 3: Generate content using frontier model
	state.Status = "Generating image..."
	var generationResult GenerateContentOutput
	err = workflow.ExecuteActivity(ctx, GenerateContent, contentGenerationPrompt, input.ModelName, input.ImageFormat, input.ImageWidth, input.ImageHeight).Get(ctx, &generationResult)
	if err != nil {
		logger.Error("Failed to generate content", "error", err)
		return AppOutput{}, err
	}

	// Step 4: Store content in object storage
	state.Status = "Storing image..."
	var storageURL string
	err = workflow.ExecuteActivity(ctx, StoreContent, generationResult.ImageData, input.StorageProvider, input.StorageBucket, input.StorageKey, input.GitHubUsername, generationResult.ContentType).Get(ctx, &storageURL)
	if err != nil {
		logger.Error("Failed to store content", "error", err)
		return AppOutput{}, err
	}

	output := AppOutput{
		GitHubProfile:           githubProfile,
		ContentGenerationPrompt: input.ContentGenerationSystemPrompt,
		ContentURL:              storageURL, // Use storage URL directly
		ImageFormat:             input.ImageFormat,
		ImageWidth:              input.ImageWidth,
		ImageHeight:             input.ImageHeight,
		StorageURL:              storageURL,
		CreatedAt:               time.Now(),
	}

	state.Status = "Completed"
	state.Completed = true
	state.Result = output

	logger.Info("Content generation workflow completed", "storage_url", storageURL)
	return output, nil
}
