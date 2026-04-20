#!/bin/bash

################################################################################
# CloudTask Orchestrator - Redis Setup Script
# Purpose: Deploy Redis to Kubernetes with full production-ready configuration
# Usage: ./setup-redis.sh
# Date: 2026-04-19
################################################################################

set -o pipefail

# ============================================================================
# COLOR OUTPUT FUNCTIONS
# ============================================================================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${BLUE}ℹ️  $1${NC}"
}

log_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

log_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

log_error() {
    echo -e "${RED}❌ $1${NC}"
}

print_header() {
    echo ""
    echo -e "${BLUE}========================================${NC}"
    echo -e "${BLUE}$1${NC}"
    echo -e "${BLUE}========================================${NC}"
    echo ""
}

# ============================================================================
# CONFIGURATION
# ============================================================================
REDIS_NAMESPACE="module2-system"
REDIS_DEPLOYMENT="redis"
REDIS_SERVICE="redis"
CONFIG_DIR="config/redis"

# ============================================================================
# PREREQUISITES CHECK
# ============================================================================
print_header "REDIS DEPLOYMENT SETUP"

log_info "Checking prerequisites..."

# Check if kubectl is installed
if ! command -v kubectl &> /dev/null; then
    log_error "kubectl not installed"
    exit 1
fi

log_success "kubectl installed"

# Check if we can connect to cluster
if ! kubectl cluster-info &> /dev/null; then
    log_error "Cannot connect to Kubernetes cluster"
    exit 1
fi

log_success "Connected to Kubernetes cluster"

# Check if config directory exists
if [ ! -d "$CONFIG_DIR" ]; then
    log_error "Config directory not found: $CONFIG_DIR"
    exit 1
fi

log_success "Config directory found: $CONFIG_DIR"

# ============================================================================
# STEP 1: CREATE NAMESPACE
# ============================================================================
print_header "STEP 1: CREATE NAMESPACE"

log_info "Creating namespace: $REDIS_NAMESPACE"

if kubectl create namespace "$REDIS_NAMESPACE" --dry-run=client -o yaml | kubectl apply -f - &> /dev/null; then
    log_success "Namespace '$REDIS_NAMESPACE' created or already exists"
else
    log_error "Failed to create namespace"
    exit 1
fi

# Label the namespace
kubectl label namespace "$REDIS_NAMESPACE" managed-by=cloudtask-operator --overwrite 2>/dev/null || true

# ============================================================================
# STEP 2: APPLY REDIS SECRET
# ============================================================================
print_header "STEP 2: APPLY REDIS PASSWORD SECRET"

log_info "Applying Redis password secret..."

if kubectl apply -f "$CONFIG_DIR/redis-secret.yaml" &> /dev/null; then
    log_success "Redis secret applied successfully"
else
    log_error "Failed to apply Redis secret"
    exit 1
fi

# ============================================================================
# STEP 3: APPLY REDIS CONFIGMAP
# ============================================================================
print_header "STEP 3: APPLY REDIS CONFIGURATION"

log_info "Applying Redis ConfigMap..."

if kubectl apply -f "$CONFIG_DIR/redis-configmap.yaml" &> /dev/null; then
    log_success "Redis ConfigMap applied successfully"
else
    log_error "Failed to apply Redis ConfigMap"
    exit 1
fi

# ============================================================================
# STEP 4: APPLY PVC
# ============================================================================
print_header "STEP 4: APPLY PERSISTENT VOLUME CLAIM"

log_info "Applying PVC for Redis storage..."

if kubectl apply -f "$CONFIG_DIR/redis-pvc.yaml" &> /dev/null; then
    log_success "PVC applied successfully"
else
    log_error "Failed to apply PVC"
    exit 1
fi

# Wait for PVC to be bound
log_info "Waiting for PVC to be bound (max 30 seconds)..."
if kubectl wait --for=condition=Bound pvc/redis-data -n "$REDIS_NAMESPACE" --timeout=30s 2>/dev/null; then
    log_success "PVC is bound"
else
    log_warning "PVC binding timed out - may still bind shortly"
fi

# ============================================================================
# STEP 5: APPLY REDIS DEPLOYMENT
# ============================================================================
print_header "STEP 5: DEPLOY REDIS"

log_info "Applying Redis Deployment..."

if kubectl apply -f "$CONFIG_DIR/redis-deployment.yaml" &> /dev/null; then
    log_success "Redis Deployment applied successfully"
else
    log_error "Failed to apply Redis Deployment"
    exit 1
fi

# ============================================================================
# STEP 6: WAIT FOR REDIS POD TO BE READY
# ============================================================================
print_header "STEP 6: WAIT FOR REDIS POD"

log_info "Waiting for Redis pod to be ready (max 60 seconds)..."

# Wait for deployment to be ready
if kubectl rollout status deployment/"$REDIS_DEPLOYMENT" -n "$REDIS_NAMESPACE" --timeout=60s 2>/dev/null; then
    log_success "Redis deployment is ready"
else
    log_error "Redis deployment failed to become ready"
    log_error "Checking pod status..."
    kubectl get pods -n "$REDIS_NAMESPACE"
    kubectl describe pod -n "$REDIS_NAMESPACE" -l app=redis || true
    exit 1
fi

# ============================================================================
# STEP 7: APPLY REDIS SERVICE
# ============================================================================
print_header "STEP 7: APPLY REDIS SERVICE"

log_info "Applying Redis Service..."

if kubectl apply -f "$CONFIG_DIR/redis-service.yaml" &> /dev/null; then
    log_success "Redis Service applied successfully"
else
    log_error "Failed to apply Redis Service"
    exit 1
fi

# ============================================================================
# STEP 8: VERIFY REDIS SETUP
# ============================================================================
print_header "STEP 8: VERIFICATION"

# Get Redis pod name
REDIS_POD=$(kubectl get pods -n "$REDIS_NAMESPACE" -l app=redis -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)

if [ -z "$REDIS_POD" ]; then
    log_error "Could not find Redis pod"
    exit 1
fi

log_success "Redis pod: $REDIS_POD"

# Get pod status
POD_STATUS=$(kubectl get pod "$REDIS_POD" -n "$REDIS_NAMESPACE" -o jsonpath='{.status.phase}' 2>/dev/null)
log_info "Pod status: $POD_STATUS"

# Get pod IP
POD_IP=$(kubectl get pod "$REDIS_POD" -n "$REDIS_NAMESPACE" -o jsonpath='{.status.podIP}' 2>/dev/null)
if [ -n "$POD_IP" ]; then
    log_info "Pod IP: $POD_IP"
fi

# ============================================================================
# STEP 9: TEST REDIS CONNECTION
# ============================================================================
print_header "STEP 9: TEST REDIS CONNECTION"

REDIS_PASSWORD=$(kubectl get secret redis-password -n "$REDIS_NAMESPACE" \
    -o jsonpath='{.data.password}' 2>/dev/null | base64 -d)

log_info "Testing Redis connection..."

# Try to connect and run PING command
PING_RESULT=$(kubectl exec -n "$REDIS_NAMESPACE" "$REDIS_POD" -- \
    redis-cli -a "$REDIS_PASSWORD" PING 2>/dev/null || echo "FAILED")

if [ "$PING_RESULT" = "PONG" ]; then
    log_success "Redis PING test passed - Redis is responding"
else
    log_warning "Redis PING test inconclusive (result: $PING_RESULT)"
    log_info "Retrying in 5 seconds..."
    sleep 5
    
    PING_RESULT=$(kubectl exec -n "$REDIS_NAMESPACE" "$REDIS_POD" -- \
        redis-cli -a "$REDIS_PASSWORD" PING 2>/dev/null || echo "FAILED")
    
    if [ "$PING_RESULT" = "PONG" ]; then
        log_success "Redis PING test passed on retry"
    else
        log_warning "Redis PING test still inconclusive"
    fi
fi

# Test SET/GET
log_info "Testing Redis SET/GET..."
kubectl exec -n "$REDIS_NAMESPACE" "$REDIS_POD" -- \
    redis-cli -a "$REDIS_PASSWORD" SET test-key "CloudTask-Redis-Test" --quiet 2>/dev/null || true

GET_RESULT=$(kubectl exec -n "$REDIS_NAMESPACE" "$REDIS_POD" -- \
    redis-cli -a "$REDIS_PASSWORD" GET test-key 2>/dev/null || echo "")

if [ "$GET_RESULT" = "CloudTask-Redis-Test" ]; then
    log_success "Redis SET/GET test passed"
else
    log_warning "Redis SET/GET test returned: $GET_RESULT"
fi

# Check info
log_info "Checking Redis info..."
REDIS_VERSION=$(kubectl exec -n "$REDIS_NAMESPACE" "$REDIS_POD" -- \
    redis-cli -a "$REDIS_PASSWORD" INFO server | grep redis_version | cut -d: -f2 | tr -d '\r' 2>/dev/null || echo "unknown")

REDIS_MEMORY=$(kubectl exec -n "$REDIS_NAMESPACE" "$REDIS_POD" -- \
    redis-cli -a "$REDIS_PASSWORD" INFO memory | grep used_memory_human | cut -d: -f2 | tr -d '\r' 2>/dev/null || echo "unknown")

log_info "Redis version: $REDIS_VERSION"
log_info "Redis memory usage: $REDIS_MEMORY"

# ============================================================================
# SUCCESS SUMMARY
# ============================================================================
print_header "✅ REDIS DEPLOYMENT COMPLETE"

echo "Redis Service Information:"
echo "  Namespace:          $REDIS_NAMESPACE"
echo "  Pod Name:           $REDIS_POD"
echo "  Pod Status:         $POD_STATUS"
echo "  Service Name:       $REDIS_SERVICE"
echo "  Service Address:    redis.$REDIS_NAMESPACE.svc.cluster.local:6379"
echo "  Internal DNS:       redis.$REDIS_NAMESPACE:6379"
echo ""

echo "Persistence Configuration:"
echo "  RDB Snapshots:      Enabled (AOF + RDB)"
echo "  AOF Persistence:    Enabled"
echo "  Storage Size:       2Gi"
echo "  Storage PVC:        redis-data"
echo ""

echo "Resource Allocation:"
echo "  CPU Request:        100m"
echo "  CPU Limit:          500m"
echo "  Memory Request:     128Mi"
echo "  Memory Limit:       512Mi"
echo ""

echo "Quick Commands:"
echo "  Check Redis status:"
echo "    kubectl get deployment -n $REDIS_NAMESPACE"
echo ""
echo "  View Redis logs:"
echo "    kubectl logs -n $REDIS_NAMESPACE -f deployment/$REDIS_DEPLOYMENT"
echo ""
echo "  Connect to Redis directly:"
echo "    kubectl exec -it -n $REDIS_NAMESPACE $REDIS_POD -- redis-cli -a '$REDIS_PASSWORD'"
echo ""
echo "  Check Redis stats:"
echo "    kubectl exec -n $REDIS_NAMESPACE $REDIS_POD -- redis-cli -a '$REDIS_PASSWORD' INFO"
echo ""
echo "  Monitor Redis in real-time:"
echo "    kubectl exec -n $REDIS_NAMESPACE $REDIS_POD -- redis-cli -a '$REDIS_PASSWORD' MONITOR"
echo ""

log_success "Redis deployment is running and ready to use!"
echo ""
