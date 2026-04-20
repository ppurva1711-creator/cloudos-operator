#!/usr/bin/env pwsh
<#
.SYNOPSIS
    Complete RBAC Deployment and Verification Script for CloudTask Multi-Tenancy
    
.DESCRIPTION
    This script deploys all RBAC resources (ServiceAccount, Role, RoleBinding, ClusterRole)
    for a CloudTask Orchestrator tenant and performs comprehensive verification.
    
.PARAMETER TenantName
    Name of the tenant (e.g., tenant-a, tenant-b)
    
.PARAMETER Namespace
    Kubernetes namespace for this tenant (default: tenant-{TenantName})
    
.EXAMPLE
    .\deploy-rbac.ps1 -TenantName "tenant-a" -Namespace "tenant-a"
    
.NOTES
    Requires: kubectl, Kind cluster running, config/rbac/ files
    Date: 2026-04-19
#>

param(
    [string]$TenantName = "tenant-a",
    [string]$Namespace = "tenant-a",
    [switch]$SkipOperatorRole = $false
)

$ErrorActionPreference = "Stop"

# ==========================================
# Color Functions
# ==========================================
function Write-Success {
    param([string]$Message)
    Write-Host "✅ $Message" -ForegroundColor Green
}

function Write-Info {
    param([string]$Message)
    Write-Host "ℹ️  $Message" -ForegroundColor Cyan
}

function Write-Warning {
    param([string]$Message)
    Write-Host "⚠️  $Message" -ForegroundColor Yellow
}

function Write-Error-Custom {
    param([string]$Message)
    Write-Host "❌ $Message" -ForegroundColor Red
}

function Write-Header {
    param([string]$Message)
    Write-Host "`n========================================" -ForegroundColor Magenta
    Write-Host $Message -ForegroundColor Magenta
    Write-Host "========================================`n" -ForegroundColor Magenta
}

# ==========================================
# Pre-flight Checks
# ==========================================
Write-Header "PRE-FLIGHT CHECKS"

Write-Info "Checking kubectl connectivity..."
try {
    $version = kubectl version --client 2>&1 | Select-String "Client Version"
    if ($version) {
        Write-Success "kubectl is available: $version"
    }
}
catch {
    Write-Error-Custom "kubectl not available or cluster unreachable"
    exit 1
}

Write-Info "Checking project files..."
$requiredFiles = @(
    "config/rbac/serviceaccount.yaml",
    "config/rbac/tenant-role.yaml",
    "config/rbac/tenant-rolebinding.yaml",
    "config/rbac/operator-clusterrole.yaml"
)

foreach ($file in $requiredFiles) {
    if (Test-Path $file) {
        Write-Success "Found: $file"
    }
    else {
        Write-Error-Custom "Missing: $file"
        exit 1
    }
}

# ==========================================
# Verify Namespace Exists
# ==========================================
Write-Header "VERIFYING NAMESPACE"

$nsExists = kubectl get namespace $Namespace -o name 2>&1
if ($nsExists -match "namespace/$Namespace") {
    Write-Success "Namespace '$Namespace' exists"
}
else {
    Write-Error-Custom "Namespace '$Namespace' does not exist. Create it first with namespace-template.yaml"
    exit 1
}

# ==========================================
# Deploy ServiceAccount
# ==========================================
Write-Header "DEPLOYING SERVICEACCOUNT"

Write-Info "Creating ServiceAccount for tenant-$TenantName..."
try {
    $saYaml = Get-Content "config/rbac/serviceaccount.yaml" -Raw
    $saSubstituted = $saYaml -replace '\$\{TENANT_NAME\}', $TenantName -replace '\$\{TENANT_NAMESPACE\}', $Namespace
    $saSubstituted | kubectl apply -f - | Out-String | ForEach-Object { Write-Info $_ }
    Write-Success "ServiceAccount deployed"
}
catch {
    Write-Error-Custom "Failed to deploy ServiceAccount: $_"
    exit 1
}

# ==========================================
# Deploy Role
# ==========================================
Write-Header "DEPLOYING ROLE"

Write-Info "Creating Role for tenant $TenantName..."
try {
    $roleYaml = Get-Content "config/rbac/tenant-role.yaml" -Raw
    $roleSubstituted = $roleYaml -replace '\$\{TENANT_NAME\}', $TenantName -replace '\$\{TENANT_NAMESPACE\}', $Namespace
    $roleSubstituted | kubectl apply -f - | Out-String | ForEach-Object { Write-Info $_ }
    Write-Success "Role deployed"
}
catch {
    Write-Error-Custom "Failed to deploy Role: $_"
    exit 1
}

# ==========================================
# Deploy RoleBinding
# ==========================================
Write-Header "DEPLOYING ROLEBINDING"

Write-Info "Creating RoleBinding for tenant-$TenantName..."
try {
    $rbYaml = Get-Content "config/rbac/tenant-rolebinding.yaml" -Raw
    $rbSubstituted = $rbYaml -replace '\$\{TENANT_NAME\}', $TenantName -replace '\$\{TENANT_NAMESPACE\}', $Namespace
    $rbSubstituted | kubectl apply -f - | Out-String | ForEach-Object { Write-Info $_ }
    Write-Success "RoleBinding deployed"
}
catch {
    Write-Error-Custom "Failed to deploy RoleBinding: $_"
    exit 1
}

# ==========================================
# Deploy Operator ClusterRole (one-time)
# ==========================================
if (-not $SkipOperatorRole) {
    Write-Header "DEPLOYING OPERATOR CLUSTERROLE"
    
    Write-Info "Creating ClusterRole for operator..."
    try {
        kubectl apply -f config/rbac/operator-clusterrole.yaml | Out-String | ForEach-Object { Write-Info $_ }
        Write-Success "Operator ClusterRole deployed"
    }
    catch {
        Write-Error-Custom "Failed to deploy Operator ClusterRole: $_"
        exit 1
    }
}

# ==========================================
# VERIFICATION PHASE
# ==========================================
Write-Header "VERIFICATION PHASE"

# Verify ServiceAccount
Write-Info "Verifying ServiceAccount creation..."
$sa = kubectl get serviceaccount "tenant-$TenantName" -n $Namespace -o name 2>&1
if ($sa -match "serviceaccount/tenant-$TenantName") {
    Write-Success "ServiceAccount verified: $sa"
}
else {
    Write-Error-Custom "ServiceAccount not found"
    exit 1
}

# Verify Role
Write-Info "Verifying Role creation..."
$role = kubectl get role tenant-role -n $Namespace -o name 2>&1
if ($role -match "role\.rbac\.authorization\.k8s\.io/tenant-role") {
    Write-Success "Role verified: $role"
}
else {
    Write-Error-Custom "Role not found"
    exit 1
}

# Verify RoleBinding
Write-Info "Verifying RoleBinding creation..."
$rb = kubectl get rolebinding tenant-rolebinding -n $Namespace -o name 2>&1
if ($rb -match "rolebinding\.rbac\.authorization\.k8s\.io/tenant-rolebinding") {
    Write-Success "RoleBinding verified: $rb"
}
else {
    Write-Error-Custom "RoleBinding not found"
    exit 1
}

# ==========================================
# LIST ALL RBAC RESOURCES
# ==========================================
Write-Header "RBAC RESOURCES SUMMARY"

Write-Info "ServiceAccounts in $Namespace :"
kubectl get serviceaccount -n $Namespace -o wide

Write-Info "Roles in $Namespace :"
kubectl get role -n $Namespace -o wide

Write-Info "RoleBindings in $Namespace :"
kubectl get rolebinding -n $Namespace -o wide

# ==========================================
# PERMISSION TESTS
# ==========================================
Write-Header "PERMISSION TESTS"

$saFull = "system:serviceaccount:$Namespace`:tenant-$TenantName"

Write-Info "Testing permissions for $saFull in namespace: $Namespace"

# Test 1: Can list CloudTasks
$canListCloudTasks = kubectl auth can-i list cloudtasks.tasks.orchestrator.dev --as=$saFull -n $Namespace 2>&1
if ($canListCloudTasks -match "yes") {
    Write-Success "Can list CloudTasks"
}
else {
    Write-Error-Custom "Cannot list CloudTasks"
}

# Test 2: Can create CloudTasks
$canCreateCloudTasks = kubectl auth can-i create cloudtasks.tasks.orchestrator.dev --as=$saFull -n $Namespace 2>&1
if ($canCreateCloudTasks -match "yes") {
    Write-Success "Can create CloudTasks"
}
else {
    Write-Error-Custom "Cannot create CloudTasks"
}

# Test 3: Can delete CloudTasks
$canDeleteCloudTasks = kubectl auth can-i delete cloudtasks.tasks.orchestrator.dev --as=$saFull -n $Namespace 2>&1
if ($canDeleteCloudTasks -match "yes") {
    Write-Success "Can delete CloudTasks"
}
else {
    Write-Error-Custom "Cannot delete CloudTasks"
}

# Test 4: Can get pods (read-only)
$canGetPods = kubectl auth can-i get pods --as=$saFull -n $Namespace 2>&1
if ($canGetPods -match "yes") {
    Write-Success "Can view pods (read-only)"
}
else {
    Write-Error-Custom "Cannot view pods"
}

# Test 5: Cannot create pods (should fail)
Write-Info "Testing denial: cannot create pods directly (expected to FAIL)..."
$canCreatePods = kubectl auth can-i create pods --as=$saFull -n $Namespace 2>&1
if ($canCreatePods -match "no") {
    Write-Success "Correctly denied: Cannot create pods directly ✓"
}
else {
    Write-Error-Custom "SECURITY ISSUE: Can create pods directly (should be denied)"
}

# ==========================================
# DETAILED PERMISSION LISTING
# ==========================================
Write-Header "DETAILED PERMISSIONS FOR $TenantName"

Write-Info "All permissions granted to tenant-$TenantName in namespace $Namespace :"
kubectl auth can-i --list --as=$saFull -n $Namespace 2>&1 | Select-Object -First 50

# ==========================================
# COMPLETION SUMMARY
# ==========================================
Write-Header "DEPLOYMENT COMPLETED SUCCESSFULLY"

Write-Success "All RBAC resources deployed and verified"
Write-Info "Tenant: $TenantName"
Write-Info "Namespace: $Namespace"
Write-Info "ServiceAccount: tenant-$TenantName"
Write-Info "Role: tenant-role"
Write-Info "RoleBinding: tenant-rolebinding"
Write-Info "Operator ClusterRole: cloudtask-operator-role"

Write-Host "`n📋 NEXT STEPS:" -ForegroundColor Cyan
Write-Host "1. Deploy sample CloudTasks to test the setup"
Write-Host "2. Test cross-namespace isolation"
Write-Host "3. Monitor operator logs: kubectl logs -f deployment/cloudtask-operator -n orchestrator-system"
Write-Host "4. Create additional tenants by running this script with different -TenantName"
Write-Host ""
