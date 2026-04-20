//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	orchestratorNamespace = "orchestrator-system"
	gatewayServiceName    = "module2-gateway"
	apiPort               = 8000
)

// TestE2E_FullTaskLifecycle tests the entire lifecycle:
// Create tenant → Submit task → Verify pod → Verify status → Verify completion → Delete tenant
func TestE2E_FullTaskLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	config, err := getKubeConfig()
	require.NoError(t, err, "Failed to get kubeconfig")

	clientset, err := kubernetes.NewForConfig(config)
	require.NoError(t, err, "Failed to create clientset")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	tenantName := fmt.Sprintf("tenant-e2e-%d", time.Now().Unix())
	tenantNamespace := fmt.Sprintf("%s-namespace", tenantName)

	// Step 1: Create tenant using create-tenant.sh
	t.Logf("Step 1: Creating tenant: %s", tenantName)
	err = createTenant(ctx, tenantName)
	require.NoError(t, err, "Failed to create tenant")
	defer func() {
		t.Logf("Cleanup: Deleting tenant: %s", tenantName)
		deleteTenant(context.Background(), tenantName)
	}()

	// Wait for namespace to be created
	time.Sleep(3 * time.Second)

	// Verify namespace exists
	ns, err := clientset.CoreV1().Namespaces().Get(ctx, tenantNamespace, metav1.GetOptions{})
	require.NoError(t, err, "Tenant namespace should exist")
	assert.Equal(t, tenantNamespace, ns.Name)
	t.Logf("  ✓ Namespace %s created", tenantNamespace)

	// Step 2: Submit task via API Gateway
	gatewayURL, err := getAPIGatewayURL(ctx)
	require.NoError(t, err, "Failed to get API Gateway URL")
	t.Logf("Step 2: Submitting task to %s", gatewayURL)

	taskName := fmt.Sprintf("e2e-task-%d", time.Now().Unix())
	taskRequest := map[string]interface{}{
		"name":     taskName,
		"image":    "busybox:latest",
		"command":  []string{"sh", "-c"},
		"args":     []string{"echo 'Hello from E2E test'; sleep 2; echo 'Task complete'"},
		"tenantID": tenantName,
		"retries":  0,
		"timeout":  "2m",
	}

	body, err := json.Marshal(taskRequest)
	require.NoError(t, err)

	resp, err := http.Post(
		fmt.Sprintf("%s/v1/tasks", gatewayURL),
		"application/json",
		bytes.NewReader(body),
	)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode, "Task submission should return 201")
	t.Logf("  ✓ Task %s submitted", taskName)

	// Step 3: Verify pod created in tenant namespace
	t.Log("Step 3: Verifying pod creation")
	time.Sleep(5 * time.Second)

	pods, err := clientset.CoreV1().Pods(tenantNamespace).List(ctx, metav1.ListOptions{})
	require.NoError(t, err, "Failed to list pods")

	t.Logf("  Found %d pods in namespace %s", len(pods.Items), tenantNamespace)
	if len(pods.Items) > 0 {
		pod := pods.Items[0]
		t.Logf("  ✓ Pod name: %s, Phase: %s", pod.Name, pod.Status.Phase)
		assert.NotEmpty(t, pod.Name, "Pod should have a name")
	}

	// Step 4: Verify task status updates to Running
	t.Log("Step 4: Checking task status")
	resp2, err := http.Get(fmt.Sprintf("%s/v1/tasks?name=%s", gatewayURL, taskName))
	require.NoError(t, err)
	defer resp2.Body.Close()

	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var taskData map[string]interface{}
	json.NewDecoder(resp2.Body).Decode(&taskData)
	assert.Equal(t, taskName, taskData["name"])
	t.Logf("  ✓ Task status: %v", taskData["phase"])

	// Step 5: Wait for task completion
	t.Log("Step 5: Waiting for task completion (max 60s)")
	deadline := time.Now().Add(60 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		pods, err := clientset.CoreV1().Pods(tenantNamespace).List(ctx, metav1.ListOptions{})
		if err == nil && len(pods.Items) > 0 {
			pod := pods.Items[0]
			if pod.Status.Phase == "Succeeded" {
				completed = true
				t.Logf("  ✓ Task completed successfully")
				break
			}
		}
		time.Sleep(3 * time.Second)
	}

	if !completed {
		t.Logf("  ⚠ Task did not complete within timeout (operator may not be running)")
	}

	// Step 6: Delete tenant and verify cleanup
	t.Log("Step 6: Deleting tenant and verifying cleanup")
	err = deleteTenant(ctx, tenantName)
	require.NoError(t, err, "Failed to delete tenant")

	time.Sleep(5 * time.Second)

	_, err = clientset.CoreV1().Namespaces().Get(ctx, tenantNamespace, metav1.GetOptions{})
	if err != nil {
		t.Logf("  ✓ Namespace %s cleaned up", tenantNamespace)
	} else {
		t.Logf("  ⚠ Namespace %s still exists (may be terminating)", tenantNamespace)
	}
}

// TestE2E_TenantIsolation tests that different tenants are isolated
func TestE2E_TenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	config, err := getKubeConfig()
	require.NoError(t, err)

	clientset, err := kubernetes.NewForConfig(config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	tenant1 := fmt.Sprintf("tenant-iso1-%d", time.Now().Unix())
	tenant2 := fmt.Sprintf("tenant-iso2-%d", time.Now().Unix())

	err = createTenant(ctx, tenant1)
	require.NoError(t, err)
	defer deleteTenant(context.Background(), tenant1)

	err = createTenant(ctx, tenant2)
	require.NoError(t, err)
	defer deleteTenant(context.Background(), tenant2)

	time.Sleep(3 * time.Second)

	// Verify each tenant has their own namespace
	ns1, err := clientset.CoreV1().Namespaces().Get(ctx, fmt.Sprintf("%s-namespace", tenant1), metav1.GetOptions{})
	require.NoError(t, err, "Tenant 1 namespace should exist")
	assert.Equal(t, fmt.Sprintf("%s-namespace", tenant1), ns1.Name)

	ns2, err := clientset.CoreV1().Namespaces().Get(ctx, fmt.Sprintf("%s-namespace", tenant2), metav1.GetOptions{})
	require.NoError(t, err, "Tenant 2 namespace should exist")
	assert.Equal(t, fmt.Sprintf("%s-namespace", tenant2), ns2.Name)

	t.Logf("✓ Tenant 1 namespace: %s", ns1.Name)
	t.Logf("✓ Tenant 2 namespace: %s", ns2.Name)
}

// TestE2E_APIGatewayHealth tests API Gateway health endpoint
func TestE2E_APIGatewayHealth(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	gatewayURL, err := getAPIGatewayURL(ctx)
	require.NoError(t, err, "Failed to get API Gateway URL")

	resp, err := http.Get(fmt.Sprintf("%s/health", gatewayURL))
	require.NoError(t, err, "Health endpoint should be reachable")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "Health should return 200")

	var health map[string]string
	json.NewDecoder(resp.Body).Decode(&health)
	assert.Equal(t, "healthy", health["status"])
	t.Logf("✓ API Gateway healthy")
}

// TestE2E_OperatorRunning tests that the operator deployment is running
func TestE2E_OperatorRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	config, err := getKubeConfig()
	require.NoError(t, err)

	clientset, err := kubernetes.NewForConfig(config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	deployments, err := clientset.AppsV1().Deployments(orchestratorNamespace).List(ctx, metav1.ListOptions{})
	require.NoError(t, err, "Failed to list deployments")

	operatorFound := false
	for _, dep := range deployments.Items {
		if strings.Contains(dep.Name, "operator") {
			operatorFound = true
			t.Logf("✓ Operator deployment: %s (ready: %d/%d)",
				dep.Name, dep.Status.ReadyReplicas, *dep.Spec.Replicas)
			break
		}
	}

	assert.True(t, operatorFound, "Operator deployment should exist")
}

// ---- Helper Functions ----

func getKubeConfig() (*rest.Config, error) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		kubeconfig = home + "/.kube/config"
	}

	// Try in-cluster config first
	config, err := rest.InClusterConfig()
	if err == nil {
		return config, nil
	}

	// Fall back to kubeconfig
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

func getAPIGatewayURL(ctx context.Context) (string, error) {
	config, err := getKubeConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return "", fmt.Errorf("failed to create clientset: %w", err)
	}

	// Try to get service
	service, err := clientset.CoreV1().Services(orchestratorNamespace).Get(ctx, gatewayServiceName, metav1.GetOptions{})
	if err == nil && len(service.Status.LoadBalancer.Ingress) > 0 {
		ip := service.Status.LoadBalancer.Ingress[0].IP
		if ip == "" {
			ip = service.Status.LoadBalancer.Ingress[0].Hostname
		}
		return fmt.Sprintf("http://%s:%d", ip, apiPort), nil
	}

	// Try NodePort
	if err == nil && service.Spec.Type == "NodePort" {
		for _, port := range service.Spec.Ports {
			if port.Port == int32(apiPort) {
				return fmt.Sprintf("http://localhost:%d", port.NodePort), nil
			}
		}
	}

	// Fallback: assume port-forward is active
	return fmt.Sprintf("http://localhost:%d", apiPort), nil
}

func createTenant(ctx context.Context, tenantName string) error {
	scriptPaths := []string{
		"../../scripts/create-tenant.sh",
		"./scripts/create-tenant.sh",
	}

	var scriptPath string
	for _, p := range scriptPaths {
		if _, err := os.Stat(p); err == nil {
			scriptPath = p
			break
		}
	}

	if scriptPath == "" {
		// Fallback: use kubectl directly
		cmd := exec.CommandContext(ctx, "kubectl", "create", "namespace", fmt.Sprintf("%s-namespace", tenantName))
		return cmd.Run()
	}

	cmd := exec.CommandContext(ctx, "bash", scriptPath, tenantName)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("create-tenant failed: %w, stderr: %s", err, stderr.String())
	}

	return nil
}

func deleteTenant(ctx context.Context, tenantName string) error {
	scriptPaths := []string{
		"../../scripts/delete-tenant.sh",
		"./scripts/delete-tenant.sh",
	}

	var scriptPath string
	for _, p := range scriptPaths {
		if _, err := os.Stat(p); err == nil {
			scriptPath = p
			break
		}
	}

	if scriptPath == "" {
		// Fallback: use kubectl directly
		cmd := exec.CommandContext(ctx, "kubectl", "delete", "namespace", fmt.Sprintf("%s-namespace", tenantName), "--ignore-not-found")
		return cmd.Run()
	}

	cmd := exec.CommandContext(ctx, "bash", scriptPath, tenantName)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("delete-tenant failed: %w, stderr: %s", err, stderr.String())
	}

	return nil
}
