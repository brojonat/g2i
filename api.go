package main

import (
	"context"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/labstack/gommon/log"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"golang.org/x/exp/errors"
)

// requestLogger is a custom middleware that logs all requests.
func requestLogger(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Log the incoming request
		c.Logger().Debugf("→ %s %s", c.Request().Method, c.Request().RequestURI)

		// Call the next handler in the chain
		err := next(c)

		// Get the response status code
		status := c.Response().Status

		// Log the response
		if status >= 500 {
			logMsg := fmt.Sprintf("← %s %s - %d", c.Request().Method, c.Request().RequestURI, status)
			if err != nil {
				logMsg = fmt.Sprintf("%s (error: %v)", logMsg, err)
			}
			c.Logger().Error(logMsg)
		} else if status >= 400 {
			c.Logger().Infof("← %s %s - %d", c.Request().Method, c.Request().RequestURI, status)
		} else {
			c.Logger().Debugf("← %s %s - %d", c.Request().Method, c.Request().RequestURI, status)
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

	isHTMX := c.Request().Header.Get("HX-Request") == "true"
	if isHTMX {
		// For HTMX requests, we determine if we're rendering a partial or a full content swap.
		// Our convention: if a template defines a block with the same name as the template key,
		// it's a partial and we render that block. Otherwise, we render the "content" block.
		block := "content"
		if tmpl.Lookup(name) != nil {
			block = name
		}
		return tmpl.ExecuteTemplate(w, block, data)
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
	r.templates["poll-details"] = template.Must(template.ParseFS(templateFS, "templates/base.html", "templates/poll-details.html", "templates/poll-results-partial.html"))
	r.templates["poll-results-partial"] = template.Must(template.ParseFS(templateFS, "templates/poll-results-partial.html"))
	r.templates["generate-form"] = template.Must(template.ParseFS(templateFS, "templates/base.html", "templates/generate-form.html"))
	r.templates["spinner-partial"] = template.Must(template.ParseFS(templateFS, "templates/spinner-partial.html"))
	r.templates["image-partial"] = template.Must(template.ParseFS(templateFS, "templates/image-partial.html"))
	r.templates["votes-partial"] = template.Must(template.ParseFS(templateFS, "templates/votes-partial.html"))

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
	e.Logger.SetLevel(log.DEBUG)

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
	e.GET("/generate-form", s.GetGenerateForm)
	e.GET("/workflow/:id/status", s.GetWorkflowStatus)
	e.GET("/workflow/:id", s.GetWorkflowDetails)
	e.GET("/profile/:username", s.GetProfilePage)

	// Poll routes
	e.GET("/poll/new", s.ShowPollForm)
	e.POST("/poll", s.CreatePoll)
	e.GET("/poll/:id", s.GetPollDetails)
	e.GET("/poll/:id/results", s.GetPollResults)
	e.POST("/poll/:id/vote", s.VoteOnPoll)
	e.DELETE("/poll/:id", s.DeletePoll)
	e.GET("/poll/:id/profile/:option", s.GetPollProfile)
	e.GET("/poll/:id/votes/:option", s.GetPollVotes)

	// Visualization routes
	e.GET("/visualization-form", s.GetVisualizationForm)

	return e
}

// GetVisualizationForm renders the visualization form partial.
func (s *APIServer) GetVisualizationForm(c echo.Context) error {
	return c.Render(http.StatusOK, "visualization-form", nil)
}

// GetGenerateForm renders the meme generation form.
func (s *APIServer) GetGenerateForm(c echo.Context) error {
	return c.Render(http.StatusOK, "generate-form", nil)
}

// ShowPollForm renders the poll creation form.
func (s *APIServer) ShowPollForm(c echo.Context) error {
	return c.Render(http.StatusOK, "poll-form", echo.Map{
		"Title": "Create a New Poll",
	})
}

// CreatePoll handles the creation of a new poll workflow.
func (s *APIServer) CreatePoll(c echo.Context) error {
	pollRequest := c.FormValue("poll_request")
	if pollRequest == "" {
		return c.Render(http.StatusBadRequest, "error", echo.Map{"error": "Poll request cannot be empty"})
	}

	// Use the LLM to parse the poll request.
	parsedRequest, err := ParsePollRequestWithLLM(
		c.Request().Context(),
		OpenAIConfig{
			APIKey:  os.Getenv("RESEARCH_ORCHESTRATOR_LLM_API_KEY"),
			Model:   os.Getenv("RESEARCH_ORCHESTRATOR_LLM_MODEL"),
			APIHost: os.Getenv("RESEARCH_ORCHESTRATOR_LLM_BASE_URL"),
		},
		pollRequest,
	)
	if err != nil {
		c.Logger().Errorf("Failed to parse poll request: %v", err)
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": "Failed to parse poll request: " + err.Error()})
	}

	// All polls run for one week.
	duration := 604800 // 7 * 24 * 60 * 60

	config := PollConfig{
		Question:        parsedRequest.Question,
		AllowedOptions:  parsedRequest.Usernames,
		DurationSeconds: duration,
		SingleVote:      false,
		StartBlocked:    false,
	}

	// Generate a unique ID for the workflow from the poll question.
	workflowID := "poll-" + sanitizeWorkflowID(parsedRequest.Question)

	_, err = StartPollWorkflow(s.temporalClient, workflowID, config)
	if err != nil {
		// If the workflow already exists, it's not an error.
		// We just redirect to the existing poll.
		var workflowExistsErr *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(err, &workflowExistsErr) {
			c.Response().Header().Set("HX-Redirect", "/poll/"+workflowID)
			return c.NoContent(http.StatusOK)
		}

		// For any other error, return a 500.
		c.Logger().Errorf("Failed to start poll workflow: %v", err)
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
	}

	c.Logger().Infof("Successfully started poll workflow %s", workflowID)
	// Get a list of all users who already have generated content.
	existingCreators, err := s.storageProvider.ListTopLevelFolders(c.Request().Context(), os.Getenv("STORAGE_BUCKET"))
	if err != nil {
		// Log the error but don't block poll creation, as this is a non-critical optimization.
		c.Logger().Errorf("Failed to list existing creators: %v", err)
		existingCreators = []string{} // Proceed with an empty list
	}

	// Create a set for quick lookups.
	existingCreatorsSet := make(map[string]struct{})
	for _, creator := range existingCreators {
		existingCreatorsSet[creator] = struct{}{}
	}

	// Correctly separate users who need image generation from those who have existing images.
	filteredUsernames := []string{} // Users for whom we will generate new images.
	existingUsernames := []string{} // Users whose existing images we will copy.
	for _, username := range parsedRequest.Usernames {
		if _, exists := existingCreatorsSet[username]; !exists {
			filteredUsernames = append(filteredUsernames, username)
		} else {
			existingUsernames = append(existingUsernames, username)
		}
	}

	// Log the operation summary
	if len(existingUsernames) > 0 {
		c.Logger().Infof("Copying existing images for %d users: %v", len(existingUsernames), existingUsernames)
	}

	// For users who already have images, copy their latest image to the poll's folder in the background.
	for _, username := range existingUsernames {
		go func(user string) {
			bucket := os.Getenv("STORAGE_BUCKET")

			// 1. Find the latest existing object for the user.
			// Use context.Background() because the request context will be canceled.
			latestKey, err := s.storageProvider.GetLatestObjectKeyForUser(context.Background(), bucket, user)
			if err != nil {
				log.Printf("Failed to find latest image for user %s: %v", user, err)
				return // Skip copying for this user
			}

			// 2. Construct the new destination key inside the poll's folder.
			parts := strings.Split(latestKey, "/")
			filename := parts[len(parts)-1]
			fileExt := strings.TrimPrefix(path.Ext(filename), ".")
			dstKey := fmt.Sprintf("%s/%s.%s", workflowID, user, fileExt)

			// 3. Perform the copy operation.
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
		c.Logger().Infof("Starting image generation for %d new users: %v", len(filteredUsernames), filteredUsernames)

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

		// We start this workflow and forget about it. It runs in the background.
		// A unique ID prevents multiple image generation workflows for the same poll.
		imageGenWorkflowID := "poll-image-generation-" + workflowID
		go func() {
			_, err := StartPollImageGenerationWorkflow(s.temporalClient, imageGenWorkflowID, workflowInput)
			if err != nil {
				log.Printf("Failed to start poll image generation workflow %s: %v", imageGenWorkflowID, err)
			} else {
				log.Printf("Successfully started poll image generation workflow %s", imageGenWorkflowID)
			}
		}()
	}

	c.Response().Header().Set("HX-Redirect", "/poll/"+workflowID)
	return c.NoContent(http.StatusOK)
}

// GetPollDetails renders the details page for a specific poll.
func (s *APIServer) GetPollDetails(c echo.Context) error {
	workflowID := c.Param("id")

	config, err := QueryPollWorkflow[PollConfig](s.temporalClient, workflowID, "get_config")
	if err != nil {
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
	}

	options, err := QueryPollWorkflow[[]string](s.temporalClient, workflowID, "get_options")
	if err != nil {
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
	}

	// Fetch image URLs for each poll option.
	return c.Render(http.StatusOK, "poll-details", echo.Map{
		"Title":      "Poll Details",
		"WorkflowID": workflowID,
		"Config":     config,
		"Options":    options,
	})
}

// GetPollResults renders the results partial for a specific poll.
func (s *APIServer) GetPollResults(c echo.Context) error {
	workflowID := c.Param("id")

	options, err := QueryPollWorkflow[[]string](s.temporalClient, workflowID, "get_options")
	if err != nil {
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
	}

	// HTMX requests expect a partial, so we render the results template directly.
	return c.Render(http.StatusOK, "poll-results-partial", echo.Map{
		"WorkflowID": workflowID,
		"Options":    options,
	})
}

// GetPollProfile handles serving the image or spinner for a poll option.
func (s *APIServer) GetPollProfile(c echo.Context) error {
	workflowID := c.Param("id")
	option := c.Param("option")
	bucket := os.Getenv("STORAGE_BUCKET")
	imageFormat := os.Getenv("IMAGE_FORMAT")

	// Construct the expected object key for the poll option's image.
	key := fmt.Sprintf("%s/%s.%s", workflowID, option, imageFormat)

	// Check if the image exists in storage.
	imageURL, err := s.storageProvider.Stat(c.Request().Context(), bucket, key)
	if err != nil {
		// If the image doesn't exist, return the spinner partial, which will
		// continue to poll.
		return c.Render(http.StatusOK, "spinner-partial", echo.Map{
			"WorkflowID": workflowID,
			"Option":     option,
		})
	}

	// If the image exists, return the image partial, which does NOT have
	// htmx polling attributes, so polling for this image will stop.
	return c.Render(http.StatusOK, "image-partial", echo.Map{
		"ImageURL":   imageURL,
		"Option":     option,
		"WorkflowID": workflowID,
	})
}

// GetPollVotes handles serving the vote count for a poll option.
func (s *APIServer) GetPollVotes(c echo.Context) error {
	workflowID := c.Param("id")
	option := c.Param("option")

	state, err := QueryPollWorkflow[PollState](s.temporalClient, workflowID, "get_state")
	if err != nil {
		// It's possible the workflow is still starting up, so don't treat this
		// as a hard error. Return a temporary state.
		return c.Render(http.StatusOK, "votes-partial", echo.Map{
			"WorkflowID": workflowID,
			"Option":     option,
			"Votes":      0,
		})
	}

	return c.Render(http.StatusOK, "votes-partial", echo.Map{
		"WorkflowID": workflowID,
		"Option":     option,
		"Votes":      state.Options[option],
	})
}

// VoteOnPoll handles a vote submission for a poll.
func (s *APIServer) VoteOnPoll(c echo.Context) error {
	workflowID := c.Param("id")

	// Get or create a unique voter ID from a cookie.
	voterCookie, err := c.Cookie("voter_id")
	var voterID string
	if err != nil || voterCookie.Value == "" {
		voterID = uuid.New().String()
		cookie := &http.Cookie{
			Name:     "voter_id",
			Value:    voterID,
			Expires:  time.Now().Add(365 * 24 * time.Hour), // Expire in one year
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		}
		c.SetCookie(cookie)
	} else {
		voterID = voterCookie.Value
	}

	update := VoteUpdate{
		UserID: voterID,
		Option: c.FormValue("option"),
		Amount: 1, // Each vote counts as 1
	}

	result, err := UpdatePollWorkflow[VoteUpdateResult](s.temporalClient, workflowID, "vote", update)
	if err != nil {
		return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
	}

	return c.Render(http.StatusOK, "votes-partial", echo.Map{
		"WorkflowID": workflowID,
		"Option":     update.Option,
		"Votes":      result.TotalVotes,
	})
}

// DeletePoll deletes all poll-related objects from storage and terminates associated workflows.
func (s *APIServer) DeletePoll(c echo.Context) error {
	pollID := c.Param("id")
	bucket := os.Getenv("STORAGE_BUCKET")

	// Terminate the poll workflow to allow workflow ID reuse
	err := TerminateWorkflow(s.temporalClient, pollID, "Poll deleted by user")
	if err != nil {
		c.Logger().Warnf("Failed to terminate poll workflow %s: %v", pollID, err)
		// Continue with deletion even if termination fails
	}

	// Terminate the image generation workflow to allow workflow ID reuse
	imageGenWorkflowID := "poll-image-generation-" + pollID
	err = TerminateWorkflow(s.temporalClient, imageGenWorkflowID, "Poll deleted by user")
	if err != nil {
		c.Logger().Warnf("Failed to terminate image generation workflow %s: %v", imageGenWorkflowID, err)
		// Continue with deletion even if termination fails
	}

	// Delete all objects with the poll ID as the prefix
	err = s.storageProvider.Delete(c.Request().Context(), bucket, pollID+"/")
	if err != nil {
		c.Logger().Errorf("Failed to delete poll storage %s: %v", pollID, err)
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "Failed to delete poll: " + err.Error()})
	}

	c.Logger().Infof("Successfully deleted poll %s", pollID)
	return c.JSON(http.StatusOK, echo.Map{"message": "Poll deleted successfully"})
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

func sanitizeWorkflowID(input string) string {
	// Replace non-alphanumeric characters with a hyphen.
	reg := regexp.MustCompile(`[^a-zA-Z0-9-_]+`)
	sanitized := reg.ReplaceAllString(input, "-")

	// Trim leading/trailing hyphens that might have been created.
	sanitized = strings.ToLower(strings.Trim(sanitized, "-"))

	// Enforce a max length for workflow IDs.
	const maxLength = 200
	if len(sanitized) > maxLength {
		sanitized = sanitized[:maxLength]
	}

	return sanitized
}
