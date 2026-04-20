package validators

import (
	"testing"

	"github.com/orchestrator/module2-orchestrator/pkg/api"
	"github.com/stretchr/testify/assert"
)

// Tests for ValidateCreateTaskRequest

func TestValidateCreateTaskRequest_Valid(t *testing.T) {
	req := &api.CreateCloudTaskRequest{
		Name:     "test-task",
		Image:    "alpine:latest",
		Command:  []string{"echo", "hello"},
		TenantID: "tenant-1",
		Priority: 50,
		Retries:  3,
	}

	errors := ValidateCreateTaskRequest(req)
	assert.Empty(t, errors)
}

func TestValidateCreateTaskRequest_InvalidImage(t *testing.T) {
	req := &api.CreateCloudTaskRequest{
		Image:    "",
		Command:  []string{"echo"},
		TenantID: "tenant-1",
	}

	errors := ValidateCreateTaskRequest(req)
	assert.NotEmpty(t, errors)
	assert.Contains(t, errors[0].Field, "image")
}

func TestValidateCreateTaskRequest_EmptyCommand(t *testing.T) {
	req := &api.CreateCloudTaskRequest{
		Image:    "alpine:latest",
		Command:  []string{},
		TenantID: "tenant-1",
	}

	errors := ValidateCreateTaskRequest(req)
	assert.NotEmpty(t, errors)
	assert.Contains(t, errors[0].Field, "command")
}

func TestValidateCreateTaskRequest_InvalidPriority(t *testing.T) {
	req := &api.CreateCloudTaskRequest{
		Name:     "test",
		Image:    "alpine:latest",
		Command:  []string{"echo"},
		TenantID: "tenant-1",
		Priority: 150,
	}

	errors := ValidateCreateTaskRequest(req)
	assert.NotEmpty(t, errors)
	assert.Contains(t, errors[0].Field, "priority")
}

func TestValidateCreateTaskRequest_InvalidTimeout(t *testing.T) {
	req := &api.CreateCloudTaskRequest{
		Name:     "test",
		Image:    "alpine:latest",
		Command:  []string{"echo"},
		TenantID: "tenant-1",
		Timeout:  "invalid",
	}

	errors := ValidateCreateTaskRequest(req)
	assert.NotEmpty(t, errors)
	assert.Contains(t, errors[0].Field, "timeout")
}

func TestValidateCreateTaskRequest_ValidTimeout(t *testing.T) {
	tests := []string{"5s", "30m", "1h", "3600s"}

	for _, timeout := range tests {
		req := &api.CreateCloudTaskRequest{
			Name:     "test",
			Image:    "alpine:latest",
			Command:  []string{"echo"},
			TenantID: "tenant-1",
			Timeout:  timeout,
		}

		errors := ValidateCreateTaskRequest(req)
		assert.Empty(t, errors, "timeout %s should be valid", timeout)
	}
}

func TestValidateCreateTaskRequest_InvalidResources(t *testing.T) {
	req := &api.CreateCloudTaskRequest{
		Name:     "test",
		Image:    "alpine:latest",
		Command:  []string{"echo"},
		TenantID: "tenant-1",
		Resources: &api.ResourceRequirementsReq{
			Requests: &api.ResourceListReq{
				CPU: "invalid-cpu", // Invalid format
			},
		},
	}

	errors := ValidateCreateTaskRequest(req)
	assert.NotEmpty(t, errors)
}

func TestValidateCreateTaskRequest_ValidResources(t *testing.T) {
	req := &api.CreateCloudTaskRequest{
		Name:     "test",
		Image:    "alpine:latest",
		Command:  []string{"echo"},
		TenantID: "tenant-1",
		Resources: &api.ResourceRequirementsReq{
			Requests: &api.ResourceListReq{
				CPU:    "100m",
				Memory: "128Mi",
			},
			Limits: &api.ResourceListReq{
				CPU:    "500m",
				Memory: "512Mi",
			},
		},
	}

	errors := ValidateCreateTaskRequest(req)
	assert.Empty(t, errors)
}

// Tests for ValidateImage

func TestValidateImage_Valid(t *testing.T) {
	tests := []string{
		"alpine:latest",
		"alpine",
		"gcr.io/project/image:v1",
		"registry.example.com:5000/myimage:latest",
		"ubuntu:20.04",
	}

	for _, image := range tests {
		err := validateImage(image)
		assert.NoError(t, err, "image %s should be valid", image)
	}
}

func TestValidateImage_Invalid(t *testing.T) {
	tests := []string{
		"",
		"invalid..image",
		"-invalid-start",
	}

	for _, image := range tests {
		err := validateImage(image)
		assert.Error(t, err, "image %s should be invalid", image)
	}
}

// Tests for ValidateCommand

func TestValidateCommand_Valid(t *testing.T) {
	tests := [][]string{
		{"echo"},
		{"echo", "hello", "world"},
		{"/bin/bash", "-c", "echo test"},
	}

	for _, cmd := range tests {
		err := validateCommand(cmd)
		assert.NoError(t, err, "command %v should be valid", cmd)
	}
}

func TestValidateCommand_Invalid(t *testing.T) {
	tests := [][]string{
		{},                  // empty
		{"", "echo", "hi"},  // empty element
	}

	for _, cmd := range tests {
		err := validateCommand(cmd)
		assert.Error(t, err, "command %v should be invalid", cmd)
	}
}

// Tests for ValidateQuantity

func TestValidateQuantity_CPU_Valid(t *testing.T) {
	tests := []string{
		"100m",
		"500m",
		"1",
		"2",
		"0.5",
		"0.1",
	}

	for _, cpu := range tests {
		err := validateQuantity("cpu", cpu)
		assert.NoError(t, err, "cpu %s should be valid", cpu)
	}
}

func TestValidateQuantity_CPU_Invalid(t *testing.T) {
	tests := []string{
		"invalid",
		"-100m",
		"100x",
	}

	for _, cpu := range tests {
		err := validateQuantity("cpu", cpu)
		assert.Error(t, err, "cpu %s should be invalid", cpu)
	}
}

func TestValidateQuantity_Memory_Valid(t *testing.T) {
	tests := []string{
		"128Mi",
		"256Mi",
		"1Gi",
		"1Ti",
		"512Ki",
	}

	for _, memory := range tests {
		err := validateQuantity("memory", memory)
		assert.NoError(t, err, "memory %s should be valid", memory)
	}
}

func TestValidateQuantity_Memory_Invalid(t *testing.T) {
	tests := []string{
		"128MB",
		"1G",
		"invalid",
		"256M",
	}

	for _, memory := range tests {
		err := validateQuantity("memory", memory)
		assert.Error(t, err, "memory %s should be invalid", memory)
	}
}

// Tests for ValidatePriority

func TestValidatePriority_Valid(t *testing.T) {
	tests := []int32{0, 1, 50, 99, 100}

	for _, priority := range tests {
		err := validatePriority(priority)
		assert.NoError(t, err, "priority %d should be valid", priority)
	}
}

func TestValidatePriority_Invalid(t *testing.T) {
	tests := []int32{-1, 101, 200}

	for _, priority := range tests {
		err := validatePriority(priority)
		assert.Error(t, err, "priority %d should be invalid", priority)
	}
}

// Tests for ValidateTimeout

func TestValidateTimeout_Valid(t *testing.T) {
	tests := []string{
		"5s",
		"30m",
		"1h",
		"3600s",
		"60m",
	}

	for _, timeout := range tests {
		err := validateTimeout(timeout)
		assert.NoError(t, err, "timeout %s should be valid", timeout)
	}
}

func TestValidateTimeout_Invalid(t *testing.T) {
	tests := []string{
		"0s",       // zero
		"-5s",      // negative
		"5x",       // invalid unit
		"invalid",  // completely invalid
		"5",        // no unit
	}

	for _, timeout := range tests {
		err := validateTimeout(timeout)
		assert.Error(t, err, "timeout %s should be invalid", timeout)
	}
}

// Tests for ValidateTenantID

func TestValidateTenantID_Valid(t *testing.T) {
	tests := []string{
		"tenant-1",
		"tenant1",
		"a",
		"tenant-with-hyphens",
		"a1b2c3",
	}

	for _, id := range tests {
		err := ValidateTenantID(id)
		assert.NoError(t, err, "tenant ID %s should be valid", id)
	}
}

func TestValidateTenantID_Invalid(t *testing.T) {
	tests := []string{
		"",           // empty
		"-tenant",    // starts with hyphen
		"tenant-",    // ends with hyphen
		"tenant_id",  // underscore
		"Tenant-ID",  // uppercase
	}

	for _, id := range tests {
		err := ValidateTenantID(id)
		assert.Error(t, err, "tenant ID %s should be invalid", id)
	}
}

func TestValidateTenantID_TooLong(t *testing.T) {
	longID := ""
	for i := 0; i < 64; i++ {
		longID += "a"
	}

	err := ValidateTenantID(longID)
	assert.Error(t, err)
}

// Tests for ValidateTaskID

func TestValidateTaskID_Valid(t *testing.T) {
	tests := []string{
		"task-1",
		"task1",
		"a",
		"task-with-hyphens",
		"a1b2c3",
	}

	for _, id := range tests {
		err := ValidateTaskID(id)
		assert.NoError(t, err, "task ID %s should be valid", id)
	}
}

func TestValidateTaskID_Invalid(t *testing.T) {
	tests := []string{
		"",          // empty
		"-task",     // starts with hyphen
		"task-",     // ends with hyphen
		"Task-ID",   // uppercase
		"task_id",   // underscore
	}

	for _, id := range tests {
		err := ValidateTaskID(id)
		assert.Error(t, err, "task ID %s should be invalid", id)
	}
}

// Tests for ValidateStatus

func TestValidateStatus_Valid(t *testing.T) {
	tests := []string{"Pending", "Running", "Completed", "Failed"}

	for _, status := range tests {
		err := ValidateStatus(status)
		assert.NoError(t, err, "status %s should be valid", status)
	}
}

func TestValidateStatus_Invalid(t *testing.T) {
	tests := []string{"InvalidStatus", "running", "pending", "completed"}

	for _, status := range tests {
		err := ValidateStatus(status)
		assert.Error(t, err, "status %s should be invalid", status)
	}
}

func TestValidateStatus_Empty(t *testing.T) {
	err := ValidateStatus("")
	assert.NoError(t, err) // Empty is allowed (optional parameter)
}

// Tests for ValidationErrors

func TestValidationErrors_Error(t *testing.T) {
	errors := ValidationErrors{
		{Field: "image", Message: "required"},
		{Field: "command", Message: "must be non-empty"},
	}

	errMsg := errors.Error()
	assert.Contains(t, errMsg, "image")
	assert.Contains(t, errMsg, "command")
	assert.Contains(t, errMsg, "required")
}

func TestValidationErrors_SingleError(t *testing.T) {
	errors := ValidationErrors{
		{Field: "field", Message: "error message"},
	}

	errMsg := errors.Error()
	assert.Contains(t, errMsg, "field: error message")
}
