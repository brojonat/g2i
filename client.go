package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

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
		ID:                    workflowID,
		TaskQueue:             os.Getenv("TEMPORAL_TASK_QUEUE"),
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
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
	return QueryPollWorkflowWithContext[T](context.Background(), c, workflowID, queryType)
}

// QueryPollWorkflowWithContext queries a poll workflow with a custom context (for timeouts).
func QueryPollWorkflowWithContext[T any](ctx context.Context, c client.Client, workflowID string, queryType string) (T, error) {
	var result T
	resp, err := c.QueryWorkflow(ctx, workflowID, "", queryType)
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

// CancelWorkflow cancels a running workflow. Returns nil if the workflow doesn't exist or is already completed/canceled.
func CancelWorkflow(c client.Client, workflowID string, reason string) error {
	// First check if the workflow exists and is still running
	desc, err := c.DescribeWorkflowExecution(context.Background(), workflowID, "")
	if err != nil {
		// If workflow doesn't exist, consider it already canceled
		log.Printf("Workflow %s does not exist or cannot be described: %v", workflowID, err)
		return nil
	}

	// Check if workflow is already closed (completed, failed, canceled, terminated)
	if desc.WorkflowExecutionInfo.Status != enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
		log.Printf("Workflow %s is not running (status: %s), skipping cancellation", workflowID, desc.WorkflowExecutionInfo.Status)
		return nil
	}

	// Cancel the workflow
	err = c.CancelWorkflow(context.Background(), workflowID, "")
	if err != nil {
		return fmt.Errorf("failed to cancel workflow: %w", err)
	}

	log.Printf("Successfully canceled workflow %s: %s", workflowID, reason)
	return nil
}

// UpdatePollWorkflow sends an update to a running poll workflow and returns the result.
func UpdatePollWorkflow[R any](c client.Client, workflowID string, updateName string, updateArg interface{}) (R, error) {
	var result R
	// Note: using a long poll context here to ensure we wait for the result.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	updateOptions := client.UpdateWorkflowOptions{
		WorkflowID:   workflowID,
		RunID:        "", // empty string means latest run
		UpdateName:   updateName,
		Args:         []interface{}{updateArg},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	}

	updateHandle, err := c.UpdateWorkflow(ctx, updateOptions)
	if err != nil {
		return result, fmt.Errorf("failed to send update to workflow: %w", err)
	}
	err = updateHandle.Get(ctx, &result)
	if err != nil {
		return result, fmt.Errorf("failed to get update result: %w", err)
	}
	return result, nil
}

// TerminateWorkflow terminates a workflow execution. This allows the workflow ID to be reused.
// Returns nil if the workflow doesn't exist or is already in a closed state (completed, failed, canceled, terminated).
func TerminateWorkflow(c client.Client, workflowID string, reason string) error {
	// First check if the workflow exists
	desc, err := c.DescribeWorkflowExecution(context.Background(), workflowID, "")
	if err != nil {
		// If workflow doesn't exist, nothing to terminate
		log.Printf("Workflow %s does not exist or cannot be described: %v", workflowID, err)
		return nil
	}

	// Check if workflow is already in a closed state (completed, failed, canceled, terminated, timed out, continued-as-new)
	status := desc.WorkflowExecutionInfo.Status
	if status != enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
		log.Printf("Workflow %s is already closed (status: %s), skipping termination", workflowID, status)
		return nil
	}

	// Terminate the workflow
	err = c.TerminateWorkflow(context.Background(), workflowID, "", reason)
	if err != nil {
		return fmt.Errorf("failed to terminate workflow: %w", err)
	}

	log.Printf("Successfully terminated workflow %s: %s", workflowID, reason)
	return nil
}

// PollListItem represents a poll in the list view
type PollListItem struct {
	WorkflowID string
	Question   string
	StartTime  time.Time
	Status     string
	VoteCount  int
}

// ListPollWorkflows lists all poll workflows
func ListPollWorkflows(c client.Client, pageSize int) ([]PollListItem, error) {
	// Add timeout to prevent slow queries from blocking indefinitely
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Query for RUNNING poll workflows only, sorted by start time descending (most recent first)
	query := "WorkflowType='PollWorkflow' AND ExecutionStatus='Running' ORDER BY StartTime DESC"

	var polls []PollListItem

	// Only fetch the first page to avoid querying too many workflows
	resp, err := c.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
		PageSize: int32(pageSize),
		Query:    query,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list workflows: %w", err)
	}

	for _, exec := range resp.Executions {
		// Only use data from the list response - no additional queries!
		// This makes the page load instantly instead of doing N+1 queries.
		// Question and VoteCount will be shown on the poll detail page.
		poll := PollListItem{
			WorkflowID: exec.Execution.WorkflowId,
			StartTime:  exec.StartTime.AsTime(),
			Status:     exec.Status.String(),
		}
		polls = append(polls, poll)
	}

	return polls, nil
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
