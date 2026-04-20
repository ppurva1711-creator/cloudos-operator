package api

// CreateCloudTaskRequest represents the request body for creating a CloudTask
type CreateCloudTaskRequest struct {
	Name      string                 `json:"name" binding:"required"`
	Image     string                 `json:"image" binding:"required"`
	Command   []string               `json:"command,omitempty"`
	Args      []string               `json:"args,omitempty"`
	TenantID  string                 `json:"tenantID" binding:"required"`
	Resources *ResourceRequirementsReq `json:"resources,omitempty"`
	Retries   int32                  `json:"retries,omitempty"`
	Timeout   string                 `json:"timeout,omitempty"`
	Priority  int32                  `json:"priority,omitempty"`
}

// ResourceRequirementsReq represents resource requirements
type ResourceRequirementsReq struct {
	Requests *ResourceListReq `json:"requests,omitempty"`
	Limits   *ResourceListReq `json:"limits,omitempty"`
}

// ResourceListReq represents CPU and memory resources
type ResourceListReq struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// CloudTaskResponse represents the API response for a CloudTask
type CloudTaskResponse struct {
	Name           string                `json:"name"`
	Namespace      string                `json:"namespace"`
	Phase          string                `json:"phase"`
	PodName        string                `json:"podName,omitempty"`
	Message        string                `json:"message,omitempty"`
	RetryCount     int32                 `json:"retryCount"`
	CreatedAt      string                `json:"createdAt"`
	CompletedAt    string                `json:"completedAt,omitempty"`
	Spec           CreateCloudTaskRequest `json:"spec"`
}

// ListCloudTasksResponse represents the API response for listing CloudTasks
type ListCloudTasksResponse struct {
	Items []CloudTaskResponse `json:"items"`
	Count int                 `json:"count"`
}

// ErrorResponse represents an error response from the API
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// HealthCheckResponse represents the health check response
type HealthCheckResponse struct {
	Status     string `json:"status"`
	Version    string `json:"version"`
	KubeStatus string `json:"kubeStatus"`
}
