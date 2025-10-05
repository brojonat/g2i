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

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/labstack/gommon/log"
	"go.temporal.io/sdk/client"
)

//go:embed all:static
var staticFS embed.FS

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

	r.templates["index"] = template.Must(template.ParseFiles("templates/base.html", "templates/index.html"))
	r.templates["workflow-status"] = template.Must(template.ParseFiles("templates/base.html", "templates/workflow-status.html"))
	r.templates["workflow-details"] = template.Must(template.ParseFiles("templates/base.html", "templates/workflow-details.html"))
	r.templates["error"] = template.Must(template.ParseFiles("templates/base.html", "templates/error.html"))

	return r
}

// GenerateRequest defines the expected input from the client
type GenerateRequest struct {
	GitHubUsername string `form:"github_username"`
	ModelName      string `form:"model_name"`
}

// APIServer for handling HTTP requests
type APIServer struct {
	temporalClient client.Client
}

// NewAPIServer creates a new API server
func NewAPIServer(temporalClient client.Client) *APIServer {
	return &APIServer{
		temporalClient: temporalClient,
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

	// Serve static files
	e.GET("/static/*", echo.WrapHandler(http.FileServer(http.FS(staticFS))))

	// Middleware
	e.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
		Skipper: func(c echo.Context) bool {
			path := c.Request().URL.Path
			// Skip logging for status polling and other noisy endpoints
			if strings.HasPrefix(path, "/workflow/") && strings.HasSuffix(path, "/status") {
				return true
			}
			if path == "/.well-known/appspecific/com.chrome.devtools.json" {
				return true
			}
			return false
		},
	}))
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

	return e
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
