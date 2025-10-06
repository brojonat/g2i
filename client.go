package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

// StartWorkflow starts a new content generation workflow
func StartWorkflow(c client.Client, input AppInput) (string, error) {
	workflowOptions := client.StartWorkflowOptions{
		ID:        fmt.Sprintf("content-generation-%s", input.GitHubUsername),
		TaskQueue: os.Getenv("TEMPORAL_TASK_QUEUE"),
	}

	workflowRun, err := c.ExecuteWorkflow(context.Background(), workflowOptions, RunContentGenerationWorkflow, input)
	if err != nil {
		return "", fmt.Errorf("failed to start workflow: %w", err)
	}

	log.Printf("Started workflow with ID: %s", workflowRun.GetID())
	return workflowRun.GetID(), nil
}

// GetWorkflowResult retrieves the result of a workflow
func GetWorkflowResult(c client.Client, workflowID string) (AppOutput, error) {
	var result AppOutput
	err := c.GetWorkflow(context.Background(), workflowID, "").Get(context.Background(), &result)
	if err != nil {
		return AppOutput{}, fmt.Errorf("failed to get workflow result: %w", err)
	}
	return result, nil
}

// QueryWorkflowState queries the current state of a workflow
func QueryWorkflowState(c client.Client, workflowID string) (WorkflowState, error) {
	var state WorkflowState
	resp, err := c.QueryWorkflow(context.Background(), workflowID, "", "getStatus")
	if err != nil {
		return WorkflowState{}, fmt.Errorf("failed to query workflow: %w", err)
	}
	if err := resp.Get(&state); err != nil {
		return WorkflowState{}, fmt.Errorf("failed to decode workflow state: %w", err)
	}
	return state, nil
}

// GetWorkflowDescription gets the description of a workflow execution.
func GetWorkflowDescription(c client.Client, workflowID string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	desc, err := c.DescribeWorkflowExecution(context.Background(), workflowID, "")
	if err != nil {
		return nil, fmt.Errorf("failed to describe workflow: %w", err)
	}
	return desc, nil
}

// StartPollWorkflow starts the poll workflow.
func StartPollWorkflow(c client.Client, workflowID string, config PollConfig) (client.WorkflowRun, error) {
	options := client.StartWorkflowOptions{
		ID:                    workflowID,
		TaskQueue:             os.Getenv("TEMPORAL_TASK_QUEUE"),
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
	}

	we, err := c.ExecuteWorkflow(context.Background(), options, PollWorkflow, config)
	if err != nil {
		return nil, err
	}

	log.Printf("Started workflow with ID: %s, RunID: %s", we.GetID(), we.GetRunID())
	return we, nil
}

// StartPollImageGenerationWorkflow starts the poll image generation workflow.
func StartPollImageGenerationWorkflow(c client.Client, workflowID string, input PollImageGenerationInput) (client.WorkflowRun, error) {
	options := client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: os.Getenv("TEMPORAL_TASK_QUEUE"),
	}

	we, err := c.ExecuteWorkflow(context.Background(), options, GeneratePollImagesWorkflow, input)
	if err != nil {
		return nil, fmt.Errorf("failed to start poll image generation workflow: %w", err)
	}

	log.Printf("Started workflow with ID: %s, RunID: %s", we.GetID(), we.GetRunID())
	return we, nil
}

// QueryPollWorkflow queries a running poll workflow.
func QueryPollWorkflow[T any](c client.Client, workflowID string, queryType string) (T, error) {
	var result T
	resp, err := c.QueryWorkflow(context.Background(), workflowID, "", queryType)
	if err != nil {
		return result, fmt.Errorf("failed to query workflow: %w", err)
	}
	if err := resp.Get(&result); err != nil {
		return result, fmt.Errorf("failed to decode query result: %w", err)
	}
	return result, nil
}

// SignalPollWorkflow sends a signal to a running poll workflow.
func SignalPollWorkflow(c client.Client, workflowID string, signalName string, signalArg interface{}) error {
	err := c.SignalWorkflow(context.Background(), workflowID, "", signalName, signalArg)
	if err != nil {
		return fmt.Errorf("failed to send signal '%s' to workflow: %w", signalName, err)
	}
	return nil
}

// Example usage function
func ExampleUsage() {
	// Create Temporal client
	c, err := client.Dial(client.Options{})
	if err != nil {
		log.Fatalln("Unable to create client", err)
	}
	defer c.Close()

	// Example input
	input := AppInput{
		GitHubUsername:  "octocat",
		SystemPrompt:    "Create a professional visual representation of this developer",
		ModelName:       "dall-e-3",
		StorageProvider: "s3",
		StorageBucket:   "my-content-bucket",
	}

	// Start workflow
	workflowID, err := StartWorkflow(c, input)
	if err != nil {
		log.Fatalln("Failed to start workflow", err)
	}

	// Get result
	result, err := GetWorkflowResult(c, workflowID)
	if err != nil {
		log.Fatalln("Failed to get result", err)
	}

	fmt.Printf("Generated content URL: %s\n", result.ContentURL)
}
