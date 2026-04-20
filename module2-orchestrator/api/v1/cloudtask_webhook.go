/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// This file is for implementing webhooks, though it's optional.

// log is for logging in this package.
var cloudtasklog = logf.Log.WithName("cloudtask-resource")

func (r *CloudTask) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-tasks-orchestrator-dev-v1-cloudtask,mutating=true,failurePolicy=fail,sideEffects=None,groups=tasks.orchestrator.dev,resources=cloudtasks,verbs=create;update,versions=v1,name=mcloudtask.kb.io,admissionReviewVersions=v1

// Default implements admission.DecoderInjector
func (r *CloudTask) Default() {
	cloudtasklog.Info("default", "name", r.Name)

	// Set default retries
	if r.Spec.Retries == 0 {
		r.Spec.Retries = 3
	}

	// Set default priority
	if r.Spec.Priority == 0 {
		r.Spec.Priority = 50
	}

	// Set default timeout
	if r.Spec.Timeout == "" {
		r.Spec.Timeout = "5m"
	}
}

// +kubebuilder:webhook:path=/validate-tasks-orchestrator-dev-v1-cloudtask,mutating=false,failurePolicy=fail,sideEffects=None,groups=tasks.orchestrator.dev,resources=cloudtasks,verbs=create;update,versions=v1,name=vcloudtask.kb.io,admissionReviewVersions=v1

// ValidateCreate implements webhook validation for create
func (r *CloudTask) ValidateCreate() (admission.Warnings, error) {
	cloudtasklog.Info("validate create", "name", r.Name)

	var allErr admission.Warnings
	if r.Spec.Image == "" {
		allErr = append(allErr, "image is required")
	}
	if r.Spec.TenantID == "" {
		allErr = append(allErr, "tenantID is required")
	}
	if r.Spec.Retries < 0 || r.Spec.Retries > 10 {
		allErr = append(allErr, "retries must be between 0 and 10")
	}
	if r.Spec.Priority < 0 || r.Spec.Priority > 100 {
		allErr = append(allErr, "priority must be between 0 and 100")
	}

	if len(allErr) > 0 {
		return allErr, fmt.Errorf("cloudtask validation failed")
	}
	return nil, nil
}

// ValidateUpdate implements webhook validation for update
func (r *CloudTask) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	cloudtasklog.Info("validate update", "name", r.Name)

	oldTask := old.(*CloudTask)
	var allErr admission.Warnings

	// Prevent image change after creation
	if r.Spec.Image != oldTask.Spec.Image {
		allErr = append(allErr, "image cannot be changed after creation")
	}

	// Prevent tenant change
	if r.Spec.TenantID != oldTask.Spec.TenantID {
		allErr = append(allErr, "tenantID cannot be changed")
	}

	if r.Spec.Retries < 0 || r.Spec.Retries > 10 {
		allErr = append(allErr, "retries must be between 0 and 10")
	}
	if r.Spec.Priority < 0 || r.Spec.Priority > 100 {
		allErr = append(allErr, "priority must be between 0 and 100")
	}

	if len(allErr) > 0 {
		return allErr, fmt.Errorf("cloudtask validation failed")
	}
	return nil, nil
}

// ValidateDelete implements webhook validation for delete
func (r *CloudTask) ValidateDelete() (admission.Warnings, error) {
	cloudtasklog.Info("validate delete", "name", r.Name)
	return nil, nil
}
