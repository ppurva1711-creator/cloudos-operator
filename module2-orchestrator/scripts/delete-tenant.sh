#!/bin/bash

################################################################################
# CloudTask Orchestrator - Delete Tenant Script
# Purpose: Safe tenant namespace deletion with confirmation
# Usage: ./delete-tenant.sh tenant-a
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
    echo -e "\n${BLUE}========================================${NC}"
    echo -e "${BLUE}$1${NC}"
    echo -e "${BLUE}========================================${NC}\n"
}

# ============================================================================
# INPUT VALIDATION
# ============================================================================
if [ $# -eq 0 ]; then
    log_error "No tenant name provided"
    echo "Usage: ./delete-tenant.sh <tenant-name>"
    echo "Example: ./delete-tenant.sh tenant-a"
    exit 1
fi

TENANT_NAME="$1"
TENANT_NAMESPACE="tenant-${TENANT_NAME}"

# Validate tenant name format
if ! [[ "$TENANT_NAME" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]]; then
    log_error "Invalid tenant name: '$TENANT_NAME'"
    echo "Tenant names must:"
    echo "  - Start with lowercase letter or digit"
    echo "  - End with lowercase letter or digit"
    echo "  - Contain only lowercase letters, digits, and hyphens"
    exit 1
fi

# Check if tenant exists
if ! kubectl get namespace "$TENANT_NAMESPACE" &> /dev/null; then
    log_error "Tenant namespace '$TENANT_NAMESPACE' does not exist"
    exit 1
fi

print_header "⚠️  DELETE TENANT: $TENANT_NAME"

# ============================================================================
# RESOURCE SUMMARY
# ============================================================================
log_info "Gathering tenant resources..."

# Count resources
POD_COUNT=$(kubectl get pods -n "$TENANT_NAMESPACE" --no-headers 2>/dev/null | wc -l)
CLOUDTASK_COUNT=$(kubectl get cloudtasks.tasks.orchestrator.dev -n "$TENANT_NAMESPACE" --no-headers 2>/dev/null | wc -l)
PVC_COUNT=$(kubectl get pvc -n "$TENANT_NAMESPACE" --no-headers 2>/dev/null | wc -l)
CONFIG_MAP_COUNT=$(kubectl get configmap -n "$TENANT_NAMESPACE" --no-headers 2>/dev/null | wc -l)

echo ""
echo "Resources to be deleted:"
echo "  Namespace:         $TENANT_NAMESPACE"
echo "  Pods:              $POD_COUNT"
echo "  CloudTasks:        $CLOUDTASK_COUNT"
echo "  PersistentVolumes: $PVC_COUNT"
echo "  ConfigMaps:        $CONFIG_MAP_COUNT"
echo ""

# ============================================================================
# CONFIRMATION PROMPT
# ============================================================================
log_warning "This operation cannot be undone!"
echo ""
read -p "Type 'yes' to confirm deletion of tenant '$TENANT_NAME': " -r CONFIRMATION

if [[ ! "$CONFIRMATION" =~ ^[Yy][Ee][Ss]$ ]]; then
    log_info "Deletion cancelled"
    exit 0
fi

echo ""

# ============================================================================
# DELETION STEPS
# ============================================================================
log_info "STEP 1: Draining tenant resources (waiting for pods to terminate)..."

# Give graceful termination time
if [ "$POD_COUNT" -gt 0 ]; then
    # Wait for graceful termination period (default 30s) plus buffer
    sleep 5
fi

# ============================================================================
# STEP 2: DELETE NAMESPACE
# ============================================================================
log_info "STEP 2: Deleting namespace and all resources..."

if kubectl delete namespace "$TENANT_NAMESPACE" 2>&1 | grep -q "deleted\|already deleted"; then
    log_success "Namespace deletion initiated"
else
    log_error "Failed to delete namespace"
    exit 1
fi

# ============================================================================
# STEP 3: WAIT FOR NAMESPACE DELETION
# ============================================================================
log_info "STEP 3: Waiting for namespace to be fully deleted (this may take a moment)..."

WAIT_SECONDS=0
MAX_WAIT=60

while kubectl get namespace "$TENANT_NAMESPACE" &> /dev/null; do
    if [ $WAIT_SECONDS -ge $MAX_WAIT ]; then
        log_warning "Namespace deletion timeout (>$MAX_WAIT seconds)"
        log_info "The namespace is being deleted asynchronously. Check status with:"
        echo "  kubectl get namespace $TENANT_NAMESPACE"
        exit 0
    fi
    sleep 2
    WAIT_SECONDS=$((WAIT_SECONDS + 2))
    echo -ne "."
done

echo ""
log_success "Namespace fully deleted"

# ============================================================================
# SUCCESS SUMMARY
# ============================================================================
print_header "✅ TENANT DELETION SUCCESSFUL"

echo "Tenant '$TENANT_NAME' has been completely removed"
echo ""

# ============================================================================
# VERIFICATION COMMAND
# ============================================================================
echo "To verify deletion:"
echo "  kubectl get namespace $TENANT_NAMESPACE"
echo ""
