package main

import (
	"fmt"
	"strings"
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
	cwo := workflow.ChildWorkflowOptions{
		WorkflowID: "agentic-scrape-" + input.GitHubUsername,
	}
	childCtx := workflow.WithChildOptions(ctx, cwo)
	err = workflow.ExecuteChildWorkflow(childCtx, AgenticScrapeGitHubProfileWorkflow, agentSystemPrompt).Get(childCtx, &githubProfile)
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
	var generationResult GenerationResult
	err = workflow.ExecuteActivity(ctx, GenerateContent, contentGenerationPrompt, input.ModelName, input.ImageFormat, input.ImageWidth, input.ImageHeight).Get(ctx, &generationResult)
	if err != nil {
		logger.Error("Failed to generate content", "error", err)
		return AppOutput{}, err
	}
	logger.Info("Content generation completed successfully.")

	// Store the generated content
	logger.Info("Storing content...")
	var storeOutput StoreContentOutput
	storagePrefix := input.GitHubUsername
	err = workflow.ExecuteActivity(ctx, StoreContent, generationResult.ImageData, input.StorageProvider, input.StorageBucket, input.StorageKey, storagePrefix, generationResult.ContentType).Get(ctx, &storeOutput)
	if err != nil {
		logger.Error("Failed to store content", "error", err)
		return AppOutput{}, err
	}
	generationResult.PublicURL = storeOutput.PublicURL
	generationResult.StorageKey = storeOutput.StorageKey

	output := AppOutput{
		GitHubProfile:           githubProfile,
		ContentGenerationPrompt: input.ContentGenerationSystemPrompt,
		ContentURL:              generationResult.PublicURL,
		ImageFormat:             input.ImageFormat,
		ContentType:             generationResult.ContentType,
		ImageWidth:              input.ImageWidth,
		ImageHeight:             input.ImageHeight,
		StorageURL:              generationResult.PublicURL,
		StorageKey:              generationResult.StorageKey,
		CreatedAt:               time.Now(),
	}

	state.Status = "Completed"
	state.Completed = true
	state.Result = output

	logger.Info("Content generation workflow completed", "storage_url", generationResult.PublicURL)
	return output, nil
}

// GeneratePollImagesWorkflow manages the generation of images for a poll.
func GeneratePollImagesWorkflow(ctx workflow.Context, input PollImageGenerationInput) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting poll image generation workflow", "PollID", input.PollID, "UserCount", len(input.Usernames))

	cwo := workflow.ChildWorkflowOptions{}
	ctx = workflow.WithChildOptions(ctx, cwo)

	var futures []workflow.Future
	for _, username := range input.Usernames {
		// Start the content generation workflow for each user.
		childInput := input.AppInput
		childInput.GitHubUsername = username

		childWorkflowFuture := workflow.ExecuteChildWorkflow(ctx, RunContentGenerationWorkflow, childInput)
		futures = append(futures, childWorkflowFuture)
	}

	for _, future := range futures {
		var childOutput AppOutput
		if err := future.Get(ctx, &childOutput); err != nil {
			logger.Error("Child workflow failed", "error", err)
			// Decide if one failure should fail the whole workflow. For now, we'll just log and continue.
			continue
		}

		// The image is now generated and stored under the user's "folder".
		// Now, copy it to the poll's "folder".
		if childOutput.StorageKey == "" {
			logger.Warn("Child workflow did not return a storage key")
			continue
		}
		childInput := input.AppInput
		if !strings.Contains(childOutput.ContentType, "/") {
			logger.Warn("Child workflow returned invalid content type", "ContentType", childOutput.ContentType)
			continue
		}
		fileExtension := strings.Split(childOutput.ContentType, "/")[1]
		destKey := fmt.Sprintf("%s/%s.%s", input.PollID, childOutput.GitHubProfile.Username, fileExtension)

		copyActivityInput := CopyObjectInput{
			SourceBucket:      childInput.StorageBucket,
			SourceKey:         childOutput.StorageKey,
			DestinationBucket: childInput.StorageBucket,
			DestinationKey:    destKey,
			StorageProvider:   childInput.StorageProvider,
		}

		err := workflow.ExecuteActivity(ctx, CopyObject, copyActivityInput).Get(ctx, nil)
		if err != nil {
			logger.Error("Failed to copy image to poll folder", "DestinationKey", destKey, "error", err)
			// Again, decide on error handling. Continuing for now.
		} else {
			logger.Info("Successfully copied image to poll folder", "DestinationKey", destKey)
		}
	}

	logger.Info("Poll image generation workflow finished.")
	return nil
}
