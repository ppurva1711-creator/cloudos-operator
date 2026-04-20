#!/bin/bash

################################################################################
# CloudTask Orchestrator - Create Tenant Script
# Purpose: Complete tenant provisioning with RBAC, quotas, and network policies
# Usage: ./create-tenant.sh tenant-a
# Date: 2026-04-19
################################################################################

set -o pipefail  # Exit if any command in pipe fails

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
    echo "Usage: ./create-tenant.sh <tenant-name>"
    echo "Example: ./create-tenant.sh tenant-a"
    exit 1
fi

TENANT_NAME="$1"
TENANT_NAMESPACE="tenant-${TENANT_NAME}"

# Validate tenant name (alphanumeric and hyphens only, must start/end with alphanumeric)
if ! [[ "$TENANT_NAME" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]]; then
    log_error "Invalid tenant name: '$TENANT_NAME'"
    echo "Tenant names must:"
    echo "  - Start with lowercase letter or digit"
    echo "  - End with lowercase letter or digit"
    echo "  - Contain only lowercase letters, digits, and hyphens"
    echo "  - Be between 1-63 characters"
    exit 1
fi

# Check if tenant already exists
if kubectl get namespace "$TENANT_NAMESPACE" &> /dev/null; then
    log_warning "Tenant namespace '$TENANT_NAMESPACE' already exists"
    read -p "Do you want to overwrite it? (yes/no): " -r RESPONSE
    if [[ ! "$RESPONSE" =~ ^[Yy][Ee][Ss]$ ]]; then
        log_info "Aborted tenant creation"
        exit 0
    fi
fi

print_header "CREATING TENANT: $TENANT_NAME"

# ============================================================================
# VERIFY REQUIRED FILES EXIST
# ============================================================================
log_info "Verifying required configuration files..."

REQUIRED_FILES=(
    "config/rbac/namespace-template.yaml"
    "config/rbac/resourcequota.yaml"
    "config/rbac/limitrange.yaml"
    "config/rbac/serviceaccount.yaml"
    "config/rbac/tenant-role.yaml"
    "config/rbac/tenant-rolebinding.yaml"
)

for file in "${REQUIRED_FILES[@]}"; do
    if [ ! -f "$file" ]; then
        log_error "Required file not found: $file"
        exit 1
    fi
done

log_success "All required files found"

# ============================================================================
# STEP 1: CREATE NAMESPACE
# ============================================================================
log_info "STEP 1: Creating namespace from template..."

NAMESPACE_YAML=$(cat "config/rbac/namespace-template.yaml" | \
    sed "s/\${TENANT_ID}/$TENANT_NAME/g" | \
    sed "s/\${TENANT_NAME}/$TENANT_NAME/g")

if echo "$NAMESPACE_YAML" | kubectl apply -f - &> /dev/null; then
    log_success "Namespace '$TENANT_NAMESPACE' created"
else
    log_error "Failed to create namespace"
    exit 1
fi

# Wait for namespace to be active
sleep 1

# ============================================================================
# STEP 2: APPLY RESOURCE QUOTA
# ============================================================================
log_info "STEP 2: Applying ResourceQuota..."

QUOTA_YAML=$(cat "config/rbac/resourcequota.yaml" | \
    sed "s/\${TENANT_ID}/$TENANT_NAME/g" | \
    sed "s/\${TENANT_NAME}/$TENANT_NAME/g" | \
    sed "s/\${TENANT_NAMESPACE}/$TENANT_NAMESPACE/g")

if echo "$QUOTA_YAML" | kubectl apply -f - &> /dev/null; then
    log_success "ResourceQuota applied to namespace"
else
    log_error "Failed to apply ResourceQuota"
    kubectl delete namespace "$TENANT_NAMESPACE" 2>/dev/null || true
    exit 1
fi

# ============================================================================
# STEP 3: APPLY LIMIT RANGE
# ============================================================================
log_info "STEP 3: Applying LimitRange..."

LIMIT_YAML=$(cat "config/rbac/limitrange.yaml" | \
    sed "s/\${TENANT_ID}/$TENANT_NAME/g" | \
    sed "s/\${TENANT_NAME}/$TENANT_NAME/g" | \
    sed "s/\${TENANT_NAMESPACE}/$TENANT_NAMESPACE/g")

if echo "$LIMIT_YAML" | kubectl apply -f - &> /dev/null; then
    log_success "LimitRange applied to namespace"
else
    log_error "Failed to apply LimitRange"
    kubectl delete namespace "$TENANT_NAMESPACE" 2>/dev/null || true
    exit 1
fi

# ============================================================================
# STEP 4: CREATE SERVICE ACCOUNT
# ============================================================================
log_info "STEP 4: Creating ServiceAccount..."

SA_YAML=$(cat "config/rbac/serviceaccount.yaml" | \
    sed "s/\${TENANT_ID}/$TENANT_NAME/g" | \
    sed "s/\${TENANT_NAME}/$TENANT_NAME/g" | \
    sed "s/\${TENANT_NAMESPACE}/$TENANT_NAMESPACE/g")

if echo "$SA_YAML" | kubectl apply -f - &> /dev/null; then
    log_success "ServiceAccount 'tenant-$TENANT_NAME' created"
else
    log_error "Failed to create ServiceAccount"
    kubectl delete namespace "$TENANT_NAMESPACE" 2>/dev/null || true
    exit 1
fi

# ============================================================================
# STEP 5: APPLY TENANT ROLE
# ============================================================================
log_info "STEP 5: Applying Tenant Role..."

ROLE_YAML=$(cat "config/rbac/tenant-role.yaml" | \
    sed "s/\${TENANT_ID}/$TENANT_NAME/g" | \
    sed "s/\${TENANT_NAME}/$TENANT_NAME/g" | \
    sed "s/\${TENANT_NAMESPACE}/$TENANT_NAMESPACE/g")

if echo "$ROLE_YAML" | kubectl apply -f - &> /dev/null; then
    log_success "Role 'tenant-role' created"
else
    log_error "Failed to apply Role"
    kubectl delete namespace "$TENANT_NAMESPACE" 2>/dev/null || true
    exit 1
fi

# ============================================================================
# STEP 6: APPLY ROLE BINDING
# ============================================================================
log_info "STEP 6: Applying RoleBinding..."

RB_YAML=$(cat "config/rbac/tenant-rolebinding.yaml" | \
    sed "s/\${TENANT_ID}/$TENANT_NAME/g" | \
    sed "s/\${TENANT_NAME}/$TENANT_NAME/g" | \
    sed "s/\${TENANT_NAMESPACE}/$TENANT_NAMESPACE/g")

if echo "$RB_YAML" | kubectl apply -f - &> /dev/null; then
    log_success "RoleBinding 'tenant-rolebinding' created"
else
    log_error "Failed to apply RoleBinding"
    kubectl delete namespace "$TENANT_NAMESPACE" 2>/dev/null || true
    exit 1
fi

# ============================================================================
# STEP 7: APPLY NETWORK POLICIES (if they exist)
# ============================================================================
if [ -d "config/network-policy" ]; then
    log_info "STEP 7: Applying NetworkPolicies..."
    
    NET_POLICY_FOUND=0
    
    # Apply tenant isolation network policy
    if [ -f "config/network-policy/tenant-isolation.yaml" ]; then
        NP_YAML=$(cat "config/network-policy/tenant-isolation.yaml" | \
            sed "s/\${TENANT_ID}/$TENANT_NAME/g" | \
            sed "s/\${TENANT_NAME}/$TENANT_NAME/g" | \
            sed "s/\${TENANT_NAMESPACE}/$TENANT_NAMESPACE/g")
        
        if echo "$NP_YAML" | kubectl apply -n "$TENANT_NAMESPACE" -f - &> /dev/null; then
            log_success "Tenant isolation NetworkPolicy applied"
            NET_POLICY_FOUND=1
        fi
    fi
    
    # Apply allow control-plane policy
    if [ -f "config/network-policy/allow-control-plane.yaml" ]; then
        NP_YAML=$(cat "config/network-policy/allow-control-plane.yaml" | \
            sed "s/\${TENANT_ID}/$TENANT_NAME/g" | \
            sed "s/\${TENANT_NAME}/$TENANT_NAME/g" | \
            sed "s/\${TENANT_NAMESPACE}/$TENANT_NAMESPACE/g")
        
        if echo "$NP_YAML" | kubectl apply -n "$TENANT_NAMESPACE" -f - &> /dev/null; then
            log_success "Allow control-plane NetworkPolicy applied"
            NET_POLICY_FOUND=1
        fi
    fi
    
    if [ $NET_POLICY_FOUND -eq 0 ]; then
        log_warning "No NetworkPolicy files found (optional)"
    fi
else
    log_warning "Network policy directory not found (optional)"
fi

# ============================================================================
# SUCCESS SUMMARY
# ============================================================================
print_header "✅ TENANT CREATION SUCCESSFUL"

echo "Tenant Name:       $TENANT_NAME"
echo "Namespace:         $TENANT_NAMESPACE"
echo ""
echo "Resources Created:"
echo "  ✅ Namespace"
echo "  ✅ ResourceQuota (CPU: 4-10 cores, Memory: 8-20 Gi)"
echo "  ✅ LimitRange (Container: 100m-2 cores, 128Mi-4Gi)"
echo "  ✅ ServiceAccount (tenant-$TENANT_NAME)"
echo "  ✅ Role (tenant-role)"
echo "  ✅ RoleBinding (tenant-rolebinding)"
echo "  ✅ NetworkPolicies (if available)"

# ============================================================================
# VERIFICATION COMMANDS
# ============================================================================
print_header "🔍 VERIFICATION COMMANDS"

echo "View namespace:"
echo "  kubectl describe namespace $TENANT_NAMESPACE"
echo ""
echo "View ServiceAccount:"
echo "  kubectl get serviceaccount -n $TENANT_NAMESPACE"
echo ""
echo "View RBAC resources:"
echo "  kubectl get role,rolebinding -n $TENANT_NAMESPACE"
echo ""
echo "View ResourceQuota usage:"
echo "  kubectl describe resourcequota tenant-quota -n $TENANT_NAMESPACE"
echo ""
echo "Test permissions:"
echo "  kubectl auth can-i list cloudtasks.tasks.orchestrator.dev --as=system:serviceaccount:$TENANT_NAMESPACE:tenant-$TENANT_NAME -n $TENANT_NAMESPACE"
echo ""
echo "Deploy a test CloudTask:"
echo "  kubectl apply -f - <<EOF"
echo "apiVersion: tasks.orchestrator.dev/v1"
echo "kind: CloudTask"
echo "metadata:"
echo "  name: test-task"
echo "  namespace: $TENANT_NAMESPACE"
echo "spec:"
echo "  image: alpine:latest"
echo "  command: ['/bin/sh', '-c']"
echo "  args: ['echo Hello from $TENANT_NAME']"
echo "  tenantID: $TENANT_NAME"
echo "  priority: 50"
echo "EOF"
echo ""

# ============================================================================
# QUICK SUMMARY
# ============================================================================
echo ""
log_success "Tenant '$TENANT_NAME' is ready for use!"
echo ""
