# Troubleshooting Guide

## Quick Diagnostics

### System Health Check

```bash
#!/bin/bash
# Run this to get a complete health overview

echo "=== Kubernetes Cluster ==="
kubectl cluster-info
kubectl get nodes

echo "=== Operator Status ==="
kubectl get deployment cloudtask-operator -n orchestrator
kubectl get pods -n orchestrator
kubectl logs -f deployment/cloudtask-operator -n orchestrator

echo "=== API Gateway Status ==="
kubectl get deployment api-gateway -n orchestrator
kubectl get svc api-gateway -n orchestrator

echo "=== CloudTasks Status ==="
kubectl get cloudtasks --all-namespaces
kubectl get pods --all-namespaces -l cloudtask-name

echo "=== CRD Status ==="
kubectl get crd cloudtasks.tasks.orchestrator.dev

echo "=== Events ==="
kubectl get events --all-namespaces --sort-by='.lastTimestamp'

echo "=== Resource Usage ==="
kubectl top pods -n orchestrator
kubectl top nodes
```

## Common Issues and Solutions

### Issue 1: Operator Pod Stuck in CrashLoopBackOff

**Symptoms:**
```
$ kubectl get pods -n orchestrator
NAME                                   READY   STATUS             RESTARTS   AGE
cloudtask-operator-7f8d7c5d8-abc123    0/1     CrashLoopBackOff   5          2m
```

**Diagnosis:**

```bash
# Check logs
kubectl logs deployment/cloudtask-operator -n orchestrator --tail=50

# Check events
kubectl describe pod -n orchestrator -l app=cloudtask-operator

# Check previous logs (before crash)
kubectl logs deployment/cloudtask-operator -n orchestrator --previous
```

**Common Causes & Solutions:**

1. **Missing RBAC permissions**
   ```bash
   # Check if operator can access CloudTask CRD
   kubectl auth can-i create cloudtasks \
     --as=system:serviceaccount:orchestrator:cloudtask-operator
   
   # Should return: yes
   # If no: apply RBAC config
   kubectl apply -f config/rbac/rbac.yaml
   ```

2. **CRD not installed**
   ```bash
   # Verify CRD exists
   kubectl get crd cloudtasks.tasks.orchestrator.dev
   
   # If not found: install CRD
   kubectl apply -f config/crd/cloudtask_crd.yaml
   ```

3. **Image pull errors**
   ```bash
   # Check image exists
   docker pull module2-orchestrator-operator:latest
   
   # If fails: build and push
   make docker-build docker-push
   
   # Check image pull secrets
   kubectl get secrets -n orchestrator
   ```

4. **Insufficient resources**
   ```bash
   # Check node capacity
   kubectl describe nodes
   
   # Check operator requests/limits
   kubectl get pod -n orchestrator -o yaml -l app=cloudtask-operator
   
   # If insufficient: add nodes or adjust requests
   ```

5. **Webhook configuration issues**
   ```bash
   # Check webhooks
   kubectl get validatingwebhookconfigurations
   kubectl get mutatingwebhookconfigurations
   
   # Check webhook service
   kubectl get svc cloudtask-webhook-service -n orchestrator
   
   # If missing: disable webhooks
   kubectl set env deployment/cloudtask-operator \
     -n orchestrator ENABLE_WEBHOOKS=false
   ```

### Issue 2: CloudTask Stuck in Pending Phase

**Symptoms:**
```
$ kubectl get cloudtasks
NAME             PHASE    PODNAME         AGE
my-task          Pending  my-task-pod     5m
```

Pod doesn't get created, task never progresses.

**Diagnosis:**

```bash
# Check CloudTask status detail
kubectl describe cloudtask my-task

# Check operator logs for errors
kubectl logs deployment/cloudtask-operator -n orchestrator | grep my-task

# Check events
kubectl get events --field-selector involvedObject.name=my-task
```

**Common Causes & Solutions:**

1. **Pod creation failures due to image**
   ```bash
   # Check if image is accessible
   kubectl run test-image --image=<image-from-task> --dry-run=client
   
   # Or manually try to pull
   docker pull <image-from-task>
   
   # If fails: push image to accessible registry
   docker tag app:latest myregistry.io/app:latest
   docker push myregistry.io/app:latest
   
   # Update CloudTask with correct image
   ```

2. **Node selector constraints**
   ```bash
   # Check if nodes match task requirements
   kubectl get nodes --show-labels
   
   # Check if node has enough resources
   kubectl describe node <node-name>
   
   # If insufficient: add more nodes
   ```

3. **Resource quota exceeded**
   ```bash
   # Check namespace quota
   kubectl describe resourcequota -n <namespace>
   
   # If quota hit:
   # Option 1: Increase quota
   kubectl patch resourcequota <quota-name> -n <namespace> \
     -p '{"spec":{"hard":{"pods":"200"}}}'
   
   # Option 2: Delete completed tasks to free resources
   kubectl delete cloudtasks --field-selector status.phase=Completed -n <namespace>
   ```

4. **Webhook validation rejection**
   ```bash
   # Check webhook logs
   kubectl logs deployment/cloudtask-operator -n orchestrator | grep webhook
   
   # Check webhook rules
   kubectl get validatingwebhookconfiguration -o yaml
   
   # If webhook rejecting: check CloudTask spec
   # - image must be present
   # - tenantID must be present
   # - retries must be 0-10
   # - priority must be 0-100
   ```

5. **Operator not watching resources**
   ```bash
   # Check operator is running
   kubectl get deployment cloudtask-operator -n orchestrator
   
   # Check operator logs for watch errors
   kubectl logs deployment/cloudtask-operator -n orchestrator | grep -i watch
   
   # If errors: restart operator
   kubectl rollout restart deployment/cloudtask-operator -n orchestrator
   ```

### Issue 3: Pod Fails Immediately With Exit Code 1

**Symptoms:**
```
$ kubectl describe cloudtask my-task
Status:
  Phase: Failed
  Message: Pod failed with exit code 1
```

**Diagnosis:**

```bash
# Get pod name from CloudTask status
POD_NAME=$(kubectl get cloudtask my-task -o jsonpath='{.status.podName}')

# Check pod logs
kubectl logs $POD_NAME

# Check pod events
kubectl describe pod $POD_NAME

# Check container last state
kubectl get pod $POD_NAME -o jsonpath='{.status.containerStatuses[0].lastState}'
```

**Solution:**

1. **Debug container image**
   ```bash
   # Run container locally
   docker run --rm -it <image> /bin/sh
   
   # Check if command works
   docker run --rm -it <image> <command> <args>
   ```

2. **Check CloudTask spec**
   ```bash
   # View full task definition
   kubectl get cloudtask my-task -o yaml
   
   # Verify:
   # - Image exists and is pullable
   # - Command is correct
   # - Arguments are valid
   # - Environment variables are set correctly
   ```

3. **Check Pod specification**
   ```bash
   # Get pod spec to see what was actually created
   kubectl get pod $POD_NAME -o yaml
   
   # Look for:
   # - Image digest
   # - Environment variables
   # - Resource limits
   # - Volume mounts
   ```

### Issue 4: Operator Reconciliation Too Slow

**Symptoms:**
```
- Tasks taking 1+ minute to start after creation
- High latency between status changes
- Operator CPU usage high
```

**Diagnosis:**

```bash
# Check reconciliation rate
kubectl logs deployment/cloudtask-operator -n orchestrator \
  | grep -i "reconciliation\|requeue"

# Check operator resource usage
kubectl top pod -n orchestrator -l app=cloudtask-operator

# Check etcd latency
kubectl get events -n orchestrator --sort-by='.lastTimestamp'

# Check queue depth
kubectl logs deployment/cloudtask-operator -n orchestrator | grep "queue depth"
```

**Solutions:**

1. **Increase operator replicas and worker count**
   ```bash
   # Scale to 3 replicas
   kubectl scale deployment cloudtask-operator \
     --replicas=3 -n orchestrator
   
   # Increase worker threads (in deployment env)
   kubectl set env deployment/cloudtask-operator \
     -n orchestrator MAX_CONCURRENT_RECONCILES=20
   ```

2. **Reduce reconciliation interval**
   ```bash
   # Lower requeue time (more CPU, lower latency)
   kubectl set env deployment/cloudtask-operator \
     -n orchestrator RECONCILIATION_INTERVAL=15s
   ```

3. **Check etcd performance**
   ```bash
   # View etcd statistics
   kubectl exec -n kube-system -it etcd-<node> -- etcdctl member list
   
   # If slow: scale up etcd or use SSD backend
   ```

4. **Optimize CloudTask watching**
   ```bash
   # Reduce label selector complexity during watch
   # Check field indexing in controller setup
   kubectl logs deployment/cloudtask-operator -n orchestrator \
     | grep -i "field.*index"
   ```

### Issue 5: Pod Resource Limits Causing Failures

**Symptoms:**
```
$ kubectl describe pod <pod-name>
  "Reason: OOMKilled"  # Out of memory
  or "Reason: Evicted"  # Evicted due to resource pressure
```

**Diagnosis:**

```bash
# Check pod resource usage
kubectl top pod <pod-name>

# Check node resource pressure
kubectl describe node <node-name> | grep -A5 "Conditions"

# Check resource limits in CloudTask
kubectl get cloudtask my-task -o jsonpath='{.spec.resources}'
```

**Solutions:**

1. **Increase resource limits**
   ```bash
   # Edit CloudTask
   kubectl edit cloudtask my-task
   
   # Increase limits in spec.resources.limits
   # limits:
   #   cpu: "2000m"  # increased from 1000m
   #   memory: "2Gi"  # increased from 1Gi
   ```

2. **Add more nodes**
   ```bash
   # If cluster is resource-constrained
   kubectl get nodes
   
   # Add node via cloud provider CLI
   # Then requeue task
   kubectl delete pod <pod-name>
   ```

3. **Monitor node resources**
   ```bash
   # Check node allocation
   kubectl describe node <node-name>
   
   # Look for: Allocated resources, Condition: MemoryPressure, DiskPressure
   ```

### Issue 6: API Gateway Unreachable

**Symptoms:**
```
curl: (7) Failed to connect to api.cloudtask.local port 80
```

**Diagnosis:**

```bash
# Check service
kubectl get svc api-gateway -n orchestrator
kubectl describe svc api-gateway -n orchestrator

# Check deployment
kubectl get deployment api-gateway -n orchestrator

# Check ingress
kubectl get ingress -n orchestrator
kubectl describe ingress cloudtask-ingress -n orchestrator

# Check ingress controller
kubectl get deployment -n ingress-nginx
kubectl logs -n ingress-nginx deployment/nginx-ingress-controller
```

**Solutions:**

1. **Verify API Gateway is running**
   ```bash
   # Check pods
   kubectl get pods -n orchestrator -l app=api-gateway
   
   # If not running: check events
   kubectl describe pod -n orchestrator -l app=api-gateway
   
   # If CrashLoopBackOff: check logs
   kubectl logs deployment/api-gateway -n orchestrator
   ```

2. **Check service endpoints**
   ```bash
   # Verify endpoints exist
   kubectl get endpoints api-gateway -n orchestrator
   
   # If no endpoints: pods might not be ready
   kubectl get pods -n orchestrator -l app=api-gateway -o wide
   ```

3. **Test service connectivity**
   ```bash
   # Port-forward to service
   kubectl port-forward -n orchestrator svc/api-gateway 8000:80 &
   curl http://localhost:8000/health
   ```

4. **Verify ingress configuration**
   ```bash
   # Check ingress rules
   kubectl get ingress cloudtask-ingress -n orchestrator -o yaml
   
   # Verify DNS if using hostname
   nslookup api.cloudtask.local
   
   # If DNS fails: add to /etc/hosts
   echo "127.0.0.1 api.cloudtask.local" >> /etc/hosts
   ```

### Issue 7: Authentication Failures (401 Unauthorized)

**Symptoms:**
```
$ curl http://api.cloudtask.local/tasks
{"error":"Unauthorized","message":"Missing or invalid token"}
```

**Diagnosis:**

```bash
# Check token validity
TOKEN=$(curl -X POST http://api.cloudtask.local/auth/token \
  -d '{"username":"user","password":"pass"}' | jq -r .token)

# Check token claims
echo $TOKEN | base64 -d | jq

# Test with token
curl -H "Authorization: Bearer $TOKEN" http://api.cloudtask.local/tasks
```

**Solutions:**

1. **Obtain valid token**
   ```bash
   # Request new token
   curl -X POST http://api.cloudtask.local/auth/token \
     -H "Content-Type: application/json" \
     -d '{
       "username": "user@example.com",
       "password": "secure_password"
     }'
   ```

2. **Check JWT secret**
   ```bash
   # Verify JWT secret is set
   kubectl get secret api-jwt-secret -n orchestrator -o yaml
   
   # If missing: create
   kubectl create secret generic api-jwt-secret \
     --from-literal=secret=$(openssl rand -base64 32) \
     -n orchestrator
   
   # Restart gateway
   kubectl rollout restart deployment/api-gateway -n orchestrator
   ```

3. **Check token expiration**
   ```bash
   # Decode token (JWT format: header.payload.signature)
   TOKEN_PAYLOAD=$(echo $TOKEN | cut -d. -f2 | base64 -d)
   echo $TOKEN_PAYLOAD | jq .exp
   
   # Compare with current time
   date +%s
   
   # If exp < current time: token expired, get new one
   ```

### Issue 8: Network Policy Blocking Traffic

**Symptoms:**
```
Pod can't connect to external services
Operator can't reach API server
```

**Diagnosis:**

```bash
# Check network policies
kubectl get networkpolicy --all-namespaces

# Check specific policy
kubectl describe networkpolicy <policy-name> -n <namespace>

# Test connectivity from pod
kubectl exec -it <pod-name> -- ping <target-ip>
kubectl exec -it <pod-name> -- curl http://<target-service>
```

**Solutions:**

1. **Verify policy allows required traffic**
   ```bash
   # Check what's allowed
   kubectl get networkpolicy -n orchestrator -o yaml
   
   # For operator to reach API server:
   # - Make sure egress to kube-apiserver:443 is allowed
   # - Check if default policy is "deny all"
   ```

2. **Temporarily disable policy to test**
   ```bash
   # Delete policy
   kubectl delete networkpolicy <policy-name> -n <namespace>
   
   # Test connectivity
   # If works: policy needs adjustment
   # If fails: issue is elsewhere
   
   # Restore policy
   kubectl apply -f config/networking/network-policy.yaml
   ```

3. **Allow required namespaces**
   ```bash
   # For inter-namespace communication
   kubectl label namespace tenant-1 namespace-name=tenant-1
   
   # Add to network policy:
   namespaceSelector:
      matchLabels:
        namespace-name: tenant-1
   ```

### Issue 9: Pod Never Transitions from Running to Completed

**Symptoms:**
```
CloudTask phase: Running (stuck for hours)
Pod still running even though command finished
```

**Diagnosis:**

```bash
# Check pod status
kubectl get pod <pod-name> -o yaml

# Check container state
kubectl get pod <pod-name> -o jsonpath='{.status.containerStatuses[0]}'

# Check if process is still running
kubectl exec <pod-name> -- ps aux

# Check command output
kubectl logs <pod-name>
```

**Solutions:**

1. **Check if command is properly terminating**
   ```bash
   # Verify command completes
   docker run --rm <image> <command> <args>
   
   # Check exit code
   docker run --rm <image> <command> <args>; echo $?
   ```

2. **Check pod timeout**
   ```bash
   # Get CloudTask timeout
   kubectl get cloudtask <name> -o jsonpath='{.spec.timeout}'
   
   # Check activeDeadlineSeconds in pod
   kubectl get pod <pod-name> -o jsonpath='{.spec.activeDeadlineSeconds}'
   
   # If timeout expired:
   kubectl describe pod <pod-name> | grep -i "deadline"
   ```

3. **Check if pod is hung**
   ```bash
   # Try to get pod logs (might be stuck)
   timeout 5 kubectl logs <pod-name> || echo "Timeout getting logs"
   
   # Try to delete pod
   kubectl delete pod <pod-name> --grace-period=60
   ```

4. **Manual fix**
   ```bash
   # Delete pod to force status update
   kubectl delete pod <pod-name>
   
   # Operator will create new pod on next reconciliation
   
   # Or mark as completed manually
   kubectl patch cloudtask <name> \
     -p '{"status":{"phase":"Completed"}}' --type merge
   ```

### Issue 10: Webhook Validation Preventing CloudTask Creation

**Symptoms:**
```
$ kubectl apply -f cloudtask.yaml
Error from server (Forbidden): 
error when creating "cloudtask.yaml": admission webhook "cloudtask.tasks.orchestrator.dev" denied the request
```

**Diagnosis:**

```bash
# Check webhook configuration
kubectl get validatingwebhookconfigurations

# View validation rules
kubectl get validatingwebhookconfig cloudtask-validating-webhook -o yaml

# Check webhook service
kubectl get svc cloudtask-webhook-service -n orchestrator

# Check webhook logs
kubectl logs deployment/cloudtask-operator -n orchestrator | grep -i webhook
```

**Solutions:**

1. **Check CloudTask spec against validation rules**
   ```yaml
   # Required fields:
   spec:
     image: "required"           # Must be non-empty
     tenantID: "required"        # Must be non-empty
     retries: 0-10               # Must be in range
     priority: 0-100             # Must be in range
   ```

2. **Temporarily disable webhooks**
   ```bash
   # Set environment variable
   kubectl set env deployment/cloudtask-operator \
     -n orchestrator ENABLE_WEBHOOKS=false
   
   # Restart
   kubectl rollout restart deployment/cloudtask-operator -n orchestrator
   ```

3. **Fix CloudTask specification**
   ```yaml
   apiVersion: tasks.orchestrator.dev/v1
   kind: CloudTask
   metadata:
     name: fixed-task
   spec:
     image: python:3.11-slim      # Add image
     tenantID: tenant-1            # Add tenantID
     command: ["python"]
     args: ["/app/script.py"]
     retries: 3                    # Valid range 0-10
     timeout: "10m"
     priority: 50                  # Valid range 0-100
   ```

## Performance Troubleshooting

### High Operator CPU Usage

```bash
# Check CPU
kubectl top pod -n orchestrator -l app=cloudtask-operator

# Check reconciliation load
kubectl logs deployment/cloudtask-operator -n orchestrator | grep "reconcile" | wc -l

# Reduce frequency (trades latency for CPU)
kubectl set env deployment/cloudtask-operator \
  -n orchestrator RECONCILIATION_INTERVAL=60s

# Or scale out (add more replicas)
kubectl scale deployment cloudtask-operator --replicas=3 -n orchestrator
```

### High Memory Usage

```bash
# Check memory
kubectl top pod -n orchestrator -l app=cloudtask-operator

# Check memory limits
kubectl get pod -n orchestrator -l app=cloudtask-operator -o yaml | grep -A2 memory

# Increase limits
kubectl set resources deployment/cloudtask-operator \
  -n orchestrator --limits=memory=1Gi

# Or identify memory leak
# - Check logs for repeated allocations
# - Profile with pprof
kubectl exec -it <pod> -- curl http://localhost:6060/debug/pprof/heap
```

### Slow API Response Times

```bash
# Check API gateway logs
kubectl logs deployment/api-gateway -n orchestrator | grep "duration"

# Check database latency (if using)
# Check network latency
kubectl exec -it <api-pod> -- ping <target>

# Check load
# More replicas can help
kubectl scale deployment api-gateway --replicas=5 -n orchestrator
```

## Monitoring and Alerting

### Key Metrics to Monitor

```
cloudtask_status_total{phase="Pending"}   # Should decrease
cloudtask_status_total{phase="Completed"} # Should increase
cloudtask_pod_creation_failures_total     # Should be near zero
cloudtask_reconciliation_errors_total     # Should be near zero
cloudtask_reconciliation_duration_seconds # Should be < 1s
```

### Query Examples (Prometheus)

```promql
# Current pending tasks
count(cloudtask_status{phase="Pending"})

# Task completion rate (per minute)
rate(cloudtask_status{phase="Completed"}[1m])

# Pod creation failure rate
rate(cloudtask_pod_creation_failures_total[5m])

# Slow reconciliations
cloudtask_reconciliation_duration_seconds > 1

# Reconciliation errors
rate(cloudtask_reconciliation_errors_total[5m]) > 0
```

## Getting Help

### Collecting Diagnostic Information

```bash
#!/bin/bash
# Generate diagnostic bundle

mkdir -p diagnostics

# Cluster info
kubectl cluster-info > diagnostics/cluster-info.txt
kubectl get nodes > diagnostics/nodes.txt

# Operator info
kubectl get deployment -n orchestrator > diagnostics/deployments.txt
kubectl describe deployment cloudtask-operator -n orchestrator > diagnostics/operator-deployment.txt
kubectl logs deployment/cloudtask-operator -n orchestrator > diagnostics/operator-logs.txt

# API Gateway info
kubectl logs deployment/api-gateway -n orchestrator > diagnostics/gateway-logs.txt

# CloudTasks
kubectl get cloudtasks --all-namespaces > diagnostics/cloudtasks.txt
kubectl describe cloudtasks --all-namespaces > diagnostics/cloudtasks-describe.txt

# Events
kubectl get events --all-namespaces > diagnostics/events.txt

# Resources
kubectl top nodes > diagnostics/node-resources.txt
kubectl top pods -n orchestrator > diagnostics/pod-resources.txt

# Tar for export
tar czf diagnostics.tar.gz diagnostics/
echo "Diagnostic bundle: diagnostics.tar.gz"
```

### Support Resources

- **Documentation**: `/docs/OPERATOR.md`, `/docs/SETUP.md`, `/docs/API.md`
- **Issue Tracker**: GitHub Issues
- **Community**: Slack channel #cloudtask
- **Email**: support@cloudtask.dev
