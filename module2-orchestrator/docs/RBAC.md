# RBAC & Multi-Tenancy Architecture - Complete Guide

**Date**: April 19, 2026  
**Version**: 1.0  
**Status**: Production-Ready

## Table of Contents

1. [Overview & Architecture](#overview--architecture)
2. [Namespace Design](#namespace-design)
3. [RBAC Architecture](#rbac-architecture)
4. [NetworkPolicy Architecture](#networkpolicy-architecture)
5. [Resource Management](#resource-management)
6. [Tenant Onboarding Process](#tenant-onboarding-process)
7. [Tenant Offboarding Process](#tenant-offboarding-process)
8. [RBAC Validation Procedures](#rbac-validation-procedures)
9. [Troubleshooting Guide](#troubleshooting-guide)
10. [Quick Reference](#quick-reference)

---

## 1. Overview & Architecture

### Why Per-Tenant Namespace Isolation?

The CloudTask Orchestrator uses a **one-namespace-per-tenant** architecture to provide strong isolation and security guarantees in a shared Kubernetes cluster. This design pattern offers several critical benefits:

#### Security Isolation
- **Mandatory namespace boundary**: Each tenant's workloads, data, and configuration are isolated within their own namespace
- **No cross-tenant access** by default: NetworkPolicies and RBAC enforcements prevent one tenant from accessing another's resources
- **Resource quotas**: Prevent one tenant's workloads from consuming all cluster resources (noisy neighbor problem)
- **LimitRange enforcement**: Prevents resource-hungry pods from being deployed

#### Operational Clarity
- **Clear ownership**: Each namespace belongs to exactly one tenant
- **Simple onboarding/offboarding**: Create or delete a namespace and all tenant resources go with it
- **Resource tracking**: Easy to see which tenant is consuming resources
- **Audit trail**: Namespace-level events and logs clearly show which tenant is responsible

#### Scalability
- **Linear growth**: Adding tenants doesn't increase cluster management complexity
- **Independent quotas**: Each tenant has independent resource limits
- **Independent policies**: Network policies, RBAC roles, and resource limits are tenant-specific

### Architecture Diagram

```
                    Kubernetes Cluster (Kind v1.28.0)
        
        ┌─────────────────────────────────────────────────────┐
        │                 orchestrator-system                  │
        │                    (Operator)                        │
        │  ┌────────────────────────────────────────────────┐  │
        │  │ CloudTask Operator (controller-manager)        │  │
        │  │  - Watches all CloudTasks in all namespaces    │  │
        │  │  - Creates/manages pods across tenants         │  │
        │  │  - Has cluster-wide RBAC permissions           │  │
        │  └────────────────────────────────────────────────┘  │
        └─────────────────────────────────────────────────────┘
                               │
          ┌────────────────────┼────────────────────┬───────────────┐
          │                    │                    │               │
    ┌─────▼─────┐         ┌─────▼─────┐        ┌─────▼─────┐   ┌──▼──┐
    │  tenant-a │         │  tenant-b │        │  tenant-c │   │ ... │
    │ Namespace │         │ Namespace │        │ Namespace │   │     │
    │           │         │           │        │           │   │     │
    │ ┌───────┐ │         │ ┌───────┐ │        │ ┌───────┐ │   │     │
    │ │SA: a  │ │         │ │SA: b  │ │        │ │SA: c  │ │   │     │
    │ │Role   │ │         │ │Role   │ │        │ │Role   │ │   │     │
    │ │RB: a→SA│ │         │ │RB: b→SA│ │        │ │RB: c→SA│ │   │     │
    │ │QA/LR  │ │         │ │QA/LR  │ │        │ │QA/LR  │ │   │     │
    │ │NP      │ │         │ │NP      │ │        │ │NP      │ │   │     │
    │ │        │ │         │ │        │ │        │ │        │ │   │     │
    │ │Pods    │ │         │ │Pods    │ │        │ │Pods    │ │   │     │
    │ └───────┘ │         │ └───────┘ │        │ └───────┘ │   │     │
    └───────────┘         └───────────┘        └───────────┘   └─────┘
    (Isolated)            (Isolated)            (Isolated)       ...
    
Legend:
  SA = ServiceAccount (tenant identity)
  Role = Namespaced role (permissions for tenant workloads)
  RB = RoleBinding (links Role to ServiceAccount)
  QA = ResourceQuota (CPU/Memory/Pod limits)
  LR = LimitRange (container resource defaults)
  NP = NetworkPolicy (pod-to-pod traffic control)
```

### Security Model Layers

The CloudTask Orchestrator implements **defense in depth** with multiple overlapping security layers:

```
Layer 1: RBAC (Role-Based Access Control)
  ├─ ClusterRole: operator (cluster-wide, trusted system component)
  └─ Role: tenant-role (namespaced, tenants can only CRUD own resources)

Layer 2: NetworkPolicy (Network Traffic Isolation)
  ├─ Deny all ingress (block by default)
  ├─ Deny all egress (block by default)
  ├─ Allow control-plane communication (operator ↔ tenant pods)
  ├─ Allow pod-to-pod within tenant (tenant-a pods can talk to each other)
  └─ Allow external egress (tenant pods can reach internet)

Layer 3: ResourceQuota (Resource Consumption Limits)
  ├─ CPU requests: 4 cores (reserved capacity)
  ├─ CPU limits: 10 cores (burst ceiling)
  ├─ Memory requests: 8 Gi
  ├─ Memory limits: 20 Gi
  └─ Pod count: 100 pods max

Layer 4: LimitRange (Container-level Defaults & Bounds)
  ├─ Container CPU request: 100m (default)
  ├─ Container CPU limit: 500m (default)
  ├─ Container Memory request: 128Mi (default)
  ├─ Container Memory limit: 512Mi (default)
  └─ Container max: 2 cores / 4Gi Memory
```

---

## 2. Namespace Design

### One Namespace Per Tenant Pattern

Each tenant operates within a dedicated Kubernetes namespace following this pattern:

**Naming Convention:**
```
tenant-{tenant-name}
```

**Examples:**
- `tenant-acme` - ACME Corporation
- `tenant-prod-01` - Production environment 1
- `tenant-staging` - Staging environment
- `tenant-dev-team-a` - Development team A

**Naming Rules:**
- Lowercase letters and digits only
- Hyphens allowed (not at start or end)
- 1-63 characters total
- Must match K8s DNS naming (RFC 1123)

### Namespace Labels and Annotations

Each tenant namespace is labeled and annotated for discovery, organization, and auditing:

#### Labels (Machine-Readable)

```yaml
labels:
  managed-by: cloudtask-operator        # Identifies this as a managed tenant namespace
  app: cloudtask-orchestrator            # Application identifier
  tenant-id: {tenant-name}               # Tenant identifier
  environment: production                # Environment (dev/staging/prod)
  billing-code: "ACME-001"              # For cost allocation
```

**Why these labels?**
- `managed-by` allows kubectl label selectors to find all tenants: `-l managed-by=cloudtask-operator`
- `tenant-id` for programmatic tenant identification and filtering
- `environment`, `billing-code` enable ops team resource tracking and chargeback models

#### Annotations (Human-Readable)

```yaml
annotations:
  description: "ACME Corporation production environment"
  contact: "devops@acme.com"
  owner-team: "Platform Engineering"
  backup-policy: "daily-incremental"
  rbac-scope: "namespace-isolated"
  created-by: "create-tenant.sh v1.0"
  created-date: "2026-04-19T12:34:56Z"
```

**Why annotations?**
- `description`, `contact` provide human context for troubleshooting
- `backup-policy`, `owner-team` document operational procedures
- `created-by`, `created-date` provide audit trail

### Namespace Lifecycle

```
┌─────────────┐   create-tenant.sh    ┌────────────┐   kubectl delete ns    ┌──────────┐
│   pending   │ ─────────────────────► │   active   │ ──────────────────────► │ deleted  │
└─────────────┘                        └────────────┘                        └──────────┘
                                           │
                                           ├─ Resources deployed
                                           ├─ Pods running CloudTasks
                                           ├─ ResourceQuota enforced
                                           └─ NetworkPolicies active
```

---

## 3. RBAC Architecture

### Kubernetes RBAC Primer

Kubernetes RBAC uses four objects to define access control:

1. **Role/ClusterRole**: Defines permissions (what can be done)
2. **RoleBinding/ClusterRoleBinding**: Links role to subjects (who can do it)
3. **ServiceAccount**: Identity for pods and users
4. **Subject**: User, group, or ServiceAccount being granted permissions

### ClusterRole for Operator (Cluster-Wide)

The operator needs **cluster-wide** permissions to orchestrate CloudTasks across all tenant namespaces.

```yaml
# cloudtask-operator-role (ClusterRole)
# Scope: Cluster-wide (applies to all namespaces)
# Used by: orchestrator-controller-manager ServiceAccount in orchestrator-system namespace

Permissions:
  ├─ CloudTasks (all namespaces)
  │  ├─ create, get, list, watch, update, patch, delete
  │  └─ Reason: Operator creates/manages CloudTasks in all tenant namespaces
  │
  ├─ Pods (all namespaces)
  │  ├─ create, get, list, watch, update, patch, delete, exec, logs
  │  └─ Reason: Operator creates pods when CloudTask executes, needs to access logs/exec
  │
  ├─ Namespaces (all namespaces)
  │  ├─ get, list, watch, create, patch
  │  └─ Reason: Operator monitors namespace creation for compliance
  │
  ├─ Events (all namespaces)
  │  ├─ create, get, list, watch
  │  └─ Reason: Operator records events for audit and debugging
  │
  ├─ NetworkPolicies (all namespaces)
  │  ├─ get, list, watch
  │  └─ Reason: Operator validates network isolation
  │
  └─ Plus: ResourceQuotas, PersistentVolumes, Nodes, Metrics, KEDA, Batch API
     └─ Reason: For sophisticated multi-tenant orchestration features
```

**Why ClusterRole?**
- Operator is the **trusted system component** - it manages the entire cluster
- Must operate across all namespaces but under strict control (deployment in orchestrator-system only)
- More secure than giving tenants cluster-wide permissions

### Role for Tenant Users (Namespaced)

Each tenant gets a **namespaced Role** limiting their access to their own namespace.

```yaml
# tenant-role (Role, not ClusterRole!)
# Scope: Single namespace (tenant-a, tenant-b, etc.)
# Used by: tenant-{name} ServiceAccount in tenant-{name} namespace

Permissions (limited to own namespace):
  ├─ CloudTasks
  │  ├─ create, get, list, watch, update, patch, delete
  │  └─ Reason: Tenants manage their own CloudTasks
  │
  ├─ Pods
  │  ├─ get, list, watch, logs
  │  └─ Reason: Tenants view their pods (created by operator from CloudTasks)
  │  └─ NO create: Tenants cannot create pods directly (only via CloudTasks)
  │
  ├─ Events
  │  ├─ get, list, watch
  │  └─ Reason: Tenants debug via events
  │
  ├─ ConfigMaps
  │  ├─ get, list
  │  └─ Reason: Access configuration data
  │
  ├─ Secrets
  │  ├─ get, list
  │  └─ Reason: Access sensitive data (credentials, keys)
  │
  ├─ PersistentVolumeClaims
  │  ├─ create, get, list, watch, update, patch, delete
  │  └─ Reason: Manage persistent storage for workloads
  │
  └─ Resource limits: NO access to RBAC, Namespaces, ResourceQuota, Nodes
```

**Why Role (namespaced) not ClusterRole?**
- Prevents tenant from seeing/accessing other namespaces
- Each tenant gets their own independent Role instance
- More secure - each role is isolated to one namespace

### RoleBinding and ServiceAccount

RoleBindings connect Roles to identities. Each tenant has:

```yaml
# tenant-serviceaccount (ServiceAccount)
# Namespace: tenant-a
# Identity: system:serviceaccount:tenant-a:tenant-a

# tenant-rolebinding (RoleBinding)
# Namespace: tenant-a
# Links: tenant-role (Role) → tenant-a (ServiceAccount)
# Effect: All pods using tenant-a ServiceAccount get tenant-role permissions
```

**How it works:**

1. Pod in tenant-a is created with auto-mount ServiceAccount
2. Kubelet mounts tenant-a token into pod at `/var/run/secrets/kubernetes.io/serviceaccount/token`
3. Pod makes API call: `curl -H "Authorization: Bearer {token}" https://kubernetes.default.svc/api/...`
4. Kubernetes API server checks: Does token belong to tenant-a in tenant-a namespace?
5. RBAC determines: tenant-a RoleBinding grants tenant-role permissions
6. API allows/denies based on matching rule in tenant-role

### Permission Matrix

| Action | Tenant-A | Tenant-B | Operator | Result |
|--------|----------|----------|----------|--------|
| Create CloudTask in tenant-a | ✅ | ❌ | ✅ | Tenant-a can only manage own tasks |
| Create CloudTask in tenant-b | ❌ | ✅ | ✅ | Tenant-b cannot access tenant-a |
| Create Pod in tenant-a | ❌ | ❌ | ✅ | Only operator creates pods |
| List Pods in tenant-a | ✅ | ❌ | ✅ | Tenants see only own pods |
| Access secret in tenant-a | ✅ | ❌ | ✅ | Namespace isolation enforced |
| List other namespaces | ❌ | ❌ | ✅ | Tenants blind to other tenants |

---

## 4. NetworkPolicy Architecture

### Default Deny Approach

The CloudTask Orchestrator uses an **explicit allow** model for network traffic:

```
Default: All traffic DENIED
Exception: Only explicitly allowed traffic is permitted
```

This is more secure than the inverse (allow all, then deny bad traffic) because:
- New vulnerabilities in one tenant cannot affect others
- Accidental misconfiguration defaults to secure
- Forces explicit thought about required connections

### Policy Layering Strategy

Network policies are stacked in layers, each building on the previous:

```
Layer 1: Foundation
  └─ deny-all-ingress.yaml
     └─ DROP all incoming traffic to pods (defense against unsolicited inbound)

Layer 2: Foundation  
  └─ deny-all-egress.yaml
     └─ DROP all outgoing traffic from pods (defense against data exfiltration)

Layer 3: System Communication
  └─ allow-control-plane.yaml
     └─ ALLOW: Operator → tenant pods (operator needs to exec/log)
     └─ ALLOW: kubelet → tenant pods (health checks, metrics)

Layer 4: Tenant Internal
  └─ tenant-isolation.yaml
     └─ ALLOW: pod-to-pod within same namespace
     └─ KEEP DENY: cross-namespace communication

Layer 5: External Communication
  └─ allow-external-egress.yaml
     └─ ALLOW: DNS (port 53 to 0.0.0.0/0)
     └─ ALLOW: HTTP/HTTPS (port 80, 443 to 0.0.0.0/0)
     └─ Reason: Tenants often need to call external APIs
```

### Traffic Flow Diagram

```
Scenario: tenant-a pod needs to reach www.google.com

  tenant-a pod          [DNS query]
        │                   │
        ├─────────────────────┬──────────────────────────────┐
        │                     │                              │
   [Policy 1]           [Policy 1]                       [Policy 2]
   deny-all             deny-all              allow-control-plane
   -ingress             -egress                  ALLOW
   (not relevant)       CHECK                  (not relevant)
                        BLOCKED? NO
                             │
                             NO (allow-external-egress permits DNS)
                             │
                        [Policy 5]
                    allow-external-egress
                    Port 53 to 0.0.0.0/0
                         ALLOW
                             │
                        DNS resolves
                             │
                    [HTTP request]
                             │
        ┌────────────────────┴──────────────────────────────┐
        │                                                    │
   [Policy check]                                      [Policy 5]
   Same as above but                              allow-external-egress
   Port 443 to 0.0.0.0/0                          Port 443 to 0.0.0.0/0
   ALLOW TO google.com                                ALLOW
        │                                                    │
        └────────────────────┬──────────────────────────────┘
                             │
                          SUCCESS


Scenario: tenant-a pod tries to reach tenant-b pod (same cluster)

  tenant-a pod
        │
    [CURL request to tenant-b pod IP]
        │
   [Policy 1] deny-all-ingress → NOT RELEVANT (pod is in tenant-a)
   [Policy 2] deny-all-egress → CHECK - Is this allowed?
   [Policy 4] tenant-isolation → "Allow pods in tenant-a ns to talk to tenant-a ns"
                                  Destination is tenant-b - NOT in tenant-a
                                  BLOCKED by tenant-isolation (keeps deny active)
   
   TRAFFIC DENIED - tenant-a cannot reach tenant-b
```

### Why This Works with Kind CNI (kindnet)

Kind uses **kindnet** as its CNI (Container Network Interface). Important property for NetworkPolicies:

```
kindnet: Does NOT enforce NetworkPolicies by default!
         But Kind includes Calico for NetworkPolicy enforcement
```

Kind clusters automatically include **Calico** for NetworkPolicy support, which:
- Watches NetworkPolicy objects in etcd
- Enforces rules via iptables/ebpf on each node
- Blocks cross-namespace traffic as specified
- Allows in-namespace traffic unless explicitly denied

---

## 5. Resource Management

### ResourceQuota: Cluster Resource Division

ResourceQuotas prevent single-tenant workloads from consuming cluster resources unfairly.

**Per-Tenant Limits:**

```yaml
# tenant-quota (ResourceQuota in each tenant namespace)

Compute Resources:
  requests.cpu: "4"              # Guaranteed reserved CPU for tenant
  limits.cpu: "10"               # Burst ceiling - tenant cannot exceed
  requests.memory: "8Gi"         # Guaranteed reserved memory
  limits.memory: "20Gi"          # Burst ceiling

Pod Limits:
  pods: "100"                    # Max 100 pods in tenant namespace

Storage:
  persistentvolumeclaims: "10"   # Max 10 persistent volumes

Custom Resources:
  cloudtasks.tasks.orchestrator.dev: "1000"  # Max 1000 CloudTasks per tenant (soft limit)
```

**Why These Limits?**

For a 3-tenant Kind cluster running on a development machine:

```
Typical Kind cluster:
  - Control plane: 2 cores, 4 Gi RAM
  - Node: 4 cores, 8 Gi RAM available for workloads
  - Total: 4 cores, 8 Gi for all tenants

Division strategy (fair share):
  - Tenant-A: CPU request 4/3 = 1.33, limit 10/3 = 3.33
  - Tenant-B: CPU request 4/3 = 1.33, limit 10/3 = 3.33
  - Tenant-C: CPU request 4/3 = 1.33, limit 10/3 = 3.33

Conservative values per tenant:
  - requests.cpu: 4 (reserved, aggregate over all possible tenants)
  - limits.cpu: 10 (burst, allows overshoot if other tenants not using)
  - Max pods: 100 (prevents pod explosion)
```

### LimitRange: Container Resource Defaults

LimitRanges set default resource requests/limits for containers without explicit specs.

**Per-Container Defaults:**

```yaml
# tenant-limits (LimitRange in each tenant namespace)

Default (applied if not specified in Pod):
  cpu: "100m"               # 1/10 of a core - typical microservice baseline
  memory: "128Mi"           # Minimal memory for Go processes

Limit (maximum if specified in Pod):
  cpu: "2"                  # Pod cannot request more than 2 cores
  memory: "4Gi"             # Pod cannot request more than 4 Gi

Min (minimum if specified in Pod):
  cpu: "50m"                # Pod must request at least 50m
  memory: "64Mi"            # Pod should request at least 64Mi

Pod-level Limits:
  max cpu: "4"              # Single pod cannot use more than 4 cores
  max memory: "8Gi"         # Single pod cannot use more than 8 Gi
```

**Why These Limits?**

```
Without LimitRange:
  Developer submits pod with no resource requests
  → Pod gets 0 request (no guaranteed capacity)
  → Pod gets unlimited memory (could crash node)
  → Pod squeezed on to any node that has physical space
  → Leads to unpredictable behavior

With LimitRange:
  Developer submits pod with no resource requests
  → Pod automatically gets 100m CPU, 128Mi memory default
  → Pod cannot exceed 2 cores or 4Gi memory (hardware protection)
  → Scheduler places pod on node with guaranteed 100m/128Mi available
  → Predictable, bounded behavior across cluster
```

### Adjusting Limits Per Tenant

For a tenant that needs more resources (e.g., machine learning workloads):

```bash
# 1. Edit the ResourceQuota
kubectl edit resourcequota tenant-quota -n tenant-ml

# Update limits:
# requests.cpu: "20"            # increase from 4
# limits.cpu: "50"              # increase from 10
# requests.memory: "40Gi"       # increase from 8Gi
# limits.memory: "100Gi"        # increase from 20Gi

# 2. Edit the LimitRange for container defaults
kubectl edit limitrange tenant-limits -n tenant-ml

# Update defaults:
# default (cpu): "2"            # increase from 100m for mutable defaults
# default (memory): "2Gi"       # increase from 128Mi

# 3. Verify changes
kubectl describe resourcequota -n tenant-ml
kubectl describe limitrange -n tenant-ml

# 4. Check current usage doesn't exceed new limits
kubectl top pods -n tenant-ml --containers
```

---

## 6. Tenant Onboarding Process

### Step 1: Provision Tenant Resources

Use the automated provisioning script:

```bash
# Create tenant namespace, RBAC, quotas, network policies
./scripts/create-tenant.sh acme-prod

# Expected output:
# ✅ Namespace 'tenant-acme-prod' created
# ✅ ResourceQuota applied
# ✅ LimitRange applied
# ✅ ServiceAccount 'tenant-acme-prod' created
# ✅ Role 'tenant-role' created
# ✅ RoleBinding 'tenant-rolebinding' created
# ✅ NetworkPolicies applied

# Time: ~5-10 seconds
```

### Step 2: Verify Provisioning Success

Run the validation suite:

```bash
# Validate all RBAC and isolation controls
./scripts/validate-rbac.sh

# Expected result:
# ✅ RBAC & MULTI-TENANCY: FULLY OPERATIONAL
# Passed: 14/14 tests
```

### Step 3: Create Tenant Credentials Document

Generate API credentials for the tenant team:

```bash
#!/bin/bash
TENANT_NAME="acme-prod"
TENANT_NS="tenant-${TENANT_NAME}"

# Get the ServiceAccount token
TOKEN=$(kubectl get secret -n "${TENANT_NS}" \
  $(kubectl get secret -n "${TENANT_NS}" -o name | grep "${TENANT_NAME}-token") \
  -o jsonpath='{.data.token}' | base64 -d)

# Get the API server certificate
CA_CERT=$(kubectl get secret -n "${TENANT_NS}" \
  $(kubectl get secret -n "${TENANT_NS}" -o name | grep "${TENANT_NAME}-token") \
  -o jsonpath='{.data.ca\.crt}' | base64 -d)

# Output credentials
cat > credentials-${TENANT_NAME}.yaml <<EOF
# CloudTask Orchestrator - Tenant API Credentials
# Tenant: ${TENANT_NAME}
# Generated: $(date)

ServiceAccount:
  Name: tenant-${TENANT_NAME}
  Namespace: ${TENANT_NS}
  
API Server:
  URL: $(kubectl cluster-info | grep 'Kubernetes master' | awk '{print $NF}')
  
Authentication:
  Token: ${TOKEN}
  Certificate Authority: |
    ${CA_CERT}

Usage Example:
  # Set context
  kubectl config set-context tenant-${TENANT_NAME} \
    --cluster=kind-orchestrator \
    --user=tenant-${TENANT_NAME}
  
  # Set credentials
  kubectl config set-credentials tenant-${TENANT_NAME} \
    --token=${TOKEN}
  
  # Switch to tenant context
  kubectl config use-context tenant-${TENANT_NAME}
  
  # Test access
  kubectl get cloudtasks -n ${TENANT_NS}
EOF

echo "Credentials saved to credentials-${TENANT_NAME}.yaml"
```

### Step 4: Onboarding Documentation to Tenant

Send to tenant team:

```markdown
# Onboarding: CloudTask Orchestrator

## Your Tenant Environment

- **Namespace**: tenant-acme-prod
- **ServiceAccount**: tenant-acme-prod
- **Region**: Default
- **Support Team**: platform-engineering@company.com

## Access

1. Configure kubectl:
   ```
   kubectl config set-context tenant-acme-prod --cluster=kind-orchestrator --user=tenant-acme-prod
   kubectl config set-credentials tenant-acme-prod --token=<YOUR_TOKEN_HERE>
   kubectl config use-context tenant-acme-prod
   ```

2. Test access:
   ```
   kubectl get cloudtasks
   kubectl get namespaces  # Should only show tenant-acme-prod
   ```

## Resource Limits

- CPU requests: 4 cores (guaranteed)
- CPU limits: 10 cores (burst maximum)
- Memory requests: 8 Gi (guaranteed)
- Memory limits: 20 Gi (burst maximum)
- Max pods: 100

## Creating CloudTasks

```yaml
apiVersion: tasks.orchestrator.dev/v1
kind: CloudTask
metadata:
  name: my-task
  namespace: tenant-acme-prod
spec:
  image: my-registry/my-image:latest
  command: ["/bin/sh", "-c"]
  args: ["python script.py"]
  tenantID: acme-prod
  priority: 50
```

## Support

Contact platform-engineering@company.com for:
- Resource limit increases
- Network policy modifications
- CloudTask implementation questions
```

---

## 7. Tenant Offboarding Process

### Step 1: Backup Tenant Data

Before deletion, backup all tenant resources:

```bash
TENANT_NAME="acme-prod"
TENANT_NS="tenant-${TENANT_NAME}"
BACKUP_DIR="backups/${TENANT_NAME}-$(date +%Y%m%d-%H%M%S)"

mkdir -p "${BACKUP_DIR}"

# Backup all resources
kubectl get all -n "${TENANT_NS}" -o yaml > "${BACKUP_DIR}/all-resources.yaml"

# Backup CloudTasks
kubectl get cloudtasks.tasks.orchestrator.dev -n "${TENANT_NS}" -o yaml > "${BACKUP_DIR}/cloudtasks.yaml"

# Backup ConfigMaps and Secrets
kubectl get configmaps -n "${TENANT_NS}" -o yaml > "${BACKUP_DIR}/configmaps.yaml"
kubectl get secrets -n "${TENANT_NS}" -o yaml > "${BACKUP_DIR}/secrets.yaml"

# Backup PersistentVolumeClaims
kubectl get pvc -n "${TENANT_NS}" -o yaml > "${BACKUP_DIR}/pvcs.yaml"

# Archive backup
tar -czf "${BACKUP_DIR}.tar.gz" "${BACKUP_DIR}"

echo "Backup complete: ${BACKUP_DIR}.tar.gz"
```

### Step 2: Notify Tenant Team

Send notification 30 days in advance, then weekly reminders.

### Step 3: Clean Deletion

Use the automated deletion script:

```bash
# List tenants before deletion
./scripts/list-tenants.sh

# Delete tenant (requires explicit "yes" confirmation)
./scripts/delete-tenant.sh acme-prod

# At prompt, type: yes

# Expected output:
# ✅ Namespace fully deleted
# Tenant 'acme-prod' has been completely removed
```

### Step 4: Verify Deletion

```bash
# Confirm tenant is gone
./scripts/list-tenants.sh
# Should not show acme-prod

# Confirm backup exists
ls -lh backups/acme-prod*.tar.gz

# Confirm namespace is gone
kubectl get namespace tenant-acme-prod
# Should return: Error from server (NotFound): namespaces "tenant-acme-prod" not found
```

### Step 5: Data Retention

Follow organizational policy for data retention:

```yaml
Recommended Policy:
  - Development/Test tenants: Delete immediately + 7-day backup retention
  - Production tenants: Delete + 30-day backup retention
  - Sensitive data: Shred backups, confirm deletion from backups service
```

---

## 8. RBAC Validation Procedures

### Running the Validation Suite

The validation suite performs 14 automated tests across 5 test categories:

```bash
# Make script executable
chmod +x scripts/validate-rbac.sh

# Run validation
./scripts/validate-rbac.sh

# Output: ~5-15 seconds depending on network policies
```

### Test Breakdown

#### Test 1: Tenant Namespace Isolation (3 checks)

Tests that RBAC properly scopes tenant access to their own namespace.

**1a: tenant-a CANNOT create CloudTasks in tenant-b**
- Command: `kubectl auth can-i create cloudtasks --as=tenant-a-sa -n tenant-b`
- Expected: DENIED (permission denied)
- Checks: Cross-namespace RBAC boundary

**1b: tenant-a CAN create CloudTasks in tenant-a**
- Command: `kubectl auth can-i create cloudtasks --as=tenant-a-sa -n tenant-a`
- Expected: ALLOWED
- Checks: Permission within own namespace

**1c: tenant-b CANNOT create CloudTasks in tenant-a**
- Command: `kubectl auth can-i create cloudtasks --as=tenant-b-sa -n tenant-a`
- Expected: DENIED
- Checks: Cross-namespace RBAC boundary (reverse)

#### Test 2: Pod Access Restrictions (3 checks)

Tests that RBAC prevents tenants from creating pods directly.

**2a: tenant-a CANNOT create Pods**
- Purpose: Operators only (tenants create via CloudTasks)
- Expected: DENIED

**2b: tenant-a CAN list Pods in own namespace**
- Purpose: Tenant observability (debugging)
- Expected: ALLOWED

**2c: tenant-a CANNOT list Pods in tenant-b**
- Purpose: Information isolation
- Expected: DENIED

#### Test 3: Resource Quota Enforcement (3 checks)

Tests that ResourceQuotas and LimitRanges are properly applied.

**3a: ResourceQuota exists**
- Checks: ResourceQuota object present in namespace
- Shows: Current CPU/Memory usage, hard limits

**3b: LimitRange exists**
- Checks: LimitRange object present in namespace
- Shows: Default container resource specs

**3c: Current quota usage**
- Displays: Utilization percentages
- Useful for: Capacity planning

#### Test 4: Network Policy Isolation (3 checks)

Tests that network policies enforce traffic isolation.

**4a: Deploy test pods (infrastructure)**
- Creates: test-pod-a in tenant-a, test-pod-b in tenant-b
- Uses: curlimages/curl for testing connectivity

**4b: tenant-a pod CANNOT reach tenant-b pod**
- Method: curl http://{pod-b-ip}:8080 from pod-a
- Expected: timeout/connection refused
- Validates: NetworkPolicy pod isolation working

**4c: tenant-a pod CAN reach external internet**
- Method: curl https://www.google.com from pod-a
- Expected: HTTP 200/302 response
- Validates: External egress allowed, internal isolation intact

#### Test 5: Operator Access Verification (4 checks)

Tests that operator has proper cluster-wide permissions.

**5a: Operator CAN list CloudTasks in tenant-a**
- Expected: ALLOWED (operator needs visibility)

**5b: Operator CAN list CloudTasks in tenant-b**
- Expected: ALLOWED (operator orchestrates all)

**5c: Operator CAN create Pods in tenant-a**
- Expected: ALLOWED (operator executes CloudTasks via pods)

**5d: Operator CAN delete CloudTasks in tenant-a**
- Expected: ALLOWED (operator manages lifecycle)

### Interpreting Results

**All Passed (14/14):**
```
✅ RBAC & MULTI-TENANCY: FULLY OPERATIONAL
```
- System ready for production
- All isolation controls verified
- All permissions correctly scoped

**Some Failed:**
```
❌ FAILED TESTS:
  • 1a: tenant-a CAN create CloudTasks in tenant-b
  • 4b: tenant-a pod CAN reach tenant-b pod
```

Common causes:
- RBAC RoleBinding not applied correctly
- NetworkPolicy not applied to namespace
- ServiceAccount not mounted correctly

### Troubleshooting Failed Tests

```bash
# For RBAC failures (Test 1, 2, 5):
kubectl describe rolebinding -n tenant-a
kubectl describe role -n tenant-a

# For NetworkPolicy failures (Test 4):
kubectl get networkpolicies -n tenant-a
kubectl describe networkpolicies -n tenant-a

# For Resource Management (Test 3):
kubectl describe resourcequota -n tenant-a
kubectl describe limitrange -n tenant-a

# Check ServiceAccount:
kubectl get serviceaccount -n tenant-a
kubectl get secret -n tenant-a | grep token
```

---

## 9. Troubleshooting Guide

### Problem 1: Pod Cannot Communicate with Operator

**Symptoms:**
- Pods in tenant namespace cannot reach orchestrator-system
- Operator cannot exec into pods
- Pod logs don't contain expected operator log lines

**Diagnosis:**
```bash
# Check if pod can reach operator namespace
kubectl run -it test-pod --image=alpine -n tenant-a -- sh
# Inside pod:
nslookup orchestrator-system.svc.cluster.local
ping orchestrator-system.default.svc.cluster.local  # Should fail

# Check NetworkPolicies
kubectl get networkpolicies -n tenant-a
```

**Solutions:**

1. **Verify allow-control-plane NetworkPolicy exists:**
   ```bash
   kubectl get networkpolicies -n tenant-a -o name | grep control-plane
   
   # If missing, apply it:
   kubectl apply -f config/network-policy/allow-control-plane.yaml -n tenant-a
   ```

2. **Check NetworkPolicy syntax:**
   ```yaml
   # Should have ingress from orchestrator-system
   ingress:
   - from:
     - namespaceSelector:
         matchLabels:
           name: orchestrator-system
     ports:
     - protocol: TCP
       port: 6443
   ```

3. **Verify pods have labels matching NetworkPolicy selectors:**
   ```bash
   kubectl get pods -n tenant-a --show-labels
   
   # Should show labels like:
   # orchestrator.dev/workload=cloudtask
   # app.kubernetes.io/name=cloudtask
   ```

---

### Problem 2: Tenant Can Access Another Tenant's Namespace

**Symptoms:**
- Tenant-a can list CloudTasks in tenant-b
- Tenant-a can read secrets from tenant-b
- RBAC validation fails on Test 1

**Diagnosis:**
```bash
# Check tenant-a RoleBinding
kubectl describe rolebinding tenant-rolebinding -n tenant-a

# Should show scope limited to tenant-a namespace
# Check if ClusterRoleBinding accidentally exists
kubectl get clusterrolebindings | grep tenant
```

**Solutions:**

1. **Verify RoleBinding (not ClusterRoleBinding) is used:**
   ```bash
   # Should only see Role and RoleBinding (namespaced)
   kubectl get roles -n tenant-a
   kubectl get rolebindings -n tenant-a
   
   # Should NOT see ClusterRoleBindings for tenants
   kubectl get clusterrolebindings | grep -v operator | grep -v admin
   ```

2. **Check Role scope - must use Role, not ClusterRole:**
   ```yaml
   # CORRECT: Role (namespaced)
   kind: Role
   metadata:
     name: tenant-role
     namespace: tenant-a
   
   # WRONG: ClusterRole for tenant
   kind: ClusterRole
   metadata:
     name: tenant-a-role  # This would have cluster-wide access!
   ```

3. **Recreate RBAC if wrong:**
   ```bash
   # Delete incorrect RBAC
   kubectl delete clusterrolebinding tenant-a-access
   
   # Reapply correct RBAC
   ./scripts/create-tenant.sh tenant-a
   ```

---

### Problem 3: Pod Creation Fails with Quota Exceeded

**Symptoms:**
```
Error: pods "pod-name" is forbidden: exceeded quota: tenant-quota
```

**Diagnosis:**
```bash
# Check current quota usage
kubectl describe resourcequota -n tenant-a

# Example output:
# Name:       tenant-quota
# Namespace:  tenant-a
# Resource    Used  Hard
# --------    ----  ----
# cpu         4     4
# memory      8Gi   8Gi
# pods        100   100
```

**Solutions:**

1. **Identify resource hog:**
   ```bash
   kubectl top pods -n tenant-a --containers | sort -k2 -rn
   ```

2. **Delete unnecessary pods:**
   ```bash
   kubectl delete pod pod-name -n tenant-a
   ```

3. **If legitimate need, increase quota temporarily:**
   ```bash
   # Edit quota
   kubectl patch resourcequota tenant-quota -n tenant-a --type merge \
     -p '{"spec":{"hard":{"cpu":"10","memory":"16Gi"}}}'
   
   # Verify
   kubectl describe resourcequota -n tenant-a
   ```

4. **Check for resource leaks:**
   ```bash
   # Find pods in Terminating state (stuck)
   kubectl get pods -n tenant-a | grep Terminating
   
   # Force delete if stuck
   kubectl delete pod pod-name -n tenant-a --grace-period=0 --force
   ```

---

### Problem 4: Network Policy Not Blocking Traffic

**Symptoms:**
- Pod in tenant-a can curl pod in tenant-b
- External traffic is blocked but shouldn't be
- Test 4b fails

**Diagnosis:**
```bash
# Verify CNI supports NetworkPolicies
kubectl get nodes -o wide
# Should show CNI: kindnet (which has calico plugin for NP support)

# Check NetworkPolicy was applied
kubectl get networkpolicies -n tenant-a

# Test policy evaluation
kubectl run -it test-pod-src -n tenant-a --image=alpine -- sh
# In pod:
nslookup test-pod -n tenant-b  # Should fail

# Check if policies have selectors
kubectl get networkpolicies -n tenant-a -o yaml | grep -A5 selector
```

**Solutions:**

1. **Verify kindnet CNI has NetworkPolicy support:**
   ```bash
   # Kind clusters include Calico CNI plugin for NetworkPolicy
   kubectl get cni -n kube-system  # May not exist (embedded in kindnet)
   
   # Check kindnet DaemonSet
   kubectl get daemonset -n kube-system | grep kindnet
   ```

2. **Check NetworkPolicy syntax:**
   ```yaml
   # CORRECT: Default deny + explicit allow
   apiVersion: networking.k8s.io/v1
   kind: NetworkPolicy
   metadata:
     name: deny-all-ingress
   spec:
     podSelector: {}
     policyTypes:
     - Ingress
     ingress: []  # Empty = allow nothing
   
   ---
   apiVersion: networking.k8s.io/v1
   kind: NetworkPolicy
   metadata:
     name: allow-internal
   spec:
     podSelector: {}
     policyTypes:
     - Ingress
     ingress:
     - from:
       - namespaceSelector:
           matchLabels:
             name: tenant-a
   ```

3. **Debug policy matching:**
   ```bash
   # Check pod labels match policy selector
   kubectl get pods -n tenant-a --show-labels
   
   # Policy selectors must match pod labels
   # Example: 
   #   Pod labels: managed-by=cloudtask-operator
   #   Selector: matchLabels: managed-by: cloudtask-operator
   ```

4. **Re-apply policies if syntax wrong:**
   ```bash
   # Remove incorrect policies
   kubectl delete networkpolicy --all -n tenant-a
   
   # Reapply correct versions
   kubectl apply -f config/network-policy/ -n tenant-a
   
   # Validate
   ./scripts/validate-rbac.sh  # Test 4b should pass now
   ```

---

### Problem 5: ServiceAccount Token Not Mounted in Pod

**Symptoms:**
- Pod cannot access Kubernetes API
- `/var/run/secrets/kubernetes.io/serviceaccount/token` doesn't exist
- "Unauthorized" errors when pod tries API calls

**Diagnosis:**
```bash
# Check if automountServiceAccount is enabled
kubectl get pods -n tenant-a -o yaml | grep -A5 automountServiceAccountToken

# Should show: automountServiceAccountToken: true

# Check secret was created
kubectl get secret -n tenant-a | grep service-account-token
```

**Solutions:**

1. **Verify ServiceAccount exists:**
   ```bash
   kubectl get serviceaccount -n tenant-a
   kubectl describe serviceaccount tenant-a -n tenant-a
   ```

2. **Rebuild ServiceAccount if missing token:**
   ```bash
   kubectl delete serviceaccount tenant-a -n tenant-a
   
   # Reapply from template
   kubectl apply -f config/rbac/serviceaccount.yaml -n tenant-a
   ```

3. **Ensure pod mounts token:**
   ```yaml
   spec:
     serviceAccountName: tenant-a
     automountServiceAccountToken: true  # Explicit
     containers:
     - name: app
       image: myapp:latest
   ```

---

### Problem 6: Operator Cannot Find Tenant Namespace

**Symptoms:**
- Operator logs show "namespace not found"
- CloudTasks in tenant namespace are not processed
- Operator doesn't create pods for CloudTasks

**Diagnosis:**
```bash
# Check operator can list namespaces
kubectl auth can-i list namespaces \
  --as=system:serviceaccount:orchestrator-system:orchestrator-controller-manager

# Check operator ClusterRole has namespace permissions
kubectl describe clusterrole cloudtask-operator-role | grep -A10 "namespaces"

# Check if namespace has required labels
kubectl get namespace tenant-a --show-labels
# Should have: managed-by=cloudtask-operator
```

**Solutions:**

1. **Add required label if missing:**
   ```bash
   kubectl label namespace tenant-a managed-by=cloudtask-operator
   ```

2. **Verify operator ClusterRole has permission:**
   ```bash
   kubectl describe clusterrole cloudtask-operator-role | grep -E "verbs|get|list"
   
   # Should include:
   # - get (individual namespace)
   # - list (all namespaces)
   ```

3. **Restart operator to pick up changes:**
   ```bash
   kubectl rollout restart deployment -n orchestrator-system
   ```

---

### Problem 7: Tenant Cannot Create CloudTasks

**Symptoms:**
```
Error: cloudtasks.tasks.orchestrator.dev "task-1" is forbidden: User cannot create resource "cloudtasks"
```

**Diagnosis:**
```bash
# Check if tenant can create CloudTasks per RBAC
kubect auth can-i create cloudtasks.tasks.orchestrator.dev \
  --as=system:serviceaccount:tenant-a:tenant-a \
  -n tenant-a

# Check Role has CloudTask permissions
kubectl describe role tenant-role -n tenant-a | grep -A10 cloudtasks
```

**Solutions:**

1. **Verify Role includes CloudTasks:**
   ```yaml
   # Should have:
   - apiGroups:
     - tasks.orchestrator.dev
     resources:
     - cloudtasks
     verbs:
     - create
     - get
     - list
   ```

2. **Re-apply Role if missing CloudTasks:**
   ```bash
   kubectl apply -f config/rbac/tenant-role.yaml -n tenant-a
   ```

3. **Wait for API cache refresh:**
   ```bash
   sleep 30  # RBAC changes can take time to propagate
   
   # Then retry CloudTask creation
   kubectl apply -f config/samples/cloudtask_v1_sample.yaml -n tenant-a
   ```

---

### Problem 8: Resource Limits Preventing Pod Launch

**Symptoms:**
```
Error: pods exceed maxAllowed cpu limit
```

**Diagnosis:**
```bash
# Check LimitRange bounds
kubectl describe limitrange -n tenant-a | grep -A20 "Container"

# Check specific pod requests
kubectl get pod pod-name -n tenant-a -o yaml | grep -A10 resources
```

**Solutions:**

1. **Increase LimitRange if legitimate:**
   ```bash
   kubectl patch limitrange tenant-limits -n tenant-a --type merge \
     -p '{"limits":[{"type":"Container","max":{"cpu":"4","memory":"8Gi"}}]}'
   ```

2. **Reduce pod resource requests if excessive:**
   ```bash
   # Edit CloudTask spec to use less resources
   kubectl edit cloudtask task-name -n tenant-a
   
   # Reduce values under spec.resources
   ```

---

## 10. Quick Reference

### Kubectl Commands by Task

#### List Operations

```bash
# List all tenant namespaces
kubectl get namespaces -l managed-by=cloudtask-operator

# List specific tenant resources
kubectl get cloudtasks -n tenant-a
kubectl get pods -n tenant-a
kubectl get events -n tenant-a -w  # Watch events realtime

# List all resources
kubectl get all -n tenant-a -o wide
```

#### View Operations

```bash
# Describe namespace (labels, quotas, policies)
kubectl describe namespace tenant-a

# View ServiceAccount token
kubectl get secret -n tenant-a $(kubectl get secret -n tenant-a -o name | grep token) \
  -o jsonpath='{.data.token}' | base64 -d

# View quotaused
kubectl describe resourcequota -n tenant-a

# View limits
kubectl describe limitrange -n tenant-a
```

#### RBAC Operations

```bash
# Check if user/SA can perform action
kubectl auth can-i create cloudtasks --as=system:serviceaccount:tenant-a:tenant-a -n tenant-a

# View all roles in namespace
kubectl get roles -n tenant-a -o yaml

# View all rolebindings
kubectl get rolebindings -n tenant-a -o yaml

# View specific rolebinding
kubectl describe rolebinding tenant-rolebinding -n tenant-a
```

#### Network Policy Operations

```bash
# List policies
kubectl get networkpolicies -n tenant-a

# View policy details
kubectl describe networkpolicies -n tenant-a

# Verify policy syntax
kubectl apply -f config/network-policy/deny-all-ingress.yaml --dry-run=client -n tenant-a
```

#### CloudTask Operations

```bash
# Create CloudTask
kubectl apply -f cloudtask.yaml -n tenant-a

# List CloudTasks
kubectl get cloudtasks -n tenant-a

# Watch CloudTasks
kubectl get cloudtasks -n tenant-a --watch

# Describe CloudTask
kubectl describe cloudtask task-1 -n tenant-a

# View CloudTask logs
kubectl log cloudtask/task-1 -n tenant-a

# Delete CloudTask
kubectl delete cloudtask task-1 -n tenant-a
```

#### Pod Operations (within tenant)

```bash
# List pods
kubectl get pods -n tenant-a

# View pod details
kubectl describe pod pod-name -n tenant-a

# View pod logs
kubectl logs pod-name -n tenant-a

# Execute command in pod
kubectl exec -it pod-name -n tenant-a -- bash

# Copy files from pod
kubectl cp tenant-a/pod-name:/path/to/file ./local-file
```

#### Tenant Lifecycle

```bash
# Create tenant
./scripts/create-tenant.sh tenant-name

# List tenants
./scripts/list-tenants.sh

# Delete tenant
./scripts/delete-tenant.sh tenant-name

# Validate RBAC
./scripts/validate-rbac.sh
```

### Label Selector Reference

```bash
# All managed tenant namespaces
-l managed-by=cloudtask-operator

# All production tenants
-l managed-by=cloudtask-operator,environment=production

# Exclude staging
-l managed-by=cloudtask-operator,environment!=staging

# Specific tenant
-l tenant-id=acme-prod
```

### Common CloudTask Examples

```yaml
# Simple Alpine task
apiVersion: tasks.orchestrator.dev/v1
kind: CloudTask
metadata:
  name: hello-world
  namespace: tenant-a
spec:
  image: alpine:latest
  command: ["/bin/sh", "-c"]
  args: ["echo Hello"]
  tenantID: a
  priority: 50

---
# Python script task
apiVersion: tasks.orchestrator.dev/v1
kind: CloudTask
metadata:
  name: python-task
  namespace: tenant-a
spec:
  image: python:3.11-slim
  command: ["python"]
  args: ["-c", "print('Python works')"]
  tenantID: a
  priority: 75
  resources:
    requests:
      cpu: 200m
      memory: 256Mi

---
# Task with environment variables
apiVersion: tasks.orchestrator.dev/v1
kind: CloudTask
metadata:
  name: env-task
  namespace: tenant-a
spec:
  image: busybox:latest
  command: ["sh", "-c"]
  args: ["echo $MY_VAR"]
  env:
  - name: MY_VAR
    value: "tenant-a-value"
  tenantID: a
  priority: 50
```

---

## Version History

| Version | Date | Changes |
|---------|------|---------|
| 1.0 | Apr 19, 2026 | Initial release: Complete RBAC & Multi-Tenancy documentation |

## Glossary

| Term | Definition |
|------|-----------|
| **RBAC** | Role-Based Access Control - Kubernetes authorization system |
| **ClusterRole** | Permissions scoped to cluster-wide (all namespaces) |
| **Role** | Permissions scoped to single namespace |
| **RoleBinding** | Links Role to ServiceAccount (grants permissions) |
| **ServiceAccount** | Identity for pods and users in Kubernetes |
| **NetworkPolicy** | Rules controlling pod-to-pod network traffic |
| **ResourceQuota** | Limits on total resource consumption in namespace |
| **LimitRange** | Default container resource requests/limits in namespace |
| **Tenant** | Single customer/application using the cluster |
| **Namespace** | Kubernetes logical cluster isolation boundary |

---

**Document Status**: ✅ Production-Ready  
**Last Updated**: April 19, 2026  
**Maintained By**: Platform Engineering Team
