package main

import (
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
	state.Status = "Scraping GitHub profile..."
	var githubProfile GitHubProfile
	err = workflow.ExecuteActivity(ctx, ScrapeGitHubProfile, input.GitHubUsername).Get(ctx, &githubProfile)
	if err != nil {
		logger.Error("Failed to scrape GitHub profile", "error", err)
		return AppOutput{}, err
	}

	// Step 2: Generate prompt (skip if provided in input)
	state.Status = "Generating prompt..."
	var prompt string
	if input.ContentPrompt != "" {
		prompt = input.ContentPrompt
		logger.Info("Using provided content prompt")
	} else {
		err = workflow.ExecuteActivity(ctx, GeneratePrompt, githubProfile, input.SystemPrompt).Get(ctx, &prompt)
		if err != nil {
			logger.Error("Failed to generate prompt", "error", err)
			return AppOutput{}, err
		}
	}

	// Step 3: Generate content using frontier model
	state.Status = "Generating image..."
	var generationResult GenerateContentOutput
	err = workflow.ExecuteActivity(ctx, GenerateContent, prompt, input.ModelName, input.ImageFormat, input.ImageWidth, input.ImageHeight).Get(ctx, &generationResult)
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
		GitHubProfile:   githubProfile,
		GeneratedPrompt: prompt,
		ContentURL:      storageURL, // Use storage URL directly
		ImageFormat:     input.ImageFormat,
		ImageWidth:      input.ImageWidth,
		ImageHeight:     input.ImageHeight,
		StorageURL:      storageURL,
		CreatedAt:       time.Now(),
	}

	state.Status = "Completed"
	state.Completed = true
	state.Result = output

	logger.Info("Content generation workflow completed", "storage_url", storageURL)
	return output, nil
}
