#!/bin/bash

# ============================================================================
# switch-to-mock.sh - Switch Module 1 from Real to Mock Implementation
# ============================================================================
#
# This script quickly switches the Module 1 gRPC endpoint between:
# 1. Real Module 1 (external): Uses ExternalName service
# 2. Mock Module 1 (internal): Uses ClusterIP service with mock pods
#
# Usage:
#   ./switch-to-mock.sh                  # Switch to mock
#   ./switch-to-mock.sh real <address>   # Switch to real (requires address)
#   ./switch-to-mock.sh status            # Show current configuration

set -e

NAMESPACE="${NAMESPACE:-orchestrator-system}"
SERVICE_NAME="module1-scheduler"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# ============================================================================
# Functions
# ============================================================================

log_info() {
  echo -e "${BLUE}ℹ${NC} $1"
}

log_success() {
  echo -e "${GREEN}✓${NC} $1"
}

log_error() {
  echo -e "${RED}✗${NC} $1"
}

log_warn() {
  echo -e "${YELLOW}⚠${NC} $1"
}

# Get current service configuration
get_current_config() {
  kubectl get svc "$SERVICE_NAME" -n "$NAMESPACE" -o json 2>/dev/null || echo "{}"
}

# Get current service type
get_service_type() {
  local config=$(get_current_config)
  echo "$config" | jq -r '.spec.type // "Unknown"'
}

# Get external name if applicable
get_external_name() {
  local config=$(get_current_config)
  echo "$config" | jq -r '.spec.externalName // "N/A"'
}

# Display current configuration
show_status() {
  log_info "Current Module 1 Service Configuration:"
  echo ""

  local type=$(get_service_type)
  
  if [[ "$type" == "Unknown" ]]; then
    log_error "Service not found"
    return 1
  fi

  echo "  Service: $SERVICE_NAME"
  echo "  Namespace: $NAMESPACE"
  echo "  Type: $type"

  if [[ "$type" == "ExternalName" ]]; then
    local ext_name=$(get_external_name)
    echo "  External Name: $ext_name"
    echo "  Mode: REAL Module 1 (external)"
  elif [[ "$type" == "ClusterIP" ]]; then
    local selector=$(get_current_config | jq -r '.spec.selector // {}' | jq -r 'to_entries | map("\(.key)=\(.value)") | join(",")')
    echo "  Selector: $selector"
    echo "  Mode: MOCK Module 1 (internal)"
  fi

  echo ""
  echo "  Port: 50051"
  echo "  Protocol: gRPC"
}

# Switch to mock Module 1
switch_to_mock() {
  log_info "Switching to Mock Module 1..."

  # Check if mock-scheduler deployment exists
  if ! kubectl get deployment mock-scheduler -n "$NAMESPACE" &> /dev/null; then
    log_error "Mock scheduler deployment not found in $NAMESPACE"
    echo "  Run: kubectl apply -f config/mock/mock-scheduler.yaml"
    return 1
  fi

  # Create mock service configuration
  local mock_service=$(cat <<'EOF'
apiVersion: v1
kind: Service
metadata:
  name: module1-scheduler
  namespace: __NAMESPACE__
  labels:
    app: module1-scheduler
    mode: mock
spec:
  type: ClusterIP
  selector:
    app: mock-scheduler
    control-plane: mock-scheduler
  ports:
  - name: grpc
    port: 50051
    protocol: TCP
    targetPort: 50051
  sessionAffinity: None
EOF
)

  echo "$mock_service" | sed "s/__NAMESPACE__/$NAMESPACE/g" | kubectl apply -f - > /dev/null 2>&1

  if [[ $? -eq 0 ]]; then
    log_success "Switched to Mock Module 1"
    echo ""
    echo "  Service Type: ClusterIP"
    echo "  Selector: app=mock-scheduler"
    echo ""
    echo "  Verify with:"
    echo "    kubectl get svc $SERVICE_NAME -n $NAMESPACE"
    echo "    kubectl get pods -n $NAMESPACE -l app=mock-scheduler"
    return 0
  else
    log_error "Failed to switch to mock"
    return 1
  fi
}

# Switch to real Module 1
switch_to_real() {
  local address="$1"

  if [[ -z "$address" ]]; then
    log_error "Real Module 1 address required"
    echo ""
    echo "Usage: $0 real <module1-address>"
    echo ""
    echo "Examples:"
    echo "  $0 real module1-scheduler.prod.example.com"
    echo "  $0 real 192.168.1.50"
    echo "  $0 real module1.internal"
    return 1
  fi

  log_info "Switching to Real Module 1: $address..."

  # Create real service configuration
  local real_service=$(cat <<EOF
apiVersion: v1
kind: Service
metadata:
  name: $SERVICE_NAME
  namespace: $NAMESPACE
  labels:
    app: module1-scheduler
    mode: real
spec:
  type: ExternalName
  externalName: $address
  ports:
  - name: grpc
    port: 50051
    protocol: TCP
    targetPort: 50051
  sessionAffinity: None
EOF
)

  echo "$real_service" | kubectl apply -f - > /dev/null 2>&1

  if [[ $? -eq 0 ]]; then
    log_success "Switched to Real Module 1"
    echo ""
    echo "  Service Type: ExternalName"
    echo "  Address: $address"
    echo "  Port: 50051"
    echo ""
    echo "  Verify with:"
    echo "    kubectl get svc $SERVICE_NAME -n $NAMESPACE -o yaml"
    echo "    kubectl run -it --rm debug --image=busybox --restart=Never -- nslookup $SERVICE_NAME.$NAMESPACE"
    return 0
  else
    log_error "Failed to switch to real"
    return 1
  fi
}

# ============================================================================
# Main
# ============================================================================

main() {
  echo ""
  echo -e "${BLUE}╔════════════════════════════════════════════════════════════════════════════╗${NC}"
  echo -e "${BLUE}║            Module 1 Scheduler Service Configuration Tool                   ║${NC}"
  echo -e "${BLUE}╚════════════════════════════════════════════════════════════════════════════╝${NC}"
  echo ""

  # Check kubectl connectivity
  if ! kubectl cluster-info &> /dev/null; then
    log_error "Cannot connect to Kubernetes cluster"
    exit 1
  fi

  # Check namespace exists
  if ! kubectl get namespace "$NAMESPACE" &> /dev/null; then
    log_error "Namespace $NAMESPACE not found"
    exit 1
  fi

  # Parse command
  local command="${1:-status}"

  case "$command" in
    mock)
      switch_to_mock
      ;;
    real)
      switch_to_real "$2"
      ;;
    status)
      show_status
      ;;
    help)
      echo "Usage: $0 [command] [options]"
      echo ""
      echo "Commands:"
      echo "  mock      Switch to Mock Module 1 (internal, ClusterIP)"
      echo "  real      Switch to Real Module 1 (external, ExternalName)"
      echo "  status    Show current Module 1 configuration"
      echo "  help      Show this help message"
      echo ""
      echo "Examples:"
      echo "  $0 mock"
      echo "  $0 real module1.example.com"
      echo "  $0 real 10.0.0.50"
      echo "  $0 status"
      ;;
    *)
      log_error "Unknown command: $command"
      echo ""
      echo "Usage: $0 [mock|real|status|help]"
      exit 1
      ;;
  esac

  exit $?
}

main "$@"
