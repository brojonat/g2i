package main

import "time"

// AppInput represents the input to the content generation workflow
type AppInput struct {
	GitHubUsername                string `json:"github_username"`
	SystemPrompt                  string `json:"system_prompt"`
	ContentPrompt                 string `json:"content_prompt,omitempty"`         // Optional: if provided, skips prompt generation
	ResearchAgentSystemPrompt     string `json:"research_agent_system_prompt"`     // Prompt for agentic github scraping
	ContentGenerationSystemPrompt string `json:"content_generation_system_prompt"` // Prompt for content generation
	ModelName                     string `json:"model_name"`                       // Frontier model to use
	ImageFormat                   string `json:"image_format,omitempty"`           // e.g., "jpeg", "webp", "png"
	ImageWidth                    int    `json:"image_width,omitempty"`
	ImageHeight                   int    `json:"image_height,omitempty"`
	StorageProvider               string `json:"storage_provider"` // "minio", "s3", "gcs", etc.
	StorageBucket                 string `json:"storage_bucket"`
	StorageKey                    string `json:"storage_key,omitempty"` // Optional: custom storage key
}

// AppOutput represents the output of the content generation workflow
type AppOutput struct {
	GitHubProfile   GitHubProfile `json:"github_profile"`
	GeneratedPrompt string        `json:"generated_prompt"`
	ContentURL      string        `json:"content_url"`
	ImageFormat     string        `json:"image_format,omitempty"`
	ImageWidth      int           `json:"image_width,omitempty"`
	ImageHeight     int           `json:"image_height,omitempty"`
	StorageURL      string        `json:"storage_url,omitempty"`
	CreatedAt       time.Time     `json:"created_at"`
}

// WorkflowState represents the current state of the content generation workflow
type WorkflowState struct {
	Status    string    `json:"status"`
	Result    AppOutput `json:"result"`
	Completed bool      `json:"completed"`
}

// GitHubProfile represents scraped GitHub profile data
type GitHubProfile struct {
	Username          string            `json:"username"`
	Bio               string            `json:"bio"`
	Location          string            `json:"location"`
	Website           string            `json:"website"`
	PublicRepos       int               `json:"public_repos"`
	OriginalRepos     int               `json:"original_repos"`
	ForkedRepos       int               `json:"forked_repos"`
	Languages         []string          `json:"languages"`
	TopRepositories   []Repository      `json:"top_repositories"`
	ContributionGraph ContributionGraph `json:"contribution_graph"`
	ProfessionalScore float64           `json:"professional_score"`
	SafetyFlags       []string          `json:"safety_flags"`
	CodeSnippets      []CodeSnippet     `json:"code_snippets"`
}

// Repository represents a GitHub repository
type Repository struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Language    string    `json:"language"`
	Stars       int       `json:"stars"`
	Forks       int       `json:"forks"`
	UpdatedAt   time.Time `json:"updated_at"`
	IsFork      bool      `json:"is_fork"`
}

// ContributionGraph represents GitHub contribution data
type ContributionGraph struct {
	TotalContributions int            `json:"total_contributions"`
	Streak             int            `json:"streak"`
	Contributions      map[string]int `json:"contributions"` // date -> count
}

// CodeSnippet represents a code snippet from the profile
type CodeSnippet struct {
	Repository string `json:"repository"`
	FilePath   string `json:"file_path"`
	Content    string `json:"content"`
	Language   string `json:"language"`
}
