#!/bin/bash

# setup-monitoring.sh - Setup monitoring stack (Prometheus, AlertManager)

set -e

NAMESPACE="monitoring"
CONFIG_DIR="${1:-config}"

echo "📊 Setting up monitoring stack"

# Check kubectl
if ! command -v kubectl &> /dev/null; then
    echo "❌ kubectl not installed. Please install kubectl."
    exit 1
fi

# Create monitoring namespace
echo "Creating namespace: $NAMESPACE"
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

# Create Prometheus RBAC
echo "Creating Prometheus ServiceAccount and RBAC..."
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ServiceAccount
metadata:
  name: prometheus
  namespace: $NAMESPACE

---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: prometheus
rules:
- apiGroups: [""]
  resources:
  - nodes
  - nodes/proxy
  - services
  - endpoints
  - pods
  verbs: ["get", "list", "watch"]
- apiGroups:
  - extensions
  resources:
  - ingresses
  verbs: ["get", "list", "watch"]

---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: prometheus
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: prometheus
subjects:
- kind: ServiceAccount
  name: prometheus
  namespace: $NAMESPACE
EOF

# Apply Prometheus deployment
echo "Deploying Prometheus..."
kubectl apply -f "$CONFIG_DIR/monitoring/prometheus.yaml"

# Wait for Prometheus to be ready
echo "Waiting for Prometheus to be ready..."
kubectl wait --for=condition=Ready pod -l app=prometheus -n "$NAMESPACE" --timeout=300s || {
    echo "⚠️ Prometheus not ready, checking logs:"
    kubectl logs -l app=prometheus -n "$NAMESPACE" --tail=50
}

# Create port-forward service
echo "Prometheus is available at: http://localhost:9090"
echo ""
echo "To access Prometheus:"
echo "  kubectl port-forward -n $NAMESPACE svc/prometheus 9090:9090"

echo "✅ Monitoring stack setup complete!"
