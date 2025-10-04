# Set the shell for make explicitly
SHELL := /bin/bash

define setup_env
	$(eval ENV_FILE := $(1))
	$(eval include $(1))
	$(eval export)
endef

# App and Tmux Configuration
APP_NAME := app
BIN_DIR := bin
BIN_PATH := $(BIN_DIR)/$(APP_NAME)
SRC_FILES := $(shell find . -name '*.go' -type f)
TMUX_SESSION := github-poll-dev

# Default target
all: build ## Build the application binary

# Build the application binary
build: $(BIN_PATH)

$(BIN_PATH): $(SRC_FILES)
	@echo "🔨 Building $(APP_NAME)..."
	@go build -o $(BIN_PATH) .

# Run tests
test: ## Run all tests
	@echo "🧪 Running tests..."
	@go test ./...

# Clean build artifacts
clean: ## Clean build artifacts
	@echo "🧹 Cleaning up..."
	@rm -rf $(BIN_DIR) logs

# Internal target to run the app; waits for Temporal and uses air for hot-reloading.
run-app: build
	@if [ ! -f .env ]; then \
		echo "Error: .env file not found. Please create one from env.example and fill in the required values."; \
		exit 1; \
	fi
	@echo "Updating prompts in .env file..."
	@# Remove old prompt variables to prevent duplication
	@grep -vE '_SYSTEM_PROMPT=' .env > .env.tmp && mv .env.tmp .env
	@make generate-prompts >> .env
	@$(call setup_env, .env)
	@echo "⏳ Waiting for Temporal frontend (port 7233) to be ready...";
	@while ! nc -z 127.0.0.1 7233; do \
	  sleep 0.5; \
	done;
	@echo "✅ Temporal frontend is ready.";
	@echo "🚀 Starting development server with hot reloading...";
	@air

# Tmux Development Session
# ------------------------
.PHONY: dev-session start-dev-session stop-dev-session

PORT_FORWARD_WEB_CMD := "kubectl port-forward service/temporal-web 8081:8080"
PORT_FORWARD_FRONTEND_CMD := "kubectl port-forward service/temporal-frontend 7233:7233"

dev-session: stop-dev-session start-dev-session ## Stop (if running) and start a new tmux dev session

start-dev-session: build ## Start a new tmux development session
	@$(call setup_env, .env)
	@command -v tmux >/dev/null 2>&1 || { echo >&2 "tmux is not installed. Aborting."; exit 1; }
	@command -v kubectl >/dev/null 2>&1 || { echo >&2 "kubectl is not installed. Aborting."; exit 1; }
	@mkdir -p logs
	@echo "Starting tmux development session: $(TMUX_SESSION)"
	@tmux new-session -d -s $(TMUX_SESSION) -n 'App'
	@tmux new-window -d -t $(TMUX_SESSION) -n 'TemporalWeb' "$(PORT_FORWARD_WEB_CMD)"
	@tmux new-window -d -t $(TMUX_SESSION) -n 'TemporalFrontend' "$(PORT_FORWARD_FRONTEND_CMD)"
	@sleep 1
	@tmux send-keys -t $(TMUX_SESSION):App "(make run-app) 2>&1 | tee logs/app.log" C-m
	@tmux split-window -h -t $(TMUX_SESSION):App
	@tmux send-keys -t $(TMUX_SESSION):App 'echo "CLI Pane"' C-m
	@echo "✅ Tmux session '$(TMUX_SESSION)' started."
	@echo "Attach with: tmux attach-session -t $(TMUX_SESSION)"
	@echo "Kill with: make stop-dev-session"
	@tmux attach-session -t $(TMUX_SESSION)

stop-dev-session: ## Stop the tmux development session and kill related processes
	@echo "Stopping background processes..."
	@pkill -f "kubectl port-forward service/temporal-web" || true
	@pkill -f "kubectl port-forward service/temporal-frontend" || true
	@pkill -f "make run-app" || true
	@pkill -f "./bin/app" || true
	@sleep 1
	@echo "Stopping tmux development session: $(TMUX_SESSION)"
	@tmux kill-session -t $(TMUX_SESSION) 2>/dev/null || echo "No tmux session '$(TMUX_SESSION)' to stop."

# Prompt/env generation
# ---------------------
.PHONY: generate-prompts

generate-prompts: ## Generate base64-encoded prompt env vars from prompts.yaml
	@if ! command -v yq &> /dev/null; then \
		echo "yq is not installed. Please install it to continue: pip install yq"; \
		exit 1; \
	fi
	@yq -r 'to_entries | .[] | (.key | ascii_upcase | gsub("-";"_")) + "=" + (.value | @base64)' prompts.yaml

help: ## Show this help message
	@echo "Available targets:"
	@awk -F ':.*?## ' '/^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-30s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort