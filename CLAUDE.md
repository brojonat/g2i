# Claude Code Guide for GitHub-to-Image Project

This guide helps Claude Code (and other AI agents) understand how to develop, debug, and interact with this project.

## Project Overview

This is a Temporal-based service that:
1. Scrapes GitHub profiles using an agentic workflow
2. Generates AI-powered visual representations (memes, images)
3. Creates voting polls for community interaction
4. Provides an HTMX-powered web interface

**Tech Stack:** Go, Temporal, HTMX, S3-compatible storage, Kubernetes (for prod)

## Primary Development Interface: Makefile

**All development tasks should be run via the Makefile.** The Makefile is the primary interface for building, running, and debugging this application.

### Key Makefile Commands

```bash
make help                # Show all available commands with descriptions
make build               # Build the application binary (./bin/app)
make test                # Run all Go tests
make clean               # Clean build artifacts and logs directory

# Development Session (recommended)
make start-dev-session   # Start (or restart) the tmux development session
make stop-dev-session    # Stop the tmux session and cleanup

# Deployment (production)
make deploy-server     # Build, push, and deploy server to Kubernetes
make deploy-worker     # Build, push, and deploy worker to Kubernetes
make deploy-all        # Deploy both server and worker
make logs-server       # Tail server logs in Kubernetes
make logs-worker       # Tail worker logs in Kubernetes
```

## Development Workflow with Tmux

The primary way to run and debug this application is through the `make start-dev-session` command, which sets up a complete tmux session.

### Tmux Session Structure

When you run `make start-dev-session`, it creates a tmux session named `github-poll-dev` with the following layout:

1. **Window 1: "App"** (split into 2 panes)
   - **Left pane**: Runs the application with hot-reloading via `air`
     - Output is logged to `logs/app.log`
     - Uses `.env.dev` for configuration
     - Waits for Temporal to be ready before starting
   - **Right pane**: CLI pane for interactive commands and debugging

2. **Window 2: "TemporalWeb"**
   - Port-forwards Temporal web UI (port 8081)
   - Allows access to Temporal dashboard at http://localhost:8081

3. **Window 3: "TemporalFrontend"**
   - Port-forwards Temporal frontend (port 7233)
   - Required for the app to communicate with Temporal

### Working with the Tmux Session

```bash
# Attach to the running session
tmux attach-session -t github-poll-dev

# Navigate between windows
Ctrl+b 1    # Switch to App window
Ctrl+b 2    # Switch to TemporalWeb window
Ctrl+b 3    # Switch to TemporalFrontend window

# Navigate between panes in a window
Ctrl+b o    # Switch to next pane
Ctrl+b q    # Show pane numbers

# Detach from session (keeps it running)
Ctrl+b d

# List all tmux sessions
tmux ls
```

### Checking Application State via Tmux

To understand the current state of the application:

1. **Attach to the tmux session:**
   ```bash
   tmux attach-session -t github-poll-dev
   ```

2. **Check the left pane in the App window** to see live application output

3. **Use the right pane in the App window** for running commands like:
   ```bash
   # Check if the app is listening
   curl http://localhost:8080

   # Check Temporal connection
   kubectl get pods  # If using k8s-based Temporal
   ```

## Logging System

All processes log their stdout and stderr to the `logs/` directory for easier debugging and reference.

### Log Files

- **`logs/app.log`**: Main application output
  - Contains all stdout/stderr from the app process
  - Includes hot-reload events from `air`
  - Shows Temporal workflow execution logs
  - **This is the primary log file to check when debugging**

### Viewing Logs

```bash
# Tail the main app log
tail -f logs/app.log

# View logs with syntax highlighting (if you have bat installed)
bat logs/app.log --style=plain --paging=never -f

# Search logs for errors
grep -i error logs/app.log

# Search logs for a specific workflow ID
grep "workflow-id-123" logs/app.log
```

### Log Structure

The application logs include:
- HTTP request/response logs (from the HTMX web server)
- Temporal workflow execution logs
- Activity execution logs
- GitHub scraping progress
- AI content generation status
- Error traces and stack dumps

## Debugging Workflows

When debugging workflows or investigating issues:

1. **Check the application log first:**
   ```bash
   tail -f logs/app.log
   ```

2. **Access the Temporal Web UI:**
   - Navigate to http://localhost:8081 (when tmux session is running)
   - View workflow history, events, and stack traces

3. **Inspect the CLI pane in tmux:**
   - Use the right pane in the App window to run ad-hoc commands
   - Test API endpoints with `curl`
   - Check database or storage state

4. **Restart the application:**
   ```bash
   # From outside tmux
   make start-dev-session

   # From inside tmux (in the right pane)
   # Kill the left pane process with Ctrl+C (in the left pane)
   # Then re-run: make run-app
   ```

## Environment Configuration

The project uses environment files for configuration:

- **`.env.dev`**: Development environment (used by `make start-dev-session`)
- **`.env.prod`**: Production environment (used for Kubernetes deployments)
- **`env.example`**: Template for environment variables

### Important Environment Variables

```bash
# Temporal connection
TEMPORAL_HOST=localhost:7233

# OpenAI for content generation
OPENAI_API_KEY=sk-...

# S3-compatible storage (Minio for local dev)
S3_ENDPOINT=localhost:9000
S3_REGION=us-east-1
S3_ACCESS_KEY=minioadmin
S3_SECRET_KEY=minioadmin
S3_USE_SSL=false
STORAGE_BUCKET=github-images

# Web server
PORT=8080
```

### Prompt Configuration

The application uses prompts defined in `prompts.yaml`. These are automatically base64-encoded and injected into the environment files during build/deploy:

```bash
# Regenerate prompt environment variables
make generate-prompts
```

This is handled automatically by:
- `make run-app` (for development)
- `make deploy-server` and `make deploy-worker` (for production)

## Project Structure

```
.
├── main.go              # Entry point (server/worker modes)
├── api.go               # HTTP handlers (HTMX web interface)
├── workflows.go         # Main Temporal workflows
├── agent_workflow.go    # Agentic GitHub scraping workflow
├── poll_workflow.go     # Poll management workflow
├── activities.go        # Temporal activities (GitHub scraping, AI generation, storage)
├── types.go             # Data structures and types
├── storage.go           # Storage abstraction (S3, GCS, etc.)
├── client.go            # Temporal client utilities
├── llm.go               # LLM client interface
├── prompts.yaml         # AI prompts for scraping and generation
├── templates/           # HTML templates for HTMX interface
├── static/              # CSS and static assets
├── server/k8s/          # Kubernetes manifests for server deployment
├── worker/k8s/          # Kubernetes manifests for worker deployment
└── logs/                # Application logs (gitignored)
```

## Common Tasks

### Starting Development

```bash
# One command to set everything up
make start-dev-session
```

This will:
1. Build the binary
2. Create logs directory
3. Start tmux session with all required services
4. Automatically attach you to the session

### Making Code Changes

The application uses `air` for hot-reloading. When you edit `.go` files, the app automatically rebuilds and restarts. Watch the logs in the tmux App window to see the reload happen.

### Testing Changes

```bash
# Run tests
make test

# Test the API
curl http://localhost:8080

# Trigger a workflow via web interface
open http://localhost:8080  # or visit in browser
```

### Stopping Development

```bash
# Stop everything cleanly (or just use make start-dev-session to restart)
make stop-dev-session
```

## Deployment to Production

The application deploys to Kubernetes. The Makefile handles building, pushing Docker images, and applying Kubernetes manifests.

```bash
# Deploy server component
make deploy-server

# Deploy worker component
make deploy-worker

# Deploy both
make deploy-all

# View logs
make logs-server
make logs-worker

# Restart deployments
make restart-server
make restart-worker
```

## Version Control with Git and GitHub CLI

### Using Git

This project uses Git for version control. Always check the status of your working directory before committing.

```bash
# Check current status
git status

# View unstaged changes
git diff

# View staged changes
git diff --cached

# View commit history
git log --oneline -10
```

### ⚠️ CRITICAL: Never Commit .env Files

**NEVER commit any `.env` files to the repository.** These files contain secrets like API keys, database credentials, and other sensitive information.

**Files to NEVER commit:**
- `.env`
- `.env.dev`
- `.env.prod`
- `.env.local`
- Any file matching `.env.*`

These are already in `.gitignore`, but always double-check before committing:

```bash
# ALWAYS check what you're about to commit
git status

# If you see any .env files, DO NOT commit them
# If you accidentally staged a .env file, unstage it:
git reset HEAD .env.dev

# Verify the .gitignore contains .env files
cat .gitignore | grep env
```

**If you accidentally commit a .env file:**
1. **DO NOT push to remote**
2. Reset the commit: `git reset --soft HEAD~1`
3. Unstage the .env file: `git reset HEAD .env*`
4. Verify with: `git status`

### Standard Git Workflow

**Making changes and committing:**

```bash
# 1. Check what's changed
git status
git diff

# 2. Verify no .env files are being committed
git status | grep -i env

# 3. Stage your changes
git add path/to/file.go
# Or stage everything (use carefully)
git add .

# 4. Review what you're about to commit
git diff --cached

# 5. Commit with a descriptive message
git commit -m "Add feature X to improve Y"

# 6. Push to remote
git push origin main
```

**Working with branches:**

```bash
# Create a new branch
git checkout -b feature/new-feature

# List branches
git branch -a

# Switch branches
git checkout main

# Make changes and commit on the branch
git add .
git commit -m "Implement new feature"

# Push branch to remote
git push origin feature/new-feature

# Merge branch into main (from main branch)
git checkout main
git merge feature/new-feature

# Delete branch after merging
git branch -d feature/new-feature
```

### Using GitHub CLI (gh)

The `gh` CLI tool provides a better interface for GitHub operations.

```bash
# Check authentication
gh auth status

# View repository info
gh repo view

# Create a pull request
gh pr create --title "Add new feature" --body "Description of changes"

# Or use interactive mode
gh pr create

# List pull requests
gh pr list

# View a specific PR
gh pr view 123

# Checkout a PR locally for review
gh pr checkout 123

# Merge a pull request
gh pr merge 123

# View PR status
gh pr status
```

**Creating a PR workflow:**

```bash
# 1. Create and switch to feature branch
git checkout -b feature/my-feature

# 2. Make changes and commit
git add .
git commit -m "Implement my feature"

# 3. Push to remote
git push origin feature/my-feature

# 4. Create PR with gh CLI
gh pr create --title "Add my feature" --body "## Summary
- Implemented X
- Fixed Y
- Updated Z

## Testing
Tested with \`make start-dev-session\` and verified in \`logs/app.log\`"
```

### Git Best Practices

1. **Always check status before committing**: `git status`
2. **Never commit .env files**: Check twice before pushing
3. **Write descriptive commit messages**: Explain what and why
4. **Pull before pushing**: `git pull origin main` to avoid conflicts
5. **Review staged changes**: `git diff --cached` before committing
6. **Keep commits focused**: One logical change per commit
7. **Test before committing**: Run `make test` and check `logs/app.log`

## Tips for Claude Code

1. **Always use the Makefile**: Don't run raw `go run` or `go build` commands. Use `make` commands.

2. **Check tmux first**: Before starting new processes, check if a tmux session is already running:
   ```bash
   tmux ls
   ```

3. **Read the logs**: When investigating issues, always check `logs/app.log` first. Most debugging information is there.

4. **Use the CLI pane**: The right pane in the App window is your interactive debugging environment. Use it for:
   - Running curl commands
   - Checking process status
   - Testing database queries
   - Inspecting files

5. **Temporal Web UI**: The Temporal dashboard (http://localhost:8081) is invaluable for understanding workflow state, debugging failures, and viewing event history.

6. **Hot-reload is your friend**: Don't restart the whole tmux session for code changes. The `air` hot-reloader will pick up changes automatically.

7. **Environment matters**: Development uses `.env.dev`, production uses `.env.prod`. Make sure you're looking at the right one.

## Troubleshooting

### "Connection refused" errors

Check that the Temporal port-forwards are running:
```bash
tmux attach-session -t github-poll-dev
# Navigate to TemporalFrontend window and check for errors
```

### Application won't start

1. Check `logs/app.log` for errors
2. Verify `.env.dev` exists and has required variables
3. Ensure Temporal is accessible on port 7233

### Hot-reload not working

1. Check that `air` is running in the left pane
2. Verify `.air.toml` configuration
3. Restart the dev session: `make start-dev-session`

### Logs are missing

The `logs/` directory is created by `make start-dev-session`. If it doesn't exist:
```bash
mkdir -p logs
```

## Additional Resources

- **README.md**: High-level project overview and architecture
- **poll_implementation_notes.md**: Implementation details for the polling system
- **Temporal Documentation**: https://docs.temporal.io/
- **HTMX Documentation**: https://htmx.org/docs/
