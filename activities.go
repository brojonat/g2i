package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/brojonat/forohtoo/client"
	"github.com/chai2010/webp"
	"github.com/nfnt/resize"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"google.golang.org/genai"
)

const (
	// USDCMintAddress is the USDC token mint address on Solana mainnet
	USDCMintAddress = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
)

// GenerateResponsesTurnInput holds the parameters for the GenerateResponsesTurnActivity.
type GenerateResponsesTurnInput struct {
	OpenAIConfig       OpenAIConfig
	PreviousResponseID string
	UserInput          string
	Tools              []Tool
	FunctionOutputs    map[string]string
	ToolChoice         any
}

// ExecuteGhCommandActivity is an activity that executes a GitHub CLI command.
func ExecuteGhCommandActivity(ctx context.Context, command string) (string, error) {
	output, err := executeGhCommand(ctx, command)
	if err != nil {
		var exitErr *exec.ExitError
		// Check if the error is an ExitError, which indicates the command ran but failed.
		// These are business logic failures (e.g., bad command) that shouldn't be retried.
		if errors.As(err, &exitErr) {
			// Forward the error message from stderr back to the agent as a non-retryable error.
			return "", temporal.NewNonRetryableApplicationError(err.Error(), "GhCommandExecutionError", nil)
		}
		// For other errors (e.g., command not found, context cancelled), let Temporal retry.
		return "", err
	}
	return output, nil
}

// GenerateResponsesTurnActivity is an activity that generates a turn in the agentic conversation.
func GenerateResponsesTurnActivity(ctx context.Context, input GenerateResponsesTurnInput) (GenerateResponsesTurnResult, error) {
	text, calls, id, err := generateResponsesTurn(ctx, input.OpenAIConfig, input.PreviousResponseID, input.UserInput, input.Tools, input.FunctionOutputs, input.ToolChoice)
	if err != nil {
		return GenerateResponsesTurnResult{}, err
	}
	return GenerateResponsesTurnResult{Assistant: text, Calls: calls, ID: id}, nil
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
	APIHost   string
}

// CopyObjectInput defines the input for the CopyObject activity.
type CopyObjectInput struct {
	StorageProvider   string
	SourceBucket      string
	SourceKey         string
	DestinationBucket string
	DestinationKey    string
}

// GeneratePrompt creates a "report card" prompt for content generation based on GitHub profile
func GenerateContentGenerationPrompt(ctx context.Context, profile GitHubProfile, systemPrompt string) (string, error) {
	// Build a comprehensive "report card" prompt that grounds the profile in cultural context
	prompt := fmt.Sprintf(`%s
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
`,
		systemPrompt,
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
func GenerateContent(ctx context.Context, prompt, modelName, imageFormat string, imageWidth, imageHeight int) (GenerationResult, error) {
	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		return GenerationResult{}, fmt.Errorf("GOOGLE_API_KEY environment variable not set")
	}

	// Initialize Gemini client. It will use the GOOGLE_API_KEY environment variable if it is set.
	client, err := genai.NewClient(ctx, nil)
	if err != nil {
		return GenerationResult{}, fmt.Errorf("failed to create genai client: %w", err)
	}

	// Generate the image
	result, err := client.Models.GenerateContent(
		ctx,
		modelName,
		genai.Text(prompt),
		nil,
	)
	if err != nil {
		return GenerationResult{}, fmt.Errorf("failed to generate content: %w", err)
	}

	if len(result.Candidates) == 0 || result.Candidates[0].Content == nil || len(result.Candidates[0].Content.Parts) == 0 {
		return GenerationResult{}, fmt.Errorf("no content returned from API")
	}

	var originalImageData []byte
	for _, part := range result.Candidates[0].Content.Parts {
		if part.InlineData != nil {
			originalImageData = part.InlineData.Data
			break
		}
	}

	if originalImageData == nil {
		return GenerationResult{}, fmt.Errorf("no image data returned")
	}

	// If no format or dimensions are specified, return the original image
	if imageFormat == "" && imageWidth == 0 && imageHeight == 0 {
		return GenerationResult{
			ImageData:   originalImageData,
			ContentType: "image/png", // Assuming default is png
		}, nil
	}

	img, _, err := image.Decode(bytes.NewReader(originalImageData))
	if err != nil {
		return GenerationResult{}, fmt.Errorf("failed to decode image: %w", err)
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
		return GenerationResult{
			ImageData:   originalImageData,
			ContentType: "image/png",
		}, nil
	}

	if err != nil {
		return GenerationResult{}, fmt.Errorf("failed to encode image to %s: %w", imageFormat, err)
	}

	return GenerationResult{
		ImageData:   buf.Bytes(),
		ContentType: contentType,
	}, nil
}

// CopyObject copies an object from one location to another in the object storage.
func CopyObject(ctx context.Context, input CopyObjectInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Copying object", "from", input.SourceKey, "to", input.DestinationKey)

	storage := NewObjectStorage(input.StorageProvider)

	err := storage.Copy(ctx, input.SourceBucket, input.SourceKey, input.DestinationBucket, input.DestinationKey)
	if err != nil {
		logger.Error("Failed to copy object", "error", err)
		return fmt.Errorf("failed to copy object: %w", err)
	}

	logger.Info("Successfully copied object")
	return nil
}

// StoreContentOutput is the output from the StoreContent activity.
type StoreContentOutput struct {
	PublicURL   string
	StorageKey  string
	ContentType string
}

// StoreContent stores content in object storage using the generic interface
func StoreContent(ctx context.Context, data []byte, provider, bucket, key, keyPrefix, contentType string) (StoreContentOutput, error) {
	if provider == "" {
		// This case should be handled by the caller; if no provider, don't call this.
		// For now, we'll return an error.
		return StoreContentOutput{}, fmt.Errorf("storage provider cannot be empty")
	}

	// Generate a key if none provided
	if key == "" {
		key = generateStorageKey(keyPrefix, contentType)
	}

	// Create storage instance
	storage := NewObjectStorage(provider)

	// Store the content
	publicURL, err := storage.Store(ctx, data, bucket, key, contentType)
	if err != nil {
		return StoreContentOutput{}, err
	}

	return StoreContentOutput{
		PublicURL:   publicURL,
		StorageKey:  key,
		ContentType: contentType,
	}, nil
}

// WaitForPaymentInput defines the input for the WaitForPayment activity.
type WaitForPaymentInput struct {
	ForohtooServerURL string  // URL of the forohtoo server
	PaymentWallet     string  // Solana wallet address to monitor
	Network           string  // Solana network ("mainnet" or "devnet")
	WorkflowID        string  // Workflow ID to match in transaction memo
	ExpectedAmount    float64 // Expected payment amount in USDC
	AssetType         string  // Asset type (e.g., "spl-token")
	TokenMint         string  // Token mint address (e.g., USDC mint)
}

// WaitForPaymentOutput defines the output from the WaitForPayment activity.
type WaitForPaymentOutput struct {
	TransactionID string // Solana transaction ID
	Amount        float64
	Memo          string
}

// WaitForPayment waits for a Solana payment transaction to arrive via forohtoo.
func WaitForPayment(ctx context.Context, input WaitForPaymentInput) (WaitForPaymentOutput, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Waiting for payment", "wallet", input.PaymentWallet, "workflowID", input.WorkflowID)

	// Create forohtoo client
	fmt.Println("Creating forohtoo client", "url", input.ForohtooServerURL, "network", input.Network)
	cl := client.NewClient(input.ForohtooServerURL, nil, slog.Default())

	// Register the wallet to track the specific asset (token mint)
	// NOTE: the current implementation of the forohtoo client requires a poll interval of at least 1 minute
	err := cl.RegisterAsset(ctx, input.PaymentWallet, input.Network, input.AssetType, input.TokenMint, 1*time.Minute)
	if err != nil {
		logger.Error("Failed to register wallet asset", "error", err, "assetType", input.AssetType, "tokenMint", input.TokenMint)
		return WaitForPaymentOutput{}, fmt.Errorf("failed to register wallet asset: %w", err)
	}

	// Wait for a transaction that matches the workflow ID in the memo
	txn, err := cl.Await(ctx, input.PaymentWallet, input.Network, 24*time.Hour, func(txn *client.Transaction) bool {
		// Convert expected amount from full USDC units to smallest unit (micro-USDC)
		// 1 USDC = 1,000,000 micro-USDC (6 decimals)
		expectedAmountInSmallestUnit := int64(input.ExpectedAmount * 1_000_000)
		// Check if the transaction memo contains the workflow ID and amount matches
		return strings.Contains(txn.Memo, input.WorkflowID) && txn.Amount == expectedAmountInSmallestUnit
	})

	if err != nil {
		logger.Error("Failed to receive payment", "error", err)
		return WaitForPaymentOutput{}, fmt.Errorf("failed to receive payment: %w", err)
	}

	logger.Info("Payment received", "transactionID", txn.Signature, "amount", txn.Amount)

	return WaitForPaymentOutput{
		TransactionID: txn.Signature,
		Amount:        float64(txn.Amount),
		Memo:          txn.Memo,
	}, nil
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
