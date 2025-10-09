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

terminate-workflow: build ## Terminate a workflow by ID (usage: make terminate-workflow ID=<workflow-id> [REASON=<reason>])
	@if [ -z "$(ID)" ]; then \
		echo "Error: workflow ID is required. Usage: make terminate-workflow ID=<workflow-id> [REASON='reason']"; \
		exit 1; \
	fi
	@$(call setup_env, .env.dev)
	@$(BIN_PATH) terminate -id "$(ID)" -reason "$(if $(REASON),$(REASON),Manual termination via Makefile)"

# Internal target to run the app; waits for Temporal and uses air for hot-reloading.
run-app: build
	@if [ ! -f .env.dev ]; then \
		echo "Error: .env.dev file not found. Please create one from env.example and fill in the required values."; \
		exit 1; \
	fi
	@echo "Updating prompts in .env.dev file..."
	@# Remove old prompt variables to prevent duplication
	@grep -vE '_SYSTEM_PROMPT=' .env.dev > .env.tmp && mv .env.tmp .env.dev
	@$(MAKE) --no-print-directory generate-prompts >> .env.dev
	@$(call setup_env, .env.dev)
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
	@$(call setup_env, .env.dev)
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
	@if [ -t 0 ]; then \
		tmux attach-session -t $(TMUX_SESSION); \
	else \
		echo "Note: Not attaching to tmux (no TTY detected). Use 'tmux attach-session -t $(TMUX_SESSION)' manually."; \
	fi

stop-dev-session: ## Stop the tmux development session and kill related processes
	@echo "Stopping tmux development session: $(TMUX_SESSION)"
	@tmux kill-session -t $(TMUX_SESSION) 2>/dev/null || echo "No tmux session '$(TMUX_SESSION)' to stop."
	@echo "Waiting for processes to terminate..."
	@sleep 1
	@echo "Cleaning up any remaining background processes..."
	@killall kubectl 2>/dev/null || true

# Prompt/env generation
# ---------------------
.PHONY: generate-prompts

generate-prompts: ## Generate base64-encoded prompt env vars from prompts.yaml
	@if ! command -v yq &> /dev/null; then \
		echo "yq is not installed. Please install it to continue: pip install yq" >&2; \
		exit 1; \
	fi
	@yq eval 'to_entries | .[] | ((.key | upcase | sub("-"; "_")) + "=" + (.value | @base64))' prompts.yaml

# Deployment
# ---------------------
DOCKER_REPO ?= brojonat/github-to-img

build-push: ## Build and push Docker image with git hash tag
	$(eval GIT_HASH := $(shell git rev-parse --short HEAD))
	$(eval DYNAMIC_TAG := $(DOCKER_REPO):$(GIT_HASH))
	@echo "Building and pushing image: $(DYNAMIC_TAG)"
	docker build -t $(DYNAMIC_TAG) .
	docker push $(DYNAMIC_TAG)
	@echo $(GIT_HASH) > .git_hash

deploy-server: ## Deploy server to Kubernetes (prod)
	@$(MAKE) build-push
	@echo "Updating prompts in .env.prod file..."
	@# Remove old prompt variables to prevent duplication
	@grep -vE '_SYSTEM_PROMPT=' .env.prod > .env.tmp && mv .env.tmp .env.prod
	@$(MAKE) --no-print-directory generate-prompts >> .env.prod
	@GIT_HASH=$$(cat .git_hash); \
	echo "Applying server deployment with image: $(DOCKER_REPO):$$GIT_HASH"; \
	kustomize build --load-restrictor=LoadRestrictionsNone server/k8s/prod | \
	sed -e "s;{{DOCKER_REPO}};$(DOCKER_REPO);g" \
			-e "s;{{GIT_COMMIT_SHA}};$$GIT_HASH;g" | \
			kubectl apply -f -

deploy-worker: ## Deploy worker to Kubernetes (prod)
	@$(MAKE) build-push
	@echo "Updating prompts in .env.prod file..."
	@# Remove old prompt variables to prevent duplication
	@grep -vE '_SYSTEM_PROMPT=' .env.prod > .env.tmp && mv .env.tmp .env.prod
	@$(MAKE) --no-print-directory generate-prompts >> .env.prod
	@GIT_HASH=$$(cat .git_hash); \
	echo "Applying worker deployment with image: $(DOCKER_REPO):$$GIT_HASH"; \
	kustomize build --load-restrictor=LoadRestrictionsNone worker/k8s/prod | \
	sed -e "s;{{DOCKER_REPO}};$(DOCKER_REPO);g" \
			-e "s;{{GIT_COMMIT_SHA}};$$GIT_HASH;g" | \
			kubectl apply -f -

deploy-all: ## Deploy both server and worker to Kubernetes (prod)
	@$(MAKE) deploy-server
	@$(MAKE) deploy-worker

delete-server: ## Delete server from Kubernetes (prod)
	kustomize build --load-restrictor=LoadRestrictionsNone server/k8s/prod | kubectl delete -f -

delete-worker: ## Delete worker from Kubernetes (prod)
	kustomize build --load-restrictor=LoadRestrictionsNone worker/k8s/prod | kubectl delete -f -

delete-all: ## Delete both server and worker from Kubernetes (prod)
	@$(MAKE) delete-server
	@$(MAKE) delete-worker

logs-server: ## Tail logs for the server deployment
	kubectl logs -f deployment/gip-api

logs-worker: ## Tail logs for the worker deployment
	kubectl logs -f deployment/gip-worker

restart-server: ## Restart the server deployment
	kubectl rollout restart deployment gip-api

restart-worker: ## Restart the worker deployment
	kubectl rollout restart deployment gip-worker

update-secrets: ## Update Kubernetes secrets from .env.prod
	kubectl create secret generic gip-api-secrets \
		--from-env-file=.env.prod \
		--dry-run=client -o yaml | kubectl apply -f -

help: ## Show this help message
	@echo "Available targets:"
	@awk -F ':.*?## ' '/^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-30s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort