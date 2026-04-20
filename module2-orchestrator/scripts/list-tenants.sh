#!/bin/bash

################################################################################
# CloudTask Orchestrator - List Tenants Script
# Purpose: Display tenant inventory with resource metrics and quota usage
# Usage: ./list-tenants.sh
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
GRAY='\033[0;37m'
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

# Convert percentage to color
color_status() {
    local percent=$1
    if [ "$percent" -lt 50 ]; then
        echo -e "${GREEN}$percent%${NC}"
    elif [ "$percent" -lt 75 ]; then
        echo -e "${YELLOW}$percent%${NC}"
    else
        echo -e "${RED}$percent%${NC}"
    fi
}

# ============================================================================
# MAIN LOGIC
# ============================================================================
print_header "📊 CLOUDTASK ORCHESTRATOR - TENANT INVENTORY"

# Check if kubectl is available
if ! command -v kubectl &> /dev/null; then
    log_error "kubectl not installed. Please install kubectl."
    exit 1
fi

# Get all tenant namespaces
TENANT_NAMESPACES=$(kubectl get namespaces -l managed-by=cloudtask-operator -o jsonpath='{.items[*].metadata.name}' 2>/dev/null)

if [ -z "$TENANT_NAMESPACES" ]; then
    log_warning "No tenants found"
    echo ""
    echo "To create a tenant, run:"
    echo "  ./create-tenant.sh tenant-a"
    exit 0
fi

# ============================================================================
# TABLE HEADER
# ============================================================================
printf "${CYAN}%-20s %-12s %-12s %-12s %-15s %-15s${NC}\n" \
    "TENANT NAME" "PODS" "CLOUDTASKS" "PVCs" "CPU USAGE" "MEMORY USAGE"
printf "${GRAY}%-20s %-12s %-12s %-12s %-15s %-15s${NC}\n" \
    "$(printf '%0.s-' {1..20})" "$(printf '%0.s-' {1..12})" "$(printf '%0.s-' {1..12})" "$(printf '%0.s-' {1..12})" "$(printf '%0.s-' {1..15})" "$(printf '%0.s-' {1..15})"

# ============================================================================
# ITERATE THROUGH TENANTS
# ============================================================================
TOTAL_PODS=0
TOTAL_CLOUDTASKS=0
TOTAL_PVCS=0

for NS in $TENANT_NAMESPACES; do
    # Extract tenant name from namespace
    TENANT_NAME="${NS#tenant-}"
    
    # Get pod count
    POD_COUNT=$(kubectl get pods -n "$NS" --no-headers 2>/dev/null | wc -l)
    TOTAL_PODS=$((TOTAL_PODS + POD_COUNT))
    
    # Get CloudTask count
    CLOUDTASK_COUNT=$(kubectl get cloudtasks.tasks.orchestrator.dev -n "$NS" --no-headers 2>/dev/null | wc -l)
    TOTAL_CLOUDTASKS=$((TOTAL_CLOUDTASKS + CLOUDTASK_COUNT))
    
    # Get PVC count
    PVC_COUNT=$(kubectl get pvc -n "$NS" --no-headers 2>/dev/null | wc -l)
    TOTAL_PVCS=$((TOTAL_PVCS + PVC_COUNT))
    
    # Get ResourceQuota usage
    CPU_USED_RAW=$(kubectl get resourcequota -n "$NS" -o jsonpath='{.items[*].status.used.requests\.cpu}' 2>/dev/null | tr -d ' ')
    CPU_HARD_RAW=$(kubectl get resourcequota -n "$NS" -o jsonpath='{.items[*].status.hard.requests\.cpu}' 2>/dev/null | tr -d ' ')
    
    MEM_USED_RAW=$(kubectl get resourcequota -n "$NS" -o jsonpath='{.items[*].status.used.requests\.memory}' 2>/dev/null | tr -d ' ')
    MEM_HARD_RAW=$(kubectl get resourcequota -n "$NS" -o jsonpath='{.items[*].status.hard.requests\.memory}' 2>/dev/null | tr -d ' ')
    
    # Parse and calculate percentages
    CPU_USED=${CPU_USED_RAW:-0}
    CPU_HARD=${CPU_HARD_RAW:-4}
    CPU_PERCENT=0
    if [ "$CPU_HARD" != "0" ]; then
        CPU_PERCENT=$((CPU_USED * 100 / CPU_HARD))
    fi
    
    # Convert memory from bytes to Gi for display
    MEM_USED_GI=$(echo "scale=2; ${MEM_USED_RAW:-0} / 1073741824" | bc 2>/dev/null || echo "0")
    MEM_HARD_GI=$(echo "scale=2; ${MEM_HARD_RAW:-8589934592} / 1073741824" | bc 2>/dev/null || echo "8")
    MEM_PERCENT=0
    if [ "${MEM_HARD_RAW:-0}" != "0" ]; then
        MEM_PERCENT=$((${MEM_USED_RAW:-0} * 100 / ${MEM_HARD_RAW:-8589934592}))
    fi
    
    # Format display strings
    CPU_DISPLAY="${CPU_USED}/${CPU_HARD} $(color_status $CPU_PERCENT)"
    MEM_DISPLAY="${MEM_USED_GI}/${MEM_HARD_GI}Gi $(color_status $MEM_PERCENT)"
    
    # Print row
    printf "%-20s %-12s %-12s %-12s %-15s %-15s\n" \
        "$TENANT_NAME" "$POD_COUNT" "$CLOUDTASK_COUNT" "$PVC_COUNT" "$CPU_DISPLAY" "$MEM_DISPLAY"
done

# ============================================================================
# SUMMARY FOOTER
# ============================================================================
echo ""
printf "${GRAY}%-20s %-12s %-12s %-12s${NC}\n" \
    "$(printf '%0.s-' {1..20})" "$(printf '%0.s-' {1..12})" "$(printf '%0.s-' {1..12})" "$(printf '%0.s-' {1..12})"

printf "%-20s %-12s %-12s %-12s\n" \
    "TOTAL" "$TOTAL_PODS" "$TOTAL_CLOUDTASKS" "$TOTAL_PVCS"

echo ""

# ============================================================================
# LEGEND & USAGE HELP
# ============================================================================
print_header "📖 LEGEND & QUICK COMMANDS"

echo "Column Descriptions:"
echo "  TENANT NAME     - Tenant identifier"
echo "  PODS            - Number of running pods"
echo "  CLOUDTASKS      - Number of CloudTasks (regardless of status)"
echo "  PVCs            - Number of PersistentVolumeClaims"
echo "  CPU USAGE       - Requested CPU (current/limit with usage %) - $(color_status 30) <50%, $(color_status 60) 50-75%, $(color_status 90) >75%"
echo "  MEMORY USAGE    - Requested Memory (current/limit with usage %)"
echo ""

echo "Quick Commands:"
echo "  View tenant details:"
echo "    kubectl describe namespace tenant-<name>"
echo ""
echo "  View tenant pods:"
echo "    kubectl get pods -n tenant-<name>"
echo ""
echo "  View tenant CloudTasks:"
echo "    kubectl get cloudtasks.tasks.orchestrator.dev -n tenant-<name>"
echo ""
echo "  View resource quota:"
echo "    kubectl describe resourcequota -n tenant-<name>"
echo ""
echo "  Create new tenant:"
echo "    ./create-tenant.sh <tenant-name>"
echo ""
echo "  Delete tenant:"
echo "    ./delete-tenant.sh <tenant-name>"
echo ""
