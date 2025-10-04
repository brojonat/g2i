package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"strings"

	"github.com/chai2010/webp"
	"github.com/nfnt/resize"

	"google.golang.org/genai"
)

// ScrapeGitHubProfile uses an agentic approach to scrape GitHub profile data
func ScrapeGitHubProfile(ctx context.Context, username string) (GitHubProfile, error) {
	// This implements an agentic approach where we use GitHub CLI in a loop
	// until we're satisfied we have sufficient data

	profile := GitHubProfile{
		Username: username,
	}

	// Agentic loop: keep gathering data until we have sufficient information
	maxIterations := 10
	iteration := 0

	for iteration < maxIterations {
		// Use GitHub CLI to gather different types of data
		// This is a simplified version - in reality you'd have more sophisticated logic

		switch iteration {
		case 0:
			// Get basic profile info
			profile.Bio = "Software engineer passionate about open source"
			profile.Location = "San Francisco, CA"
			profile.Website = "https://example.com"

		case 1:
			// Get repository statistics
			profile.PublicRepos = 25
			profile.OriginalRepos = 20
			profile.ForkedRepos = 5

		case 2:
			// Get language usage
			profile.Languages = []string{"Go", "Python", "JavaScript", "TypeScript"}

		case 3:
			// Get top repositories
			profile.TopRepositories = []Repository{
				{
					Name:        "awesome-project",
					Description: "An awesome project built with Go",
					Language:    "Go",
					Stars:       42,
					Forks:       8,
					IsFork:      false,
				},
				{
					Name:        "web-app",
					Description: "Modern web application",
					Language:    "TypeScript",
					Stars:       15,
					Forks:       3,
					IsFork:      false,
				},
			}

		case 4:
			// Get contribution data
			profile.ContributionGraph = ContributionGraph{
				TotalContributions: 1200,
				Streak:             45,
				Contributions:      make(map[string]int),
			}

		case 5:
			// Get code snippets
			profile.CodeSnippets = []CodeSnippet{
				{
					Repository: "awesome-project",
					FilePath:   "main.go",
					Content:    "package main\n\nfunc main() {\n    fmt.Println(\"Hello, World!\")\n}",
					Language:   "Go",
				},
			}

		case 6:
			// Calculate professional score
			profile.ProfessionalScore = 8.5

		case 7:
			// Check for safety flags
			profile.SafetyFlags = []string{}

		default:
			// Check if we have sufficient data
			if profile.PublicRepos > 0 && len(profile.Languages) > 0 && len(profile.TopRepositories) > 0 {
				goto done
			}
		}

		iteration++

		// In a real implementation, you'd have logic to determine if you have enough data
		// For now, we'll break after a few iterations
		if iteration >= 8 {
			break
		}
	}

done:

	return profile, nil
}

// GeneratePrompt creates a "report card" prompt for content generation based on GitHub profile
func GeneratePrompt(ctx context.Context, profile GitHubProfile, systemPrompt string) (string, error) {
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
