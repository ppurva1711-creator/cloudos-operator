.PHONY: help build test test-unit test-integration test-e2e test-coverage test-all test-all-with-e2e test-watch lint fmt docker-build docker-push deploy setup setup-kind setup-dev setup-monitoring logs clean

# Configuration
PROJECT_NAME := cloudtask-orchestrator
DOCKER_REGISTRY := localhost
VERSION := v1.0.0
GO_VERSION := 1.22
KUBE_VERSION := v1.28.0
OPERATOR_IMAGE := $(DOCKER_REGISTRY)/cloudtask-operator:$(VERSION)
GATEWAY_IMAGE := $(DOCKER_REGISTRY)/cloudtask-api-gateway:$(VERSION)
COVERAGE_THRESHOLD := 90

# Directories
BIN_DIR := bin
OUTPUT_DIR := build
SCRIPT_DIR := scripts
CONFIG_DIR := config

# Detect OS
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Linux)
    DETECTED_OS := linux
endif
ifeq ($(UNAME_S),Darwin)
    DETECTED_OS := darwin
endif
ifeq ($(OS),Windows_NT)
    DETECTED_OS := windows
endif

help: ## Display this help screen
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

build: ## Build the operator and API gateway binaries
	@echo "Building CloudTask Operator..."
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BIN_DIR)/operator cmd/operator/main.go
	@echo "Building CloudTask API Gateway..."
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BIN_DIR)/api-gateway cmd/api-gateway/main.go
	@echo "Build complete!"

build-local: ## Build binaries for local machine
	@echo "Building for local machine..."
	go build -o $(BIN_DIR)/operator cmd/operator/main.go
	go build -o $(BIN_DIR)/api-gateway cmd/api-gateway/main.go
	@echo "Local build complete!"

test: test-unit ## Run unit tests (default)

test-unit: ## Run all unit tests with race detector and coverage report
	@echo "Running unit tests with race detector..."
	mkdir -p $(OUTPUT_DIR)
	go test ./... -v -race -coverprofile=$(OUTPUT_DIR)/coverage.out -covermode=atomic
	@echo ""
	@echo "Coverage Summary:"
	@go tool cover -func=$(OUTPUT_DIR)/coverage.out | grep total
	@echo ""
	@bash -c "coverage=$$(go tool cover -func=$(OUTPUT_DIR)/coverage.out | grep total | awk '{print $$3}' | sed 's/%//'); \
	echo \"Code Coverage: $$coverage%\"; \
	if [ $$(echo "$$coverage < $(COVERAGE_THRESHOLD)" | bc) -eq 1 ]; then \
		echo \"ERROR: Coverage $$coverage% is below minimum threshold $(COVERAGE_THRESHOLD)%\"; \
		exit 1; \
	else \
		echo \"✓ Coverage meets minimum threshold of $(COVERAGE_THRESHOLD)%\"; \
	fi"

test-integration: ## Run integration tests (no coverage threshold)
	@echo "Running integration tests..."
	go test ./pkg/auth/... ./pkg/pubsub/... ./pkg/storage/... ./pkg/scaling/... ./pkg/api/... \
		-v -race -run "*Integration*" -coverprofile=$(OUTPUT_DIR)/integration-coverage.out -covermode=atomic
	@echo ""
	@echo "Integration Test Coverage:"
	@go tool cover -func=$(OUTPUT_DIR)/integration-coverage.out | grep total || echo "No integration coverage data"

test-e2e: ## Run end-to-end tests against Kind cluster (requires running cluster)
	@echo "Running end-to-end tests against KIND cluster..."
	@echo "Note: Requires a running Kind cluster and configured kubectl"
	@command -v kind >/dev/null 2>&1 || { echo "kind is not installed"; exit 1; }
	@command -v kubectl >/dev/null 2>&1 || { echo "kubectl is not installed"; exit 1; }
	@echo "Cluster status:"
	@kubectl cluster-info || { echo "No Kubernetes cluster available"; exit 1; }
	@go test ./test/e2e/... -v -race -timeout=15m -tags=e2e

test-coverage: ## Generate HTML coverage report and open in browser
	@echo "Generating HTML coverage report..."
	mkdir -p $(OUTPUT_DIR)
	go test ./... -v -race -coverprofile=$(OUTPUT_DIR)/coverage.out -covermode=atomic > /dev/null 2>&1 || true
	@go tool cover -html=$(OUTPUT_DIR)/coverage.out -o $(OUTPUT_DIR)/coverage.html
	@echo "Coverage report: $(OUTPUT_DIR)/coverage.html"
	@echo ""
	@echo "Coverage Summary:"
	@go tool cover -func=$(OUTPUT_DIR)/coverage.out | grep total
	@if command -v xdg-open >/dev/null 2>&1; then \
		echo "Opening in browser..."; \
		xdg-open $(OUTPUT_DIR)/coverage.html; \
	elif command -v open >/dev/null 2>&1; then \
		echo "Opening in browser..."; \
		open $(OUTPUT_DIR)/coverage.html; \
	else \
		echo "Please open $(OUTPUT_DIR)/coverage.html in your browser"; \
	fi

test-all: test-unit test-integration ## Run unit tests + integration tests (no e2e)
	@echo ""
	@echo "==============================="
	@echo "All tests (unit + integration) completed!"
	@echo "==============================="

test-all-with-e2e: test-unit test-integration test-e2e ## Run all tests: unit + integration + e2e
	@echo ""
	@echo "==============================="
	@echo "All tests (unit + integration + e2e) completed!"
	@echo "==============================="

test-watch: ## Run unit tests in watch mode (requires watchexec)
	@command -v watchexec >/dev/null 2>&1 || { echo "watchexec is required: go install github.com/watchexec/watchexec@latest"; exit 1; }
	watchexec -r "make test-unit"

lint: ## Run linter (golangci-lint)
	@echo "Running linter..."
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run ./... --deadline=5m

fmt: ## Format code with gofmt
	@echo "Formatting code..."
	@go fmt ./...
	@goimports -w .

vet: ## Run go vet
	@echo "Running vet..."
	@go vet ./...

docker-build: ## Build Docker images
	@echo "Building Docker images..."
	docker build -t $(OPERATOR_IMAGE) -f Dockerfile.operator .
	docker build -t $(GATEWAY_IMAGE) -f Dockerfile.gateway .
	@echo "Docker build complete!"

docker-push: docker-build ## Build and push Docker images to registry
	@echo "Pushing Docker images to registry..."
	docker push $(OPERATOR_IMAGE)
	docker push $(GATEWAY_IMAGE)
	@echo "Push complete!"

docker-run-local: docker-build ## Run the operator locally in Docker (for testing)
	@echo "Running operator in Docker..."
	docker run -it --rm \
		-e KUBECONFIG=/etc/kubeconfig \
		-v ~/.kube/config:/etc/kubeconfig:ro \
		$(OPERATOR_IMAGE)

setup: setup-kind setup-dev setup-monitoring ## Complete setup (Kind + dev + monitoring)
	@echo "Setup complete!"

setup-kind: ## Create Kind cluster
	@echo "Creating Kind cluster..."
	@which kind > /dev/null || (echo "Installing kind..." && go install sigs.k8s.io/kind@latest)
	@kind create cluster --config=$(CONFIG_DIR)/kind/cluster.yaml --name=cloudtask-cluster 2>/dev/null || echo "Cluster already exists"
	@echo "Waiting for cluster to be ready..."
	@kubectl wait --for=condition=Ready node --all --timeout=300s
	@echo "Kind cluster ready!"

setup-dev: ## Install CRD, RBAC, and sample CloudTask
	@echo "Setting up development environment..."
	@kubectl create namespace orchestrator-system --dry-run=client -o yaml | kubectl apply -f -
	@kubectl apply -f $(CONFIG_DIR)/crd/cloudtask_crd.yaml
	@kubectl apply -f $(CONFIG_DIR)/rbac/rbac.yaml
	@kubectl apply -f $(CONFIG_DIR)/operator/deployment.yaml
	@echo "Waiting for operator to be ready..."
	@kubectl wait --for=condition=Ready pod -l app=cloudtask-operator -n orchestrator-system --timeout=300s 2>/dev/null || true
	@echo "Development environment ready!"

setup-monitoring: ## Install Prometheus and AlertManager
	@echo "Installing monitoring stack..."
	@kubectl create namespace monitoring --dry-run=client -o yaml | kubectl apply -f -
	@kubectl apply -f $(CONFIG_DIR)/monitoring/prometheus.yaml
	@echo "Monitoring stack installed!"

deploy: setup docker-build ## Deploy to Kind cluster
	@echo "Deploying CloudTask Operator..."
	@kind load docker-image $(OPERATOR_IMAGE) --name=cloudtask-cluster 2>/dev/null || true
	@kind load docker-image $(GATEWAY_IMAGE) --name=cloudtask-cluster 2>/dev/null || true
	@kubectl set image deployment/cloudtask-operator cloudtask-operator=$(OPERATOR_IMAGE) -n orchestrator-system 2>/dev/null || true
	@echo "Deployment complete!"

deploy-sample: ## Deploy sample CloudTask
	@echo "Deploying sample CloudTask..."
	@kubectl apply -f $(CONFIG_DIR)/samples/cloudtask_v1_sample.yaml
	@echo "Sample CloudTasks deployed!"

undeploy: ## Undeploy from Kind cluster
	@echo "Undeploying CloudTask Operator..."
	@kubectl delete -f $(CONFIG_DIR)/operator/deployment.yaml --ignore-not-found
	@kubectl delete -f $(CONFIG_DIR)/rbac/rbac.yaml --ignore-not-found
	@kubectl delete -f $(CONFIG_DIR)/crd/cloudtask_crd.yaml --ignore-not-found
	@echo "Undeploy complete!"

logs: ## Show operator logs
	@echo "Fetching operator logs..."
	@kubectl logs -f deployment/cloudtask-operator -n orchestrator-system --all-containers=true --timestamps=true 2>/dev/null || echo "No logs available"

logs-gateway: ## Show API gateway logs
	@echo "Fetching API gateway logs..."
	@kubectl logs -f deployment/cloudtask-api-gateway -n default --all-containers=true --timestamps=true 2>/dev/null || echo "No logs available"

logs-pods: ## Show all CloudTask Pod logs
	@echo "Fetching CloudTask Pod logs..."
	@kubectl logs -f -l cloudtask-name --all-containers=true --timestamps=true 2>/dev/null || echo "No pods found"

status: ## Show cluster and operator status
	@echo "Cluster Status:"
	@kubectl get nodes
	@echo "\nOperator Deployment:"
	@kubectl get deployment -n orchestrator-system cloudtask-operator 2>/dev/null || echo "Operator not deployed"
	@echo "\nCloudTasks:"
	@kubectl get cloudtasks --all-namespaces 2>/dev/null || echo "No CloudTasks found"
	@echo "\nPods:"
	@kubectl get pods -l cloudtask-name 2>/dev/null || echo "No CloudTask Pods found"

get-tasks: ## List all CloudTasks
	@kubectl get cloudtasks -A -o wide

describe-task: ## Describe a specific CloudTask (use TASK=<name>)
	@kubectl describe cloudtask $(TASK) 2>/dev/null || echo "Usage: make describe-task TASK=<cloudtask-name>"

debug-pod: ## Debug a specific Pod (use POD=<name>)
	@kubectl debug pod/$(POD) --image=busybox:1.28 2>/dev/null || kubectl exec -it $(POD) -- sh

port-forward: ## Forward ports for API Gateway (localhost:8000)
	@echo "Forwarding port 8000 to API Gateway..."
	@kubectl port-forward svc/cloudtask-api-gateway 8000:8000 -n default

port-forward-prometheus: ## Forward ports for Prometheus (localhost:9090)
	@echo "Forwarding port 9090 to Prometheus..."
	@kubectl port-forward svc/prometheus 9090:9090 -n monitoring

clean: ## Clean up build artifacts
	@echo "Cleaning up..."
	@rm -rf $(BIN_DIR) $(OUTPUT_DIR)
	@go clean
	@echo "Clean complete!"

clean-cluster: ## Destroy Kind cluster
	@echo "Destroying Kind cluster..."
	@kind delete cluster --name=cloudtask-cluster 2>/dev/null || echo "Cluster not found"
	@echo "Cluster destroyed!"

reset: clean clean-cluster ## Full reset (clean artifacts and destroy cluster)
	@echo "Full reset complete!"

create-tenant: ## Create a new tenant (use TENANT=<tenant-id>)
	@if [ -z "$(TENANT)" ]; then \
		echo "Usage: make create-tenant TENANT=<tenant-id>"; \
		exit 1; \
	fi
	@$(SCRIPT_DIR)/create-tenant.sh $(TENANT)

version: ## Show version information
	@echo "CloudTask Orchestrator $(VERSION)"
	@echo "Go Version: $(GO_VERSION)"
	@echo "Kubernetes Version: $(KUBE_VERSION)"
	@go version
	@kubectl version --client 2>/dev/null || echo "kubectl not installed"

ci: fmt vet lint test build ## Run CI pipeline (format, vet, lint, test, build)
	@echo "CI pipeline complete!"

.DEFAULT_GOAL := help
