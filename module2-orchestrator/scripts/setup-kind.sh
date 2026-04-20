#!/bin/bash

# setup-kind.sh - Create and configure a Kind cluster for CloudTask development

set -e

CLUSTER_NAME="${1:-cloudtask-cluster}"
CONFIG_FILE="${2:-config/kind/cluster.yaml}"
KUBE_VERSION="${3:-v1.28.0}"

echo "🚀 Setting up Kind cluster: $CLUSTER_NAME"

# Check if kind is installed
if ! command -v kind &> /dev/null; then
    echo "Installing kind..."
    go install sigs.k8s.io/kind@latest
fi

# Check if kubectl is installed
if ! command -v kubectl &> /dev/null; then
    echo "Installing kubectl..."
    if [ "$(uname)" == "Darwin" ]; then
        brew install kubectl
    else
        curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
        chmod +x kubectl
        sudo mv kubectl /usr/local/bin/
    fi
fi

# Delete existing cluster if it exists
if kind get clusters | grep -q "$CLUSTER_NAME"; then
    echo "Deleting existing cluster: $CLUSTER_NAME"
    kind delete cluster --name="$CLUSTER_NAME"
fi

# Create cluster
echo "Creating Kind cluster with config: $CONFIG_FILE"
kind create cluster --name="$CLUSTER_NAME" --config="$CONFIG_FILE" --image="kindest/node:$KUBE_VERSION"

# Wait for cluster to be ready
echo "Waiting for cluster to be ready..."
kubectl cluster-info
kubectl wait --for=condition=Ready node --all --timeout=300s

# Install CNI if needed
echo "Checking CNI..."
kubectl get daemonset -n kube-system

# Install NGINX Ingress Controller
echo "Installing NGINX Ingress Controller..."
kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/main/deploy/static/provider/kind/deploy.yaml
kubectl wait --namespace ingress-nginx --for=condition=ready pod --selector=app.kubernetes.io/component=controller --timeout=120s

# Create namespaces
echo "Creating namespaces..."
kubectl create namespace orchestrator-system --dry-run=client -o yaml | kubectl apply -f -
kubectl create namespace monitoring --dry-run=client -o yaml | kubectl apply -f -

echo "✅ Kind cluster setup complete!"
echo "Cluster name: $CLUSTER_NAME"
echo "To use this cluster, run: export KUBECONFIG=$(kind get kubeconfig-path --name=$CLUSTER_NAME)"
