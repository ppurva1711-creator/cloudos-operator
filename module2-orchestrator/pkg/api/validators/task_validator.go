package validators

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/orchestrator/module2-orchestrator/pkg/api"
)

// ValidationError represents a validation error for a specific field
type ValidationError struct {
	Field   string
	Message string
}

// ValidationErrors is a slice of validation errors
type ValidationErrors []ValidationError

// Error implements the error interface
func (ve ValidationErrors) Error() string {
	if len(ve) == 0 {
		return "no validation errors"
	}
	var messages []string
	for _, e := range ve {
		messages = append(messages, fmt.Sprintf("%s: %s", e.Field, e.Message))
	}
	return strings.Join(messages, "; ")
}

// ValidateCreateTaskRequest validates a CreateCloudTaskRequest
func ValidateCreateTaskRequest(req *api.CreateCloudTaskRequest) ValidationErrors {
	var errors ValidationErrors

	// Validate image (required, valid docker image format)
	if err := validateImage(req.Image); err != nil {
		errors = append(errors, ValidationError{Field: "image", Message: err.Error()})
	}

	// Validate command (required, non-empty)
	if err := validateCommand(req.Command); err != nil {
		errors = append(errors, ValidationError{Field: "command", Message: err.Error()})
	}

	// Validate resources if provided
	if req.Resources != nil {
		if err := validateResources(req.Resources); err != nil {
			for _, e := range err {
				errors = append(errors, e)
			}
		}
	}

	// Validate priority (0-100 range)
	if err := validatePriority(req.Priority); err != nil {
		errors = append(errors, ValidationError{Field: "priority", Message: err.Error()})
	}

	// Validate timeout if provided
	if req.Timeout != "" {
		if err := validateTimeout(req.Timeout); err != nil {
			errors = append(errors, ValidationError{Field: "timeout", Message: err.Error()})
		}
	}

	// Validate retries if provided
	if req.Retries > 10 {
		errors = append(errors, ValidationError{Field: "retries", Message: "must be between 0 and 10"})
	}

	// Note: TenantID is validated separately from JWT context in handler

	return errors
}

// validateImage validates docker image format
func validateImage(image string) error {
	if image == "" {
		return fmt.Errorf("required")
	}

	// Basic docker image format validation
	// Valid formats: image, image:tag, registry/image, registry/image:tag, registry:port/image:tag
	imageRegex := regexp.MustCompile(`^(?:[a-z0-9]|[._-])+(?::[0-9]+)?/(?:[a-z0-9]|[._-])+(?::[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127})?$|^(?:[a-z0-9]|[._-])+(?::[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127})?$`)

	if !imageRegex.MatchString(image) {
		return fmt.Errorf("invalid docker image format")
	}

	return nil
}

// validateCommand validates command array
func validateCommand(command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("required, must have at least one element")
	}

	// Check if any command is empty
	for _, cmd := range command {
		if cmd == "" {
			return fmt.Errorf("command elements cannot be empty")
		}
	}

	return nil
}

// validateResources validates resource requirements
func validateResources(resources *api.ResourceRequirementsReq) ValidationErrors {
	var errors ValidationErrors

	if resources.Requests != nil {
		if resources.Requests.CPU != "" {
			if err := validateQuantity("cpu", resources.Requests.CPU); err != nil {
				errors = append(errors, ValidationError{Field: "resources.requests.cpu", Message: err.Error()})
			}
		}
		if resources.Requests.Memory != "" {
			if err := validateQuantity("memory", resources.Requests.Memory); err != nil {
				errors = append(errors, ValidationError{Field: "resources.requests.memory", Message: err.Error()})
			}
		}
	}

	if resources.Limits != nil {
		if resources.Limits.CPU != "" {
			if err := validateQuantity("cpu", resources.Limits.CPU); err != nil {
				errors = append(errors, ValidationError{Field: "resources.limits.cpu", Message: err.Error()})
			}
		}
		if resources.Limits.Memory != "" {
			if err := validateQuantity("memory", resources.Limits.Memory); err != nil {
				errors = append(errors, ValidationError{Field: "resources.limits.memory", Message: err.Error()})
			}
		}
	}

	return errors
}

// validateQuantity validates Kubernetes resource quantity format
func validateQuantity(quantityType, value string) error {
	if value == "" {
		return nil
	}

	switch quantityType {
	case "cpu":
		// Accept: 100m, 1, 0.5, 2500m, etc.
		cpuRegex := regexp.MustCompile(`^\d+(\.\d+)?m?$`)
		if !cpuRegex.MatchString(value) {
			return fmt.Errorf("invalid CPU format (use: 100m, 1, 0.5)")
		}

	case "memory":
		// Accept: 128Mi, 256Mi, 1Gi, etc.
		memRegex := regexp.MustCompile(`^\d+(Ki|Mi|Gi|Ti|Pi)$`)
		if !memRegex.MatchString(value) {
			return fmt.Errorf("invalid memory format (use: 128Mi, 256Mi, 1Gi)")
		}
	}

	return nil
}

// validatePriority validates priority range
func validatePriority(priority int32) error {
	if priority < 0 || priority > 100 {
		return fmt.Errorf("must be between 0 and 100")
	}
	return nil
}

// validateTimeout validates timeout format
func validateTimeout(timeout string) error {
	if timeout == "" {
		return nil
	}

	// Expected format: number + unit (s, m, h)
	// Examples: 5s, 30m, 1h, 3600s
	timeoutRegex := regexp.MustCompile(`^(\d+)([smh])$`)
	matches := timeoutRegex.FindStringSubmatch(timeout)

	if len(matches) != 3 {
		return fmt.Errorf("invalid format (use: 5s, 30m, 1h)")
	}

	// Validate number is reasonable (positive)
	num, err := strconv.ParseInt(matches[1], 10, 32)
	if err != nil || num <= 0 {
		return fmt.Errorf("timeout must be positive")
	}

	return nil
}

// ValidateTenantID validates tenant ID format
func ValidateTenantID(tenantID string) error {
	if tenantID == "" {
		return fmt.Errorf("tenant ID required")
	}

	// Alphanumeric + hyphens only, no leading/trailing hyphens
	tenantIDRegex := regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$`)
	if !tenantIDRegex.MatchString(tenantID) {
		return fmt.Errorf("invalid format: must be alphanumeric with hyphens (no leading/trailing hyphens)")
	}

	if len(tenantID) > 63 {
		return fmt.Errorf("must be 63 characters or less")
	}

	return nil
}

// ValidateTaskID validates task ID format
func ValidateTaskID(taskID string) error {
	if taskID == "" {
		return fmt.Errorf("task ID required")
	}

	// Kubernetes DNS-1123 subdomain format
	idRegex := regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	if !idRegex.MatchString(taskID) {
		return fmt.Errorf("invalid format: must match Kubernetes naming rules [a-z0-9-]")
	}

	if len(taskID) > 63 {
		return fmt.Errorf("must be 63 characters or less")
	}

	return nil
}

// ValidateStatus validates task status filter
func ValidateStatus(status string) error {
	if status == "" {
		return nil // optional
	}

	validStatuses := map[string]bool{
		"Pending":   true,
		"Running":   true,
		"Completed": true,
		"Failed":    true,
	}

	if !validStatuses[status] {
		return fmt.Errorf("invalid status: must be one of Pending, Running, Completed, Failed")
	}

	return nil
}
