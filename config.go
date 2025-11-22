package main

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all application configuration loaded from environment variables
type Config struct {
	// Temporal Configuration
	TemporalHost      string
	TemporalNamespace string
	TemporalTaskQueue string

	// Storage Configuration
	StorageProvider string
	StorageBucket   string

	// S3-Compatible Storage Configuration
	S3Platform       string
	S3Endpoint       string
	S3PublicEndpoint string
	S3Region         string
	S3AccessKey      string
	S3SecretKey      string
	S3UseSSL         bool

	// AWS Configuration (for native S3)
	AWSRegion    string
	AWSAccessKey string
	AWSSecretKey string

	// GCS Configuration
	GCSProjectID       string
	GCSCredentialsPath string

	// Google AI Configuration
	GoogleAPIKey string
	GeminiModel  string

	// LLM Orchestrator Configuration
	ResearchOrchestratorAPIKey  string
	ResearchOrchestratorModel   string
	ResearchOrchestratorBaseURL string

	// Image Generation Configuration
	ImageFormat string
	ImageWidth  int
	ImageHeight int

	// Payment Configuration
	ForohtooServerURL  string
	SolanaNetwork      string
	PaymentWalletAddr  string
	PaymentAmount      float64

	// System Prompts
	ResearchAgentPrompt      string
	ContentGenerationPrompt  string
	PollParserPrompt         string

	// Server Configuration
	Port string

	// GitHub Token
	GitHubToken string
}

// LoadConfig loads and validates all required environment variables
func LoadConfig() (*Config, error) {
	cfg := &Config{}
	var errs []string

	// Helper to get required string env var
	getRequired := func(key string) string {
		val := os.Getenv(key)
		if val == "" {
			errs = append(errs, fmt.Sprintf("%s is required", key))
		}
		return val
	}

	// Helper to get optional string env var with default
	getOptional := func(key, defaultVal string) string {
		val := os.Getenv(key)
		if val == "" {
			return defaultVal
		}
		return val
	}

	// Helper to get required int env var
	getRequiredInt := func(key string) int {
		val := os.Getenv(key)
		if val == "" {
			errs = append(errs, fmt.Sprintf("%s is required", key))
			return 0
		}
		intVal, err := strconv.Atoi(val)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s must be a valid integer: %v", key, err))
			return 0
		}
		return intVal
	}

	// Helper to get optional float env var with default
	getOptionalFloat := func(key string, defaultVal float64) float64 {
		val := os.Getenv(key)
		if val == "" {
			return defaultVal
		}
		floatVal, err := strconv.ParseFloat(val, 64)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s must be a valid float: %v", key, err))
			return defaultVal
		}
		return floatVal
	}

	// Temporal Configuration (all required)
	cfg.TemporalHost = getOptional("TEMPORAL_HOST", "localhost:7233")
	cfg.TemporalNamespace = getRequired("TEMPORAL_NAMESPACE")
	cfg.TemporalTaskQueue = getRequired("TEMPORAL_TASK_QUEUE")

	// Storage Configuration (required)
	cfg.StorageProvider = getRequired("STORAGE_PROVIDER")
	cfg.StorageBucket = getRequired("STORAGE_BUCKET")

	// S3-Compatible Storage Configuration
	cfg.S3Platform = getOptional("S3_PLATFORM", "minio")
	cfg.S3Endpoint = os.Getenv("S3_ENDPOINT")
	cfg.S3PublicEndpoint = os.Getenv("S3_PUBLIC_ENDPOINT")
	cfg.S3Region = getOptional("S3_REGION", "us-east-1")
	cfg.S3AccessKey = os.Getenv("S3_ACCESS_KEY")
	cfg.S3SecretKey = os.Getenv("S3_SECRET_KEY")
	cfg.S3UseSSL = os.Getenv("S3_USE_SSL") == "true"

	// AWS Configuration (optional, only needed for native AWS S3)
	cfg.AWSRegion = os.Getenv("AWS_REGION")
	cfg.AWSAccessKey = os.Getenv("AWS_ACCESS_KEY_ID")
	cfg.AWSSecretKey = os.Getenv("AWS_SECRET_ACCESS_KEY")

	// GCS Configuration (optional, only needed for GCS)
	cfg.GCSProjectID = os.Getenv("GCS_PROJECT_ID")
	cfg.GCSCredentialsPath = os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")

	// Google AI Configuration (required)
	cfg.GoogleAPIKey = getRequired("GOOGLE_API_KEY")
	cfg.GeminiModel = getRequired("GEMINI_MODEL")

	// LLM Orchestrator Configuration (required)
	cfg.ResearchOrchestratorAPIKey = getRequired("RESEARCH_ORCHESTRATOR_LLM_API_KEY")
	cfg.ResearchOrchestratorModel = getRequired("RESEARCH_ORCHESTRATOR_LLM_MODEL")
	cfg.ResearchOrchestratorBaseURL = getRequired("RESEARCH_ORCHESTRATOR_LLM_BASE_URL")

	// Image Generation Configuration (required)
	cfg.ImageFormat = getRequired("IMAGE_FORMAT")
	cfg.ImageWidth = getRequiredInt("IMAGE_WIDTH")
	cfg.ImageHeight = getRequiredInt("IMAGE_HEIGHT")

	// Payment Configuration (required)
	cfg.ForohtooServerURL = getRequired("FOROHTOO_SERVER_URL")
	cfg.SolanaNetwork = getOptional("SOLANA_NETWORK", "mainnet")
	cfg.PaymentWalletAddr = getRequired("PAYMENT_WALLET_ADDRESS")
	cfg.PaymentAmount = getOptionalFloat("PAYMENT_AMOUNT", 0.01)

	// System Prompts (required)
	cfg.ResearchAgentPrompt = getRequired("RESEARCH_AGENT_SYSTEM_PROMPT")
	cfg.ContentGenerationPrompt = getRequired("CONTENT_GENERATION_SYSTEM_PROMPT")
	cfg.PollParserPrompt = getRequired("POLL_PARSER_SYSTEM_PROMPT")

	// Server Configuration
	cfg.Port = getOptional("PORT", "8080")

	// GitHub Token (optional for now, but probably should be required)
	cfg.GitHubToken = os.Getenv("GH_TOKEN")

	// If there were any validation errors, return them all at once
	if len(errs) > 0 {
		return nil, fmt.Errorf("configuration validation failed:\n  - %s", joinErrors(errs))
	}

	return cfg, nil
}

// joinErrors joins error messages with newlines and bullet points
func joinErrors(errs []string) string {
	result := ""
	for i, err := range errs {
		if i > 0 {
			result += "\n  - "
		}
		result += err
	}
	return result
}
