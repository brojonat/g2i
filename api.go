package main

import (
	"embed"
	"encoding/base64"
	"html/template"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/labstack/gommon/log"
	"go.temporal.io/sdk/client"
)

// requestLogger is a custom middleware that logs only failed requests (4xx and 5xx).
func requestLogger(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Call the next handler in the chain
		err := next(c)

		// Get the response status code
		status := c.Response().Status

		// Only log if the status code indicates an error
		if status >= 400 {
			c.Logger().Errorf("Request failed: status=%d, method=%s, uri=%s",
				status, c.Request().Method, c.Request().RequestURI)
		}

		return err
	}
}

//go:embed all:static
var staticFS embed.FS

//go:embed all:templates
var templateFS embed.FS

// TemplateRenderer is a custom html/template renderer for Echo framework
type TemplateRenderer struct {
	templates map[string]*template.Template
}

// Render renders a template document
func (t *TemplateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	tmpl, ok := t.templates[name]
	if !ok {
		return echo.NewHTTPError(http.StatusInternalServerError, "Template not found: "+name)
	}

	// For HTMX requests, render only the content block.
	// Otherwise, render the full page with the base layout.
	isHTMX := c.Request().Header.Get("HX-Request") == "true"
	if isHTMX {
		return tmpl.ExecuteTemplate(w, "content", data)
	}

	return tmpl.ExecuteTemplate(w, "base.html", data)
}

// getEnvB64 reads a base64-encoded environment variable and returns the decoded string.
func getEnvB64(key string) string {
	val := os.Getenv(key)
	if val == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(val)
	if err != nil {
		// Consider logging this error
		return ""
	}
	return string(decoded)
}

// createMyRender creates the template renderer
func createMyRender() *TemplateRenderer {
	r := &TemplateRenderer{
		templates: make(map[string]*template.Template),
	}

	r.templates["index"] = template.Must(template.ParseFS(templateFS, "templates/base.html", "templates/index.html"))
	r.templates["workflow-status"] = template.Must(template.ParseFS(templateFS, "templates/base.html", "templates/workflow-status.html"))
	r.templates["workflow-details"] = template.Must(template.ParseFS(templateFS, "templates/base.html", "templates/workflow-details.html"))
	r.templates["error"] = template.Must(template.ParseFS(templateFS, "templates/base.html", "templates/error.html"))
	r.templates["poll-form"] = template.Must(template.ParseFS(templateFS, "templates/base.html", "templates/poll-form.html"))
	r.templates["poll-details"] = template.Must(template.ParseFS(templateFS, "templates/base.html", "templates/poll-details.html", "templates/poll-results.html"))
	r.templates["poll-results"] = template.Must(template.ParseFS(templateFS, "templates/poll-results.html"))
	r.templates["poll-creator-search-results"] = template.Must(template.ParseFS(templateFS, "templates/poll-creator-search-results.html"))
	r.templates["visualization-form"] = template.Must(template.ParseFS(templateFS, "templates/base.html", "templates/visualization-form.html"))

	return r
}

// GenerateRequest defines the expected input from the client
type GenerateRequest struct {
	GitHubUsername string `form:"github_username"`
	ModelName      string `form:"model_name"`
}

// APIServer for handling HTTP requests
type APIServer struct {
	temporalClient  client.Client
	storageProvider ObjectStorage
}

// NewAPIServer creates a new API server
func NewAPIServer(temporalClient client.Client, storageProvider ObjectStorage) *APIServer {
	return &APIServer{
		temporalClient:  temporalClient,
		storageProvider: storageProvider,
	}
}

// StartContentGeneration handles POST /generate
func (s *APIServer) StartContentGeneration(c echo.Context) error {
	var req GenerateRequest
	if err := c.Bind(&req); err != nil {
		return c.Render(http.StatusBadRequest, "error", echo.Map{"error": err.Error()})
	}

	// Map the request to the AppInput for the workflow
	width, err := strconv.Atoi(os.Getenv("IMAGE_WIDTH"))
	if err != nil {
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
	}
	height, err := strconv.Atoi(os.Getenv("IMAGE_HEIGHT"))
	if err != nil {
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
	}
	input := AppInput{
		GitHubUsername:                req.GitHubUsername,
		ModelName:                     req.ModelName,
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
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
	}

	c.Response().Header().Set("HX-Redirect", "/profile/"+req.GitHubUsername)
	return c.NoContent(http.StatusOK)
}

// GetWorkflowStatus handles GET /workflow/:id/status
func (s *APIServer) GetWorkflowStatus(c echo.Context) error {
	workflowID := c.Param("id")
	c.Logger().Debugf("Checking status for workflow ID: %s", workflowID)

	state, err := QueryWorkflowState(s.temporalClient, workflowID)
	if err != nil {
		c.Logger().Errorf("Error getting workflow result for ID %s: %v", workflowID, err)
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
	}
	c.Logger().Debugf("Successfully retrieved status for workflow ID %s", workflowID)

	if state.Completed {
		c.Response().Header().Set("HX-Retarget", "#workflow-status")
	}

	return c.Render(http.StatusOK, "workflow-details", state)
}

// SetupRoutes sets up the API routes
func (s *APIServer) SetupRoutes() *echo.Echo {
	e := echo.New()
	e.Logger.SetLevel(log.INFO)

	e.HTTPErrorHandler = customHTTPErrorHandler

	// Serve static files
	e.GET("/static/*", echo.WrapHandler(http.FileServer(http.FS(staticFS))))

	// Middleware
	e.Use(requestLogger)
	e.Use(middleware.Recover())

	// Setup template engine
	e.Renderer = createMyRender()

	// Home page
	e.GET("/", s.HomePage)
	e.GET("/ping", s.Ping)

	// Workflow routes
	e.POST("/generate", s.StartContentGeneration)
	e.GET("/workflow/:id/status", s.GetWorkflowStatus)
	e.GET("/workflow/:id", s.GetWorkflowDetails)
	e.GET("/profile/:username", s.GetProfilePage)

	// Poll routes
	e.GET("/poll/new", s.ShowPollForm)
	e.POST("/poll", s.CreatePoll)
	e.POST("/poll/search-creators", s.SearchMemeCreators)
	e.GET("/poll/:id", s.GetPollDetails)
	e.GET("/poll/:id/results", s.GetPollResults)
	e.POST("/poll/:id/vote", s.VoteOnPoll)

	// Visualization routes
	e.GET("/visualization-form", s.GetVisualizationForm)

	return e
}

// GetVisualizationForm renders the visualization form partial.
func (s *APIServer) GetVisualizationForm(c echo.Context) error {
	return c.Render(http.StatusOK, "visualization-form", nil)
}

// ShowPollForm renders the poll creation form.
func (s *APIServer) ShowPollForm(c echo.Context) error {
	return c.Render(http.StatusOK, "poll-form", echo.Map{
		"Title": "Create a New Poll",
	})
}

// SearchMemeCreators handles the active search for meme creators.
func (s *APIServer) SearchMemeCreators(c echo.Context) error {
	searchTerm := c.FormValue("search")
	bucket := os.Getenv("STORAGE_BUCKET")
	if bucket == "" {
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": "STORAGE_BUCKET environment variable not set"})
	}

	allCreators, err := s.storageProvider.ListTopLevelFolders(c.Request().Context(), bucket)
	if err != nil {
		// In an HTMX context, it might be better to return a partial with an error message.
		return c.String(http.StatusInternalServerError, "Error fetching creators: "+err.Error())
	}

	var filteredCreators []string
	if searchTerm == "" {
		filteredCreators = allCreators
	} else {
		for _, creator := range allCreators {
			if strings.Contains(strings.ToLower(creator), strings.ToLower(searchTerm)) {
				filteredCreators = append(filteredCreators, creator)
			}
		}
	}

	return c.Render(http.StatusOK, "poll-creator-search-results", echo.Map{
		"Creators": filteredCreators,
	})
}

// CreatePoll handles the creation of a new poll workflow.
func (s *APIServer) CreatePoll(c echo.Context) error {
	duration, err := strconv.Atoi(c.FormValue("duration_seconds"))
	if err != nil {
		// Default to 1 hour if not specified or invalid
		duration = 3600
	}

	// For a multi-select form, we need to get all values.
	allowedOptions := c.Request().Form["allowed_options"]

	config := PollConfig{
		Question:        c.FormValue("question"),
		AllowedOptions:  allowedOptions,
		DurationSeconds: duration,
		SingleVote:      c.FormValue("single_vote") == "true",
		StartBlocked:    c.FormValue("start_blocked") == "true",
	}

	// Generate a unique ID for the workflow
	workflowID := "poll-" + uuid.New().String()

	_, err = StartPollWorkflow(s.temporalClient, workflowID, config)
	if err != nil {
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
	}

	c.Response().Header().Set("HX-Redirect", "/poll/"+workflowID)
	return c.NoContent(http.StatusOK)
}

// GetPollDetails renders the details page for a specific poll.
func (s *APIServer) GetPollDetails(c echo.Context) error {
	workflowID := c.Param("id")

	// Query the workflow to get its state
	// Note: We need to implement QueryPollWorkflow in client.go
	state, err := QueryPollWorkflow[PollState](s.temporalClient, workflowID, "get_state")
	if err != nil {
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
	}

	config, err := QueryPollWorkflow[PollConfig](s.temporalClient, workflowID, "get_config")
	if err != nil {
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
	}

	options, err := QueryPollWorkflow[[]string](s.temporalClient, workflowID, "get_options")
	if err != nil {
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
	}

	return c.Render(http.StatusOK, "poll-details", echo.Map{
		"Title":      "Poll Details",
		"WorkflowID": workflowID,
		"State":      state,
		"Config":     config,
		"Options":    options,
	})
}

// GetPollResults renders the results partial for a specific poll.
func (s *APIServer) GetPollResults(c echo.Context) error {
	workflowID := c.Param("id")

	state, err := QueryPollWorkflow[PollState](s.temporalClient, workflowID, "get_state")
	if err != nil {
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
	}

	config, err := QueryPollWorkflow[PollConfig](s.temporalClient, workflowID, "get_config")
	if err != nil {
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
	}

	// HTMX requests expect a partial, so we render the results template directly.
	return c.Render(http.StatusOK, "poll-results", echo.Map{
		"WorkflowID": workflowID,
		"State":      state,
		"Config":     config,
	})
}

// VoteOnPoll handles a vote submission for a poll.
func (s *APIServer) VoteOnPoll(c echo.Context) error {
	workflowID := c.Param("id")

	signal := VoteSignal{
		UserID: c.FormValue("user_id"),
		Option: c.FormValue("option"),
		Amount: 1, // Each vote counts as 1
	}

	// Note: We need to implement SignalPollWorkflow in client.go
	err := SignalPollWorkflow(s.temporalClient, workflowID, "vote", signal)
	if err != nil {
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
	}

	// After sending the signal, we return the updated results partial.
	return s.GetPollResults(c)
}

// HomePage renders the home page
func (s *APIServer) HomePage(c echo.Context) error {
	return c.Render(http.StatusOK, "index", echo.Map{
		"Title": "Vibe Check",
	})
}

// Ping is a simple health check endpoint
func (s *APIServer) Ping(c echo.Context) error {
	return c.String(http.StatusOK, "pong")
}

// GetWorkflowDetails renders the workflow details page
func (s *APIServer) GetWorkflowDetails(c echo.Context) error {
	workflowID := c.Param("id")
	c.Logger().Debugf("Getting details for workflow ID: %s", workflowID)

	// For the details page, we wait for the final result
	result, err := GetWorkflowResult(s.temporalClient, workflowID)
	if err != nil {
		c.Logger().Errorf("Error getting workflow result for ID %s: %v", workflowID, err)
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
	}

	state := WorkflowState{
		Status:    "Completed",
		Completed: true,
		Result:    result,
	}

	c.Logger().Debugf("Successfully retrieved details for workflow ID %s", workflowID)
	return c.Render(http.StatusOK, "workflow-details", state)
}

// GetProfilePage renders the profile page with status or result
func (s *APIServer) GetProfilePage(c echo.Context) error {
	username := c.Param("username")
	workflowID := "content-generation-" + username
	c.Logger().Debugf("Getting profile page for workflow ID: %s", workflowID)

	desc, err := GetWorkflowDescription(s.temporalClient, workflowID)
	if err != nil {
		c.Logger().Debugf("Error getting workflow description for ID %s: %v", workflowID, err)
		return c.Render(http.StatusNotFound, "error", echo.Map{
			"error":           "Workflow for this user not found.",
			"RedirectURL":     "/",
			"RedirectTimeout": 5,
		})
	}

	status := desc.WorkflowExecutionInfo.Status
	c.Logger().Debugf("Workflow %s status: %s", workflowID, status)

	switch status {
	case 1: // RUNNING
		return c.Render(http.StatusOK, "workflow-status", echo.Map{
			"GitHubUsername": username,
			"WorkflowID":     workflowID,
			"Title":          "Profile for " + username,
		})
	case 2: // COMPLETED
		result, err := GetWorkflowResult(s.temporalClient, workflowID)
		if err != nil {
			c.Logger().Errorf("Error getting workflow result for ID %s: %v", workflowID, err)
			return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
		}
		return c.Render(http.StatusOK, "workflow-details", echo.Map{
			"Title":     "Profile for " + username,
			"Completed": true,
			"Status":    "Completed",
			"Result":    result,
		})
	default: // FAILED, CANCELED, TERMINATED, TIMED_OUT, etc.
		return c.Render(http.StatusNotFound, "error", echo.Map{
			"error":           "Profile generation for this user did not complete successfully.",
			"RedirectURL":     "/",
			"RedirectTimeout": 5,
		})
	}
}

// customHTTPErrorHandler handles all HTTP errors for the application.
// It provides a custom 404 page with a redirect.
func customHTTPErrorHandler(err error, c echo.Context) {
	code := http.StatusInternalServerError
	if he, ok := err.(*echo.HTTPError); ok {
		code = he.Code
	}

	// For 404 Not Found errors, render a custom page that redirects to home.
	if code == http.StatusNotFound {
		c.Logger().Warnf("Handling 404 for %s", c.Request().URL.Path)
		if err := c.Render(http.StatusNotFound, "error", echo.Map{
			"error":           "Page not found. You will be redirected to the homepage.",
			"RedirectURL":     "/",
			"RedirectTimeout": 5,
		}); err != nil {
			c.Logger().Error(err)
		}
		return
	}

	// For all other errors, use the default Echo error handler.
	c.Echo().DefaultHTTPErrorHandler(err, c)
}
