package main

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/skip2/go-qrcode"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"golang.org/x/exp/errors"
)

const (
	// MaxPollRequestLength defines the maximum allowed length for a poll request.
	MaxPollRequestLength = 2048
	// MaxGitHubUsernameLength defines the maximum allowed length for a GitHub username.
	MaxGitHubUsernameLength = 39
	// MaxModelNameLength defines the maximum allowed length for a model name.
	MaxModelNameLength = 100
	// MaxWorkflowIDLength defines the maximum allowed length for a workflow ID.
	MaxWorkflowIDLength = 256
	// MaxOptionLength defines the maximum allowed length for a poll option.
	MaxOptionLength = 100
)

//go:embed all:static
var staticFS embed.FS

//go:embed all:templates
var templateFS embed.FS

// TemplateRenderer handles HTML template rendering.
type TemplateRenderer struct {
	templates map[string]*template.Template
	logger    *slog.Logger
}

// NewTemplateRenderer creates a new template renderer and loads all templates.
func NewTemplateRenderer(logger *slog.Logger) (*TemplateRenderer, error) {
	r := &TemplateRenderer{
		templates: make(map[string]*template.Template),
		logger:    logger,
	}

	// Load all templates using the same pattern as the original implementation
	var err error
	r.templates["index"], err = template.ParseFS(templateFS, "templates/base.html", "templates/index.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse index template: %w", err)
	}

	r.templates["workflow-status"], err = template.ParseFS(templateFS, "templates/base.html", "templates/workflow-status.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse workflow-status template: %w", err)
	}

	r.templates["workflow-details"], err = template.ParseFS(templateFS, "templates/base.html", "templates/workflow-details.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse workflow-details template: %w", err)
	}

	r.templates["error"], err = template.ParseFS(templateFS, "templates/base.html", "templates/error.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse error template: %w", err)
	}

	r.templates["poll-form"], err = template.ParseFS(templateFS, "templates/base.html", "templates/poll-form.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse poll-form template: %w", err)
	}

	r.templates["poll-details"], err = template.ParseFS(templateFS, "templates/base.html", "templates/poll-details.html", "templates/poll-results-partial.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse poll-details template: %w", err)
	}

	r.templates["poll-results-partial"], err = template.ParseFS(templateFS, "templates/poll-results-partial.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse poll-results-partial template: %w", err)
	}

	r.templates["generate-form"], err = template.ParseFS(templateFS, "templates/base.html", "templates/generate-form.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse generate-form template: %w", err)
	}

	r.templates["spinner-partial"], err = template.ParseFS(templateFS, "templates/spinner-partial.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse spinner-partial template: %w", err)
	}

	r.templates["image-partial"], err = template.ParseFS(templateFS, "templates/image-partial.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse image-partial template: %w", err)
	}

	r.templates["votes-partial"], err = template.ParseFS(templateFS, "templates/votes-partial.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse votes-partial template: %w", err)
	}

	r.templates["poll-list"], err = template.ParseFS(templateFS, "templates/base.html", "templates/poll-list.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse poll-list template: %w", err)
	}

	return r, nil
}

// RenderWithRequest renders a template with HTMX support.
func (r *TemplateRenderer) RenderWithRequest(w http.ResponseWriter, req *http.Request, name string, data interface{}) error {
	tmpl, ok := r.templates[name]
	if !ok {
		return fmt.Errorf("template not found: %s", name)
	}

	isHTMX := req.Header.Get("HX-Request") == "true"
	if isHTMX {
		block := "content"
		if tmpl.Lookup(name) != nil {
			block = name
		}
		return tmpl.ExecuteTemplate(w, block, data)
	}

	return tmpl.ExecuteTemplate(w, "base.html", data)
}

// APIServer for handling HTTP requests
type APIServer struct {
	temporalClient  client.Client
	storageProvider ObjectStorage
	renderer        *TemplateRenderer
	logger          *slog.Logger
	server          *http.Server
}

// NewAPIServer creates a new API server
func NewAPIServer(temporalClient client.Client, storageProvider ObjectStorage) *APIServer {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	renderer, err := NewTemplateRenderer(logger)
	if err != nil {
		log.Fatalf("Failed to create template renderer: %v", err)
	}

	return &APIServer{
		temporalClient:  temporalClient,
		storageProvider: storageProvider,
		renderer:        renderer,
		logger:          logger,
	}
}

// SetupRoutes sets up the API routes and returns the configured server
func (s *APIServer) SetupRoutes() *APIServer {
	mux := http.NewServeMux()

	// Serve static files - use fs.Sub to get the "static" subdirectory
	staticSubFS, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("Failed to create static sub-filesystem: %v", err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSubFS))))

	// Home page
	mux.Handle("GET /", s.handleHomePage())
	mux.Handle("GET /ping", s.handlePing())

	// Workflow routes
	mux.Handle("POST /generate", s.handleStartContentGeneration())
	mux.Handle("GET /generate-form", s.handleGetGenerateForm())
	mux.Handle("GET /workflow/{id}/status", s.handleGetWorkflowStatus())
	mux.Handle("GET /workflow/{id}", s.handleGetWorkflowDetails())
	mux.Handle("GET /profile/{username}", s.handleGetProfilePage())

	// Poll routes
	mux.Handle("GET /polls", s.handleListPolls())
	mux.Handle("GET /poll/new", s.handleShowPollForm())
	mux.Handle("POST /poll", s.handleCreatePoll())
	mux.Handle("GET /poll/{id}", s.handleGetPollDetails())
	mux.Handle("GET /poll/{id}/results", s.handleGetPollResults())
	mux.Handle("POST /poll/{id}/vote", s.handleVoteOnPoll())
	mux.Handle("DELETE /poll/{id}", s.handleDeletePoll())
	mux.Handle("GET /poll/{id}/profile/{option}", s.handleGetPollProfile())
	mux.Handle("GET /poll/{id}/votes/{option}", s.handleGetPollVotes())

	// Visualization routes
	mux.Handle("GET /visualization-form", s.handleGetVisualizationForm())

	// Wrap with middleware (order matters: outer middleware runs first)
	handler := s.recoveryMiddleware(
		s.loggingMiddleware(
			s.corsMiddleware(mux),
		),
	)

	s.server = &http.Server{
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s
}

// Start starts the HTTP server
func (s *APIServer) Start(addr string) error {
	s.server.Addr = addr
	s.logger.Info("starting HTTP server", "addr", addr)
	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the HTTP server
func (s *APIServer) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down HTTP server")
	return s.server.Shutdown(ctx)
}

// getEnvB64 reads a base64-encoded environment variable and returns the decoded string.
func getEnvB64(key string) string {
	val := os.Getenv(key)
	if val == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(val)
	if err != nil {
		return ""
	}
	return string(decoded)
}

// sanitizeWorkflowID sanitizes a string for use as a workflow ID.
func sanitizeWorkflowID(input string) string {
	reg := regexp.MustCompile(`[^a-zA-Z0-9-_]+`)
	sanitized := reg.ReplaceAllString(input, "-")
	sanitized = strings.ToLower(strings.Trim(sanitized, "-"))
	const maxLength = 200
	if len(sanitized) > maxLength {
		sanitized = sanitized[:maxLength]
	}
	return sanitized
}

// GenerateRequest defines the expected input from the client
type GenerateRequest struct {
	GitHubUsername string `form:"github_username"`
	ModelName      string `form:"model_name"`
}

// Middleware functions

// loggingMiddleware logs all HTTP requests and responses.
func (s *APIServer) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		s.logger.Debug("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
		)

		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(wrapped, r)

		duration := time.Since(start)
		level := slog.LevelDebug
		if wrapped.statusCode >= 500 {
			level = slog.LevelError
		} else if wrapped.statusCode >= 400 {
			level = slog.LevelInfo
		}

		s.logger.Log(r.Context(), level, "http response",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration_ms", duration.Milliseconds(),
		)
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// corsMiddleware adds CORS headers to all responses.
func (s *APIServer) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// recoveryMiddleware recovers from panics and logs them.
func (s *APIServer) recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				s.logger.Error("panic recovered",
					"error", err,
					"path", r.URL.Path,
					"stack", string(debug.Stack()),
				)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// Response helper methods

// writeJSON writes a JSON response with the given status code.
func (s *APIServer) writeJSON(w http.ResponseWriter, data interface{}, statusCode int) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	return json.NewEncoder(w).Encode(data)
}

// renderError renders an error template with the given message and status code.
func (s *APIServer) renderError(w http.ResponseWriter, r *http.Request, message string, statusCode int) {
	data := map[string]interface{}{
		"error": message,
	}
	w.WriteHeader(statusCode)
	if err := s.renderer.RenderWithRequest(w, r, "error", data); err != nil {
		s.logger.Error("failed to render error template", "error", err)
		http.Error(w, message, statusCode)
	}
}

// renderErrorWithRedirect renders an error template with a redirect.
func (s *APIServer) renderErrorWithRedirect(w http.ResponseWriter, r *http.Request, message, redirectURL string, timeout int, statusCode int) {
	data := map[string]interface{}{
		"error":           message,
		"RedirectURL":     redirectURL,
		"RedirectTimeout": timeout,
	}
	w.WriteHeader(statusCode)
	if err := s.renderer.RenderWithRequest(w, r, "error", data); err != nil {
		s.logger.Error("failed to render error template", "error", err)
		http.Error(w, message, statusCode)
	}
}

// writeBadRequest writes a 400 Bad Request error response.
func (s *APIServer) writeBadRequest(w http.ResponseWriter, r *http.Request, message string) {
	s.renderError(w, r, message, http.StatusBadRequest)
}

// writeInternalError writes a 500 Internal Server Error response.
func (s *APIServer) writeInternalError(w http.ResponseWriter, r *http.Request, message string) {
	s.renderError(w, r, message, http.StatusInternalServerError)
}

// writeNotFound writes a 404 Not Found error response with redirect.
func (s *APIServer) writeNotFound(w http.ResponseWriter, r *http.Request, message string) {
	s.renderErrorWithRedirect(w, r, message, "/", 5, http.StatusNotFound)
}

// JSON response helpers

// writeOK writes a 200 OK JSON response.
func (s *APIServer) writeOK(w http.ResponseWriter, data interface{}) {
	if err := s.writeJSON(w, data, http.StatusOK); err != nil {
		s.logger.Error("failed to write JSON response", "error", err)
	}
}

// writeJSONBadRequest writes a 400 Bad Request JSON error response.
func (s *APIServer) writeJSONBadRequest(w http.ResponseWriter, message string) {
	if err := s.writeJSON(w, map[string]string{"error": message}, http.StatusBadRequest); err != nil {
		s.logger.Error("failed to write JSON response", "error", err)
	}
}

// writeJSONInternalError writes a 500 Internal Server Error JSON response.
func (s *APIServer) writeJSONInternalError(w http.ResponseWriter, message string) {
	if err := s.writeJSON(w, map[string]string{"error": message}, http.StatusInternalServerError); err != nil {
		s.logger.Error("failed to write JSON response", "error", err)
	}
}

// Handler functions

// handleHomePage renders the home page.
func (s *APIServer) handleHomePage() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle 404 for non-root paths
		if r.URL.Path != "/" {
			s.writeNotFound(w, r, "Page not found. You will be redirected to the homepage.")
			return
		}

		data := map[string]interface{}{
			"Title": "Vibe Check",
		}
		if err := s.renderer.RenderWithRequest(w, r, "index", data); err != nil {
			s.logger.Error("failed to render template", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	})
}

// handlePing is a simple health check endpoint.
func (s *APIServer) handlePing() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pong"))
	})
}

// handleGetGenerateForm renders the meme generation form.
func (s *APIServer) handleGetGenerateForm() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := s.renderer.RenderWithRequest(w, r, "generate-form", nil); err != nil {
			s.logger.Error("failed to render template", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	})
}

// handleGetVisualizationForm renders the visualization form partial.
func (s *APIServer) handleGetVisualizationForm() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := s.renderer.RenderWithRequest(w, r, "visualization-form", nil); err != nil {
			s.logger.Error("failed to render template", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	})
}

// handleStartContentGeneration handles POST /generate
func (s *APIServer) handleStartContentGeneration() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			s.writeBadRequest(w, r, err.Error())
			return
		}

		githubUsername := r.FormValue("github_username")
		modelName := r.FormValue("model_name")

		if len(githubUsername) > MaxGitHubUsernameLength {
			s.writeBadRequest(w, r, "GitHub username is too long.")
			return
		}
		if len(modelName) > MaxModelNameLength {
			s.writeBadRequest(w, r, "Model name is too long.")
			return
		}

		width, err := strconv.Atoi(os.Getenv("IMAGE_WIDTH"))
		if err != nil {
			s.writeInternalError(w, r, err.Error())
			return
		}
		height, err := strconv.Atoi(os.Getenv("IMAGE_HEIGHT"))
		if err != nil {
			s.writeInternalError(w, r, err.Error())
			return
		}

		input := AppInput{
			GitHubUsername:                githubUsername,
			ModelName:                     modelName,
			ResearchAgentSystemPrompt:     getEnvB64("RESEARCH_AGENT_SYSTEM_PROMPT"),
			ContentGenerationSystemPrompt: getEnvB64("CONTENT_GENERATION_SYSTEM_PROMPT"),
			StorageProvider:               os.Getenv("STORAGE_PROVIDER"),
			StorageBucket:                 os.Getenv("STORAGE_BUCKET"),
			ImageFormat:                   os.Getenv("IMAGE_FORMAT"),
			ImageWidth:                    width,
			ImageHeight:                   height,
		}

		if input.ModelName == "" {
			input.ModelName = os.Getenv("GEMINI_MODEL")
		}

		_, err = StartWorkflow(s.temporalClient, input)
		if err != nil {
			s.writeInternalError(w, r, err.Error())
			return
		}

		w.Header().Set("HX-Redirect", "/profile/"+githubUsername)
		w.WriteHeader(http.StatusOK)
	})
}

// handleGetWorkflowStatus handles GET /workflow/{id}/status
func (s *APIServer) handleGetWorkflowStatus() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workflowID := r.PathValue("id")
		if len(workflowID) > MaxWorkflowIDLength {
			s.writeBadRequest(w, r, "Invalid workflow ID.")
			return
		}

		s.logger.Debug("checking status for workflow", "workflow_id", workflowID)

		state, err := QueryWorkflowState(s.temporalClient, workflowID)
		if err != nil {
			s.logger.Error("error getting workflow result", "workflow_id", workflowID, "error", err)
			s.writeInternalError(w, r, err.Error())
			return
		}

		s.logger.Debug("successfully retrieved status", "workflow_id", workflowID)

		if state.Completed {
			w.Header().Set("HX-Retarget", "#workflow-status")
		}

		if err := s.renderer.RenderWithRequest(w, r, "workflow-details", state); err != nil {
			s.logger.Error("failed to render template", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	})
}

// handleGetWorkflowDetails handles GET /workflow/{id}
func (s *APIServer) handleGetWorkflowDetails() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workflowID := r.PathValue("id")
		if len(workflowID) > MaxWorkflowIDLength {
			s.writeBadRequest(w, r, "Invalid workflow ID.")
			return
		}

		s.logger.Debug("getting details for workflow", "workflow_id", workflowID)

		result, err := GetWorkflowResult(s.temporalClient, workflowID)
		if err != nil {
			s.logger.Error("error getting workflow result", "workflow_id", workflowID, "error", err)
			s.writeInternalError(w, r, err.Error())
			return
		}

		state := WorkflowState{
			Status:    "Completed",
			Completed: true,
			Result:    result,
		}

		s.logger.Debug("successfully retrieved details", "workflow_id", workflowID)
		if err := s.renderer.RenderWithRequest(w, r, "workflow-details", state); err != nil {
			s.logger.Error("failed to render template", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	})
}

// handleGetProfilePage renders the profile page with status or result
func (s *APIServer) handleGetProfilePage() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username := r.PathValue("username")
		if len(username) > MaxGitHubUsernameLength {
			s.writeBadRequest(w, r, "Invalid username.")
			return
		}

		workflowID := "content-generation-" + username
		s.logger.Debug("getting profile page", "workflow_id", workflowID)

		desc, err := GetWorkflowDescription(s.temporalClient, workflowID)
		if err != nil {
			s.logger.Debug("error getting workflow description", "workflow_id", workflowID, "error", err)
			s.writeNotFound(w, r, "Workflow for this user not found.")
			return
		}

		status := desc.WorkflowExecutionInfo.Status
		s.logger.Debug("workflow status", "workflow_id", workflowID, "status", status)

		switch status {
		case 1: // RUNNING
			data := map[string]interface{}{
				"GitHubUsername": username,
				"WorkflowID":     workflowID,
				"Title":          "Profile for " + username,
			}
			if err := s.renderer.RenderWithRequest(w, r, "workflow-status", data); err != nil {
				s.logger.Error("failed to render template", "error", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
		case 2: // COMPLETED
			result, err := GetWorkflowResult(s.temporalClient, workflowID)
			if err != nil {
				s.logger.Error("error getting workflow result", "workflow_id", workflowID, "error", err)
				s.writeInternalError(w, r, err.Error())
				return
			}
			data := map[string]interface{}{
				"Title":     "Profile for " + username,
				"Completed": true,
				"Status":    "Completed",
				"Result":    result,
			}
			if err := s.renderer.RenderWithRequest(w, r, "workflow-details", data); err != nil {
				s.logger.Error("failed to render template", "error", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
		default: // FAILED, CANCELED, TERMINATED, TIMED_OUT, etc.
			s.writeNotFound(w, r, "Profile generation for this user did not complete successfully.")
		}
	})
}

// Poll handlers

// handleShowPollForm renders the poll creation form.
func (s *APIServer) handleShowPollForm() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := map[string]interface{}{
			"Title": "Create a New Poll",
		}
		if err := s.renderer.RenderWithRequest(w, r, "poll-form", data); err != nil {
			s.logger.Error("failed to render template", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	})
}

// handleCreatePoll handles the creation of a new poll workflow.
func (s *APIServer) handleCreatePoll() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			s.writeBadRequest(w, r, err.Error())
			return
		}

		pollRequest := r.FormValue("poll_request")
		if pollRequest == "" {
			s.writeBadRequest(w, r, "Poll request cannot be empty")
			return
		}
		if len(pollRequest) > MaxPollRequestLength {
			s.writeBadRequest(w, r, fmt.Sprintf("Poll request is too long. Please limit to %d characters.", MaxPollRequestLength))
			return
		}

		// Use the LLM to parse the poll request.
		parsedRequest, err := ParsePollRequestWithLLM(
			r.Context(),
			OpenAIConfig{
				APIKey:  os.Getenv("RESEARCH_ORCHESTRATOR_LLM_API_KEY"),
				Model:   os.Getenv("RESEARCH_ORCHESTRATOR_LLM_MODEL"),
				APIHost: os.Getenv("RESEARCH_ORCHESTRATOR_LLM_BASE_URL"),
			},
			pollRequest,
		)
		if err != nil {
			s.logger.Error("failed to parse poll request", "error", err)
			s.writeInternalError(w, r, "Failed to parse poll request: "+err.Error())
			return
		}

		// All polls run for one week.
		duration := 604800 // 7 * 24 * 60 * 60

		// Parse payment configuration
		paymentWallet := os.Getenv("PAYMENT_WALLET_ADDRESS")
		paymentAmount := 0.01 // default
		if envAmount := os.Getenv("PAYMENT_AMOUNT"); envAmount != "" {
			if parsed, err := strconv.ParseFloat(envAmount, 64); err == nil {
				paymentAmount = parsed
			}
		}

		config := PollConfig{
			Question:        parsedRequest.Question,
			AllowedOptions:  parsedRequest.Usernames,
			DurationSeconds: duration,
			SingleVote:      false,
			StartBlocked:    false,
			// Payment configuration
			PaymentRequired: paymentWallet != "", // Only require payment if wallet is configured
			PaymentWallet:   paymentWallet,
			PaymentAmount:   paymentAmount,
		}

		// Generate a unique ID for the workflow from the poll question.
		workflowID := "g2i-poll-" + sanitizeWorkflowID(parsedRequest.Question)

		_, err = StartPollWorkflow(s.temporalClient, workflowID, config)
		if err != nil {
			// If the workflow already exists, it's not an error.
			var workflowExistsErr *serviceerror.WorkflowExecutionAlreadyStarted
			if errors.As(err, &workflowExistsErr) {
				w.Header().Set("HX-Redirect", "/poll/"+workflowID)
				w.WriteHeader(http.StatusOK)
				return
			}

			s.logger.Error("failed to start poll workflow", "error", err)
			s.writeInternalError(w, r, err.Error())
			return
		}

		s.logger.Info("successfully started poll workflow", "workflow_id", workflowID)

		// Kick off image orchestration in the background.
		// This includes listing existing creators, copying existing images, and starting generation workflows.
		// By doing this asynchronously, the user gets redirected immediately to the payment page.
		go func() {
			// Get a list of all users who already have generated content.
			existingCreators, err := s.storageProvider.ListTopLevelFolders(context.Background(), os.Getenv("STORAGE_BUCKET"))
			if err != nil {
				s.logger.Error("failed to list existing creators", "error", err)
				existingCreators = []string{}
			}

			// Create a set for quick lookups.
			existingCreatorsSet := make(map[string]struct{})
			for _, creator := range existingCreators {
				existingCreatorsSet[creator] = struct{}{}
			}

			// Separate users who need image generation from those who have existing images.
			filteredUsernames := []string{}
			existingUsernames := []string{}
			for _, username := range parsedRequest.Usernames {
				if _, exists := existingCreatorsSet[username]; !exists {
					filteredUsernames = append(filteredUsernames, username)
				} else {
					existingUsernames = append(existingUsernames, username)
				}
			}

			// Log the operation summary
			if len(existingUsernames) > 0 {
				s.logger.Info("copying existing images", "count", len(existingUsernames), "users", existingUsernames)
			}

			// For users who already have images, copy their latest image to the poll's folder in the background.
			for _, username := range existingUsernames {
				go func(user string) {
					bucket := os.Getenv("STORAGE_BUCKET")

					latestKey, err := s.storageProvider.GetLatestObjectKeyForUser(context.Background(), bucket, user)
					if err != nil {
						log.Printf("Failed to find latest image for user %s: %v", user, err)
						return
					}

					parts := strings.Split(latestKey, "/")
					filename := parts[len(parts)-1]
					fileExt := strings.TrimPrefix(path.Ext(filename), ".")
					dstKey := fmt.Sprintf("%s/%s.%s", workflowID, user, fileExt)

					err = s.storageProvider.Copy(context.Background(), bucket, latestKey, bucket, dstKey)
					if err != nil {
						log.Printf("Failed to copy image for user %s to poll folder: %v", user, err)
					} else {
						log.Printf("Successfully copied existing image for user %s to poll folder", user)
					}
				}(username)
			}

			// After the poll is created, kick off the content generation workflows for each new user.
			if len(filteredUsernames) > 0 {
				s.logger.Info("starting image generation", "count", len(filteredUsernames), "users", filteredUsernames)

				width, _ := strconv.Atoi(os.Getenv("IMAGE_WIDTH"))
				height, _ := strconv.Atoi(os.Getenv("IMAGE_HEIGHT"))
				baseInput := AppInput{
					ModelName:                     os.Getenv("GEMINI_MODEL"),
					ResearchAgentSystemPrompt:     getEnvB64("RESEARCH_AGENT_SYSTEM_PROMPT"),
					ContentGenerationSystemPrompt: getEnvB64("CONTENT_GENERATION_SYSTEM_PROMPT"),
					StorageProvider:               os.Getenv("STORAGE_PROVIDER"),
					StorageBucket:                 os.Getenv("STORAGE_BUCKET"),
					ImageFormat:                   os.Getenv("IMAGE_FORMAT"),
					ImageWidth:                    width,
					ImageHeight:                   height,
				}

				workflowInput := PollImageGenerationInput{
					Usernames: filteredUsernames,
					PollID:    workflowID,
					AppInput:  baseInput,
				}

				imageGenWorkflowID := "g2i-poll-image-generation-" + workflowID
				_, err := StartPollImageGenerationWorkflow(s.temporalClient, imageGenWorkflowID, workflowInput)
				if err != nil {
					log.Printf("Failed to start poll image generation workflow %s: %v", imageGenWorkflowID, err)
				} else {
					log.Printf("Successfully started poll image generation workflow %s", imageGenWorkflowID)
				}
			}
		}()

		// Redirect immediately - user doesn't need to wait for image orchestration
		w.Header().Set("HX-Redirect", "/poll/"+workflowID)
		w.WriteHeader(http.StatusOK)
	})
}

// handleGetPollDetails renders the details page for a specific poll.
func (s *APIServer) handleGetPollDetails() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workflowID := r.PathValue("id")
		if len(workflowID) > MaxWorkflowIDLength {
			s.writeBadRequest(w, r, "Invalid poll ID.")
			return
		}

		// Check if workflow exists and is running
		desc, err := GetWorkflowDescription(s.temporalClient, workflowID)
		if err != nil {
			var notFoundErr *serviceerror.NotFound
			if errors.As(err, &notFoundErr) {
				s.writeNotFound(w, r, "Poll not found")
				return
			}
			s.writeInternalError(w, r, err.Error())
			return
		}

		// Only show poll details for running workflows
		if desc.WorkflowExecutionInfo.Status != enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
			s.writeNotFound(w, r, "Poll not found")
			return
		}

		config, err := QueryPollWorkflow[PollConfig](s.temporalClient, workflowID, "get_config")
		if err != nil {
			var notFoundErr *serviceerror.NotFound
			if errors.As(err, &notFoundErr) {
				s.writeNotFound(w, r, "Poll not found")
				return
			}
			s.writeInternalError(w, r, err.Error())
			return
		}

		options, err := QueryPollWorkflow[[]string](s.temporalClient, workflowID, "get_options")
		if err != nil {
			var notFoundErr *serviceerror.NotFound
			if errors.As(err, &notFoundErr) {
				s.writeNotFound(w, r, "Poll not found")
				return
			}
			s.writeInternalError(w, r, err.Error())
			return
		}

		state, err := QueryPollWorkflow[PollState](s.temporalClient, workflowID, "get_state")
		if err != nil {
			var notFoundErr *serviceerror.NotFound
			if errors.As(err, &notFoundErr) {
				s.writeNotFound(w, r, "Poll not found")
				return
			}
			s.writeInternalError(w, r, err.Error())
			return
		}

		// Generate payment QR code if payment is required but not paid
		var paymentQRCode string
		var paymentURL template.URL
		if config.PaymentRequired && !state.PaymentPaid {
			// Format amount with proper precision (avoid scientific notation)
			amountStr := strconv.FormatFloat(config.PaymentAmount, 'f', -1, 64)
			// USDC mint address on Solana mainnet
			usdcMint := "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
			// Build Solana Pay URI for USDC transfer
			paymentURLStr := fmt.Sprintf("solana:%s?amount=%s&spl-token=%s&memo=%s",
				config.PaymentWallet,
				amountStr,
				usdcMint,
				url.QueryEscape(workflowID))
			// Convert to template.URL to mark as safe for template rendering
			paymentURL = template.URL(paymentURLStr)

			qrPNG, err := qrcode.Encode(paymentURLStr, qrcode.Medium, 256)
			if err != nil {
				s.logger.Error("failed to generate QR code", "error", err)
			} else {
				paymentQRCode = base64.StdEncoding.EncodeToString(qrPNG)
			}
		}

		data := map[string]interface{}{
			"Title":         "Poll Details",
			"WorkflowID":    workflowID,
			"Config":        config,
			"Options":       options,
			"PaymentPaid":   state.PaymentPaid,
			"PaymentQRCode": paymentQRCode,
			"PaymentURL":    paymentURL,
			"PaymentTxnID":  state.PaymentTxnID,
		}

		if err := s.renderer.RenderWithRequest(w, r, "poll-details", data); err != nil {
			s.logger.Error("failed to render template", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	})
}

// handleGetPollResults renders the results partial for a specific poll.
func (s *APIServer) handleGetPollResults() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workflowID := r.PathValue("id")
		if len(workflowID) > MaxWorkflowIDLength {
			s.writeBadRequest(w, r, "Invalid poll ID.")
			return
		}

		options, err := QueryPollWorkflow[[]string](s.temporalClient, workflowID, "get_options")
		if err != nil {
			s.writeInternalError(w, r, err.Error())
			return
		}

		data := map[string]interface{}{
			"WorkflowID": workflowID,
			"Options":    options,
		}

		if err := s.renderer.RenderWithRequest(w, r, "poll-results-partial", data); err != nil {
			s.logger.Error("failed to render template", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	})
}

// handleGetPollProfile handles serving the image or spinner for a poll option.
func (s *APIServer) handleGetPollProfile() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workflowID := r.PathValue("id")
		option := r.PathValue("option")

		if len(workflowID) > MaxWorkflowIDLength {
			s.writeBadRequest(w, r, "Invalid poll ID.")
			return
		}
		if len(option) > MaxOptionLength {
			s.writeBadRequest(w, r, "Invalid option.")
			return
		}

		bucket := os.Getenv("STORAGE_BUCKET")
		imageFormat := os.Getenv("IMAGE_FORMAT")

		key := fmt.Sprintf("%s/%s.%s", workflowID, option, imageFormat)

		imageURL, err := s.storageProvider.Stat(r.Context(), bucket, key)
		if err != nil {
			// If the image doesn't exist, return the spinner partial
			data := map[string]interface{}{
				"WorkflowID": workflowID,
				"Option":     option,
			}
			if err := s.renderer.RenderWithRequest(w, r, "spinner-partial", data); err != nil {
				s.logger.Error("failed to render template", "error", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
			return
		}

		// If the image exists, return the image partial
		data := map[string]interface{}{
			"ImageURL":   imageURL,
			"Option":     option,
			"WorkflowID": workflowID,
		}
		if err := s.renderer.RenderWithRequest(w, r, "image-partial", data); err != nil {
			s.logger.Error("failed to render template", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	})
}

// handleGetPollVotes handles serving the vote count for a poll option.
func (s *APIServer) handleGetPollVotes() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workflowID := r.PathValue("id")
		option := r.PathValue("option")

		if len(workflowID) > MaxWorkflowIDLength {
			s.writeBadRequest(w, r, "Invalid poll ID.")
			return
		}
		if len(option) > MaxOptionLength {
			s.writeBadRequest(w, r, "Invalid option.")
			return
		}

		state, err := QueryPollWorkflow[PollState](s.temporalClient, workflowID, "get_state")
		if err != nil {
			// If workflow is still starting up, return temporary state
			data := map[string]interface{}{
				"WorkflowID": workflowID,
				"Option":     option,
				"Votes":      0,
			}
			if err := s.renderer.RenderWithRequest(w, r, "votes-partial", data); err != nil {
				s.logger.Error("failed to render template", "error", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
			return
		}

		data := map[string]interface{}{
			"WorkflowID": workflowID,
			"Option":     option,
			"Votes":      state.Options[option],
		}

		if err := s.renderer.RenderWithRequest(w, r, "votes-partial", data); err != nil {
			s.logger.Error("failed to render template", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	})
}

// handleVoteOnPoll handles a vote submission for a poll.
func (s *APIServer) handleVoteOnPoll() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workflowID := r.PathValue("id")
		if len(workflowID) > MaxWorkflowIDLength {
			s.writeBadRequest(w, r, "Invalid poll ID.")
			return
		}

		if err := r.ParseForm(); err != nil {
			s.writeBadRequest(w, r, err.Error())
			return
		}

		// Get or create a unique voter ID from a cookie
		voterCookie, err := r.Cookie("voter_id")
		var voterID string
		if err != nil || voterCookie.Value == "" {
			voterID = uuid.New().String()
			cookie := &http.Cookie{
				Name:     "voter_id",
				Value:    voterID,
				Expires:  time.Now().Add(365 * 24 * time.Hour),
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			}
			http.SetCookie(w, cookie)
		} else {
			voterID = voterCookie.Value
		}

		update := VoteUpdate{
			UserID: voterID,
			Option: r.FormValue("option"),
			Amount: 1,
		}
		if len(update.Option) > MaxOptionLength {
			s.writeBadRequest(w, r, "Invalid option.")
			return
		}

		result, err := UpdatePollWorkflow[VoteUpdateResult](s.temporalClient, workflowID, "vote", update)
		if err != nil {
			s.writeInternalError(w, r, err.Error())
			return
		}

		data := map[string]interface{}{
			"WorkflowID": workflowID,
			"Option":     update.Option,
			"Votes":      result.TotalVotes,
		}

		if err := s.renderer.RenderWithRequest(w, r, "votes-partial", data); err != nil {
			s.logger.Error("failed to render template", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	})
}

// handleDeletePoll deletes all poll-related objects from storage and terminates associated workflows.
func (s *APIServer) handleDeletePoll() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pollID := r.PathValue("id")
		if len(pollID) > MaxWorkflowIDLength {
			s.writeJSONBadRequest(w, "Invalid poll ID.")
			return
		}

		bucket := os.Getenv("STORAGE_BUCKET")

		// Terminate the poll workflow
		err := TerminateWorkflow(s.temporalClient, pollID, "Poll deleted by user")
		if err != nil {
			s.logger.Warn("failed to terminate poll workflow", "poll_id", pollID, "error", err)
		}

		// Terminate the image generation workflow
		imageGenWorkflowID := "g2i-poll-image-generation-" + pollID
		err = TerminateWorkflow(s.temporalClient, imageGenWorkflowID, "Poll deleted by user")
		if err != nil {
			s.logger.Warn("failed to terminate image generation workflow", "workflow_id", imageGenWorkflowID, "error", err)
		}

		// Delete all objects with the poll ID as the prefix
		err = s.storageProvider.Delete(r.Context(), bucket, pollID+"/")
		if err != nil {
			s.logger.Error("failed to delete poll storage", "poll_id", pollID, "error", err)
			s.writeJSONInternalError(w, "Failed to delete poll: "+err.Error())
			return
		}

		s.logger.Info("successfully deleted poll", "poll_id", pollID)
		s.writeOK(w, map[string]string{"message": "Poll deleted successfully"})
	})
}

// handleListPolls renders the list of all polls.
func (s *APIServer) handleListPolls() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.logger.Debug("listing all polls")

		// Limit to 20 most recent polls to avoid timeout
		polls, err := ListPollWorkflows(s.temporalClient, 20)
		if err != nil {
			s.logger.Error("failed to list polls", "error", err)
			s.writeInternalError(w, r, "Failed to list polls: "+err.Error())
			return
		}

		data := map[string]interface{}{
			"Title": "All Polls",
			"Polls": polls,
		}

		if err := s.renderer.RenderWithRequest(w, r, "poll-list", data); err != nil {
			s.logger.Error("failed to render template", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	})
}
