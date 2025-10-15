# Server Refactoring Plan: Echo → Standard Library

This document outlines the plan to refactor the HTTP server from Echo framework to the Go standard library, following the patterns established in the [forohtoo](https://github.com/brojonat/forohtoo) project.

## Motivation

**Why Refactor:**
- **Remove framework dependency**: Reduce complexity and coupling to Echo's abstractions
- **Explicit dependencies**: Follow go-kit philosophy - all dependencies passed as parameters
- **Standard library patterns**: More maintainable, easier to understand, no magic
- **Better testability**: Handlers are just functions returning `http.Handler`
- **Consistency**: Align with forohtoo library patterns and conventions

**From the forohtoo CLAUDE.md:**
> Avoid Frameworks, Embrace the Standard Library
>
> Frameworks often make the above goals harder by hiding complexity and coupling your code to their abstractions. Instead, write functions that return `http.Handler` and use the standard library router.

## Current Architecture (Echo)

```go
type APIServer struct {
    temporalClient  client.Client
    storageProvider ObjectStorage
}

func (s *APIServer) HomePage(c echo.Context) error {
    return c.Render(http.StatusOK, "index", echo.Map{...})
}

func (s *APIServer) SetupRoutes() *echo.Echo {
    e := echo.New()
    e.GET("/", s.HomePage)
    // ...
    return e
}
```

**Issues:**
- Dependencies hidden in struct methods
- Echo-specific types (`echo.Context`, `echo.Map`)
- Framework magic for routing, middleware, rendering
- Harder to test (need to mock Echo context)

## Target Architecture (Standard Library)

```go
type Server struct {
    addr            string
    temporalClient  client.Client
    storageProvider ObjectStorage
    renderer        *TemplateRenderer
    logger          *slog.Logger
    server          *http.Server
}

func New(addr string, temporal client.Client, storage ObjectStorage, logger *slog.Logger) *Server {
    return &Server{
        addr:            addr,
        temporalClient:  temporal,
        storageProvider: storage,
        logger:          logger,
    }
}

func (s *Server) handleHomePage() http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        data := map[string]interface{}{
            "Title": "Vibe Check",
        }
        s.renderer.Render(w, "index", data)
    })
}

func (s *Server) Start() error {
    mux := http.NewServeMux()

    // Register routes
    mux.Handle("GET /", s.handleHomePage())
    mux.Handle("POST /poll", s.handleCreatePoll())
    mux.Handle("GET /poll/{id}", s.handleGetPoll())
    // ...

    // Wrap with middleware
    handler := corsMiddleware(loggingMiddleware(mux, s.logger))

    s.server = &http.Server{
        Addr:         s.addr,
        Handler:      handler,
        ReadTimeout:  15 * time.Second,
        WriteTimeout: 15 * time.Second,
        IdleTimeout:  60 * time.Second,
    }

    s.logger.Info("starting HTTP server", "addr", s.addr)
    return s.server.ListenAndServe()
}
```

**Benefits:**
- All dependencies explicit in constructor
- Standard library types (`http.ResponseWriter`, `*http.Request`)
- No framework magic - just functions
- Easy to test (just call the handler function)
- Handler has everything it needs in its closure

## Refactoring Steps

### Phase 1: Setup New Architecture

1. **Create new `server` package** (or refactor existing `api.go`)
   ```
   server/
   ├── server.go          # Server struct, Start/Shutdown methods
   ├── handlers.go        # HTTP handlers (poll, workflow, etc.)
   ├── middleware.go      # Middleware functions
   ├── templates.go       # Template renderer
   └── response.go        # JSON/error response helpers
   ```

2. **Implement Server struct**
   ```go
   type Server struct {
       addr            string
       temporalClient  client.Client
       storageProvider ObjectStorage
       renderer        *TemplateRenderer
       logger          *slog.Logger
       server          *http.Server
   }
   ```

3. **Implement constructor with explicit dependencies**
   ```go
   func New(
       addr string,
       temporal client.Client,
       storage ObjectStorage,
       logger *slog.Logger,
   ) *Server
   ```

### Phase 2: Replace Template System

Echo's template renderer uses `html/template` under the hood, so this is straightforward:

**Current (Echo):**
```go
type TemplateRenderer struct {
    templates map[string]*template.Template
}

func (t *TemplateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
    tmpl, ok := t.templates[name]
    if !ok {
        return echo.NewHTTPError(http.StatusInternalServerError, "Template not found: "+name)
    }

    isHTMX := c.Request().Header.Get("HX-Request") == "true"
    if isHTMX {
        block := "content"
        if tmpl.Lookup(name) != nil {
            block = name
        }
        return tmpl.ExecuteTemplate(w, block, data)
    }

    return tmpl.ExecuteTemplate(w, "base.html", data)
}
```

**Target (Standard Library):**
```go
type TemplateRenderer struct {
    templates map[string]*template.Template
    logger    *slog.Logger
}

func NewTemplateRenderer(logger *slog.Logger) (*TemplateRenderer, error) {
    r := &TemplateRenderer{
        templates: make(map[string]*template.Template),
        logger:    logger,
    }

    // Load templates from embedded FS
    r.templates["index"] = template.Must(
        template.ParseFS(templateFS, "templates/base.html", "templates/index.html"),
    )
    // ... load other templates

    return r, nil
}

func (r *TemplateRenderer) Render(w http.ResponseWriter, name string, data interface{}) error {
    tmpl, ok := r.templates[name]
    if !ok {
        return fmt.Errorf("template not found: %s", name)
    }

    // Check for HTMX request
    // Note: we need to check the request header somehow
    // Solution: pass http.Request to Render method

    return tmpl.ExecuteTemplate(w, "base.html", data)
}
```

**Better approach - make renderer aware of HTMX:**
```go
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
```

### Phase 3: Convert Handlers

**Pattern: Functions Returning http.Handler**

Each handler becomes a method on Server that returns `http.Handler`:

**Before (Echo):**
```go
func (s *APIServer) GetPollDetails(c echo.Context) error {
    workflowID := c.Param("id")
    if len(workflowID) > MaxWorkflowIDLength {
        return c.Render(http.StatusBadRequest, "error", echo.Map{"error": "Invalid poll ID."})
    }

    config, err := QueryPollWorkflow[PollConfig](s.temporalClient, workflowID, "get_config")
    if err != nil {
        return c.Render(http.StatusInternalServerError, "error", echo.Map{"error": err.Error()})
    }

    return c.Render(http.StatusOK, "poll-details", echo.Map{
        "Title":      "Poll Details",
        "WorkflowID": workflowID,
        "Config":     config,
    })
}
```

**After (Standard Library):**
```go
func (s *Server) handleGetPollDetails() http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        workflowID := r.PathValue("id")
        if len(workflowID) > MaxWorkflowIDLength {
            s.renderError(w, r, "Invalid poll ID.", http.StatusBadRequest)
            return
        }

        config, err := QueryPollWorkflow[PollConfig](s.temporalClient, workflowID, "get_config")
        if err != nil {
            s.logger.Error("failed to get poll config", "workflow_id", workflowID, "error", err)
            s.renderError(w, r, err.Error(), http.StatusInternalServerError)
            return
        }

        data := map[string]interface{}{
            "Title":      "Poll Details",
            "WorkflowID": workflowID,
            "Config":     config,
        }

        if err := s.renderer.RenderWithRequest(w, r, "poll-details", data); err != nil {
            s.logger.Error("failed to render template", "error", err)
            http.Error(w, "Internal server error", http.StatusInternalServerError)
        }
    })
}

// Helper method
func (s *Server) renderError(w http.ResponseWriter, r *http.Request, message string, statusCode int) {
    data := map[string]interface{}{
        "error": message,
    }
    w.WriteHeader(statusCode)
    if err := s.renderer.RenderWithRequest(w, r, "error", data); err != nil {
        s.logger.Error("failed to render error template", "error", err)
        http.Error(w, message, statusCode)
    }
}
```

**Handlers to Convert:**

Poll handlers:
- `ShowPollForm` → `handleShowPollForm()`
- `CreatePoll` → `handleCreatePoll()`
- `GetPollDetails` → `handleGetPollDetails()`
- `GetPollResults` → `handleGetPollResults()`
- `VoteOnPoll` → `handleVoteOnPoll()`
- `DeletePoll` → `handleDeletePoll()`
- `GetPollProfile` → `handleGetPollProfile()`
- `GetPollVotes` → `handleGetPollVotes()`

Workflow handlers:
- `StartContentGeneration` → `handleStartContentGeneration()`
- `GetWorkflowStatus` → `handleGetWorkflowStatus()`
- `GetWorkflowDetails` → `handleGetWorkflowDetails()`
- `GetProfilePage` → `handleGetProfilePage()`

Misc handlers:
- `HomePage` → `handleHomePage()`
- `Ping` → `handlePing()`
- `GetGenerateForm` → `handleGetGenerateForm()`
- `GetVisualizationForm` → `handleGetVisualizationForm()`

### Phase 4: Implement Middleware

**Logging Middleware:**
```go
func loggingMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()

        // Log request
        logger.Debug("http request",
            "method", r.Method,
            "path", r.URL.Path,
            "remote_addr", r.RemoteAddr,
        )

        // Wrap ResponseWriter to capture status code
        wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

        // Call next handler
        next.ServeHTTP(wrapped, r)

        // Log response
        duration := time.Since(start)
        level := slog.LevelDebug
        if wrapped.statusCode >= 500 {
            level = slog.LevelError
        } else if wrapped.statusCode >= 400 {
            level = slog.LevelInfo
        }

        logger.Log(r.Context(), level, "http response",
            "method", r.Method,
            "path", r.URL.Path,
            "status", wrapped.statusCode,
            "duration_ms", duration.Milliseconds(),
        )
    })
}

type responseWriter struct {
    http.ResponseWriter
    statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
    rw.statusCode = code
    rw.ResponseWriter.WriteHeader(code)
}
```

**CORS Middleware:**
```go
func corsMiddleware(next http.Handler) http.Handler {
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
```

**Recovery Middleware:**
```go
func recoveryMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if err := recover(); err != nil {
                logger.Error("panic recovered",
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
```

### Phase 5: Update Response Helpers

**JSON Response:**
```go
func writeJSON(w http.ResponseWriter, data interface{}, statusCode int) error {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(statusCode)
    return json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, message string, statusCode int) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(statusCode)
    json.NewEncoder(w).Encode(map[string]string{
        "error": message,
    })
}
```

### Phase 6: Update main.go

**Current (Echo):**
```go
apiServer := NewAPIServer(temporalClient, storageProvider)
e := apiServer.SetupRoutes()
e.Logger.Fatal(e.Start(":8080"))
```

**Target (Standard Library):**
```go
logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
    Level: slog.LevelDebug,
}))

server := server.New(":8080", temporalClient, storageProvider, logger)

// Load templates
if err := server.WithTemplates(); err != nil {
    logger.Error("failed to load templates", "error", err)
    os.Exit(1)
}

// Start server in goroutine
go func() {
    if err := server.Start(); err != nil && err != http.ErrServerClosed {
        logger.Error("server error", "error", err)
        os.Exit(1)
    }
}()

// Wait for interrupt signal
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
<-sigCh

// Graceful shutdown
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
if err := server.Shutdown(ctx); err != nil {
    logger.Error("shutdown error", "error", err)
}
```

### Phase 7: Remove Echo Dependency

Update `go.mod`:
```bash
go mod edit -droprequire github.com/labstack/echo/v4
go mod edit -droprequire github.com/labstack/gommon
go mod tidy
```

## Migration Checklist

- [ ] Create new server package structure
- [ ] Implement Server struct with explicit dependencies
- [ ] Implement TemplateRenderer (standard library)
- [ ] Convert all handlers to return http.Handler
- [ ] Implement middleware (logging, CORS, recovery)
- [ ] Create response helper functions
- [ ] Update main.go to use new server
- [ ] Add graceful shutdown
- [ ] Update tests for new handler pattern
- [ ] Remove Echo dependency
- [ ] Update CLAUDE.md with new patterns
- [ ] Test all endpoints manually
- [ ] Run integration tests

## Testing Strategy

**Old (Echo):**
```go
func TestGetPollDetails(t *testing.T) {
    e := echo.New()
    req := httptest.NewRequest(http.MethodGet, "/poll/123", nil)
    rec := httptest.NewRecorder()
    c := e.NewContext(req, rec)
    c.SetParamNames("id")
    c.SetParamValues("123")

    server := &APIServer{...}
    err := server.GetPollDetails(c)
    // assertions...
}
```

**New (Standard Library):**
```go
func TestHandleGetPollDetails(t *testing.T) {
    server := &Server{
        temporalClient:  mockTemporalClient,
        storageProvider: mockStorage,
        renderer:        mockRenderer,
        logger:          slog.Default(),
    }

    handler := server.handleGetPollDetails()

    req := httptest.NewRequest(http.MethodGet, "/poll/123", nil)
    req.SetPathValue("id", "123") // Go 1.22+ path values
    rec := httptest.NewRecorder()

    handler.ServeHTTP(rec, req)

    // assertions on rec.Code, rec.Body, etc.
}
```

## Benefits After Refactoring

1. **No framework dependency** - Just standard library
2. **Explicit dependencies** - All dependencies visible in constructor
3. **Easier testing** - Handlers are pure functions
4. **Better performance** - No framework overhead
5. **More maintainable** - Standard patterns, no magic
6. **Aligned with forohtoo** - Consistent with our payment library
7. **Simpler middleware** - Just functions wrapping handlers
8. **Standard logging** - slog instead of Echo's logger

## References

- [forohtoo CLAUDE.md](https://github.com/brojonat/forohtoo/blob/main/CLAUDE.md)
- [forohtoo server implementation](https://github.com/brojonat/forohtoo/tree/main/service/server)
- [Mat Ryer - How I write HTTP services](https://pace.dev/blog/2018/05/09/how-I-write-http-services-after-eight-years.html)
- [Go 1.22 enhanced routing](https://go.dev/blog/routing-enhancements)

## Timeline

- **Phase 1-2**: 2-4 hours (setup, templates)
- **Phase 3**: 4-6 hours (convert all handlers)
- **Phase 4-5**: 2-3 hours (middleware, helpers)
- **Phase 6-7**: 1-2 hours (main.go, cleanup)
- **Testing**: 2-3 hours

**Total estimated time**: 11-18 hours of focused work

## Notes

- This refactoring is purely structural - no functional changes
- All existing templates, routes, and behavior remain the same
- Can be done incrementally - run both servers side-by-side during migration
- Consider doing this on a `refactor/standard-library` branch
- Merge only after thorough testing and review
