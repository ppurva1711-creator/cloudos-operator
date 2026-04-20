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

package controllers

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	tasksv1 "github.com/orchestrator/module2-orchestrator/api/v1"
	"github.com/orchestrator/module2-orchestrator/pkg/utils"
	"github.com/sirupsen/logrus"
)

const (
	cloudTaskOwnerKey     = ".metadata.controller"
	cloudTaskFinalizerKey = "tasks.orchestrator.dev/finalizer"
	podNameLabel          = "cloudtask-name"
	podTenantLabel        = "tenant-id"
	podPriorityLabel      = "priority"
)

var (
	apiGVStr = tasksv1.GroupVersion.String()
)

// CloudTaskReconciler reconciles a CloudTask object
type CloudTaskReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Log      *logrus.Logger
}

//+kubebuilder:rbac:groups=tasks.orchestrator.dev,resources=cloudtasks,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=tasks.orchestrator.dev,resources=cloudtasks/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=tasks.orchestrator.dev,resources=cloudtasks/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile implements the reconciliation loop for CloudTask resources
func (r *CloudTaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithFields(logrus.Fields{
		"cloudtask": req.NamespacedName.String(),
	})

	// Fetch the CloudTask instance
	cloudTask := &tasksv1.CloudTask{}
	if err := r.Get(ctx, req.NamespacedName, cloudTask); err != nil {
		if apierrors.IsNotFound(err) {
			log.Debugf("CloudTask not found, ignoring")
			return ctrl.Result{}, nil
		}
		log.Errorf("Failed to get CloudTask: %v", err)
		return ctrl.Result{}, err
	}

	log = log.WithFields(logrus.Fields{
		"tenant": cloudTask.Spec.TenantID,
		"image":  cloudTask.Spec.Image,
	})

	// Handle deletion with finalizer
	if !cloudTask.ObjectMeta.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(cloudTask, cloudTaskFinalizerKey) {
			log.Infof("Deleting CloudTask")
			if err := r.deleteExternalResources(ctx, cloudTask); err != nil {
				log.Errorf("Failed to delete external resources: %v", err)
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(cloudTask, cloudTaskFinalizerKey)
			if err := r.Update(ctx, cloudTask); err != nil {
				log.Errorf("Failed to remove finalizer: %v", err)
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(cloudTask, cloudTaskFinalizerKey) {
		controllerutil.AddFinalizer(cloudTask, cloudTaskFinalizerKey)
		if err := r.Update(ctx, cloudTask); err != nil {
			log.Errorf("Failed to add finalizer: %v", err)
			return ctrl.Result{}, err
		}
	}

	// Check for existing Pod
	pod := &corev1.Pod{}
	podName := cloudTask.Status.PodName
	if podName != "" {
		if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: cloudTask.Namespace}, pod); err != nil {
			if !apierrors.IsNotFound(err) {
				log.Errorf("Failed to get Pod: %v", err)
				return ctrl.Result{}, err
			}
			// Pod was deleted, check if we should retry
			log.Warnf("Pod %s not found", podName)
			pod = nil
		}
	}

	// Update CloudTask status based on Pod status
	if pod != nil && pod.Name != "" {
		updatedStatus := r.updateStatusFromPod(ctx, cloudTask, pod)
		if updatedStatus {
			log.Debugf("Updating CloudTask status, phase=%s", cloudTask.Status.Phase)
			if err := r.Status().Update(ctx, cloudTask); err != nil {
				log.Errorf("Failed to update CloudTask status: %v", err)
				return ctrl.Result{}, err
			}
		}

		// Handle completion or failure
		switch cloudTask.Status.Phase {
		case tasksv1.PhaseCompleted:
			log.Infof("CloudTask completed successfully")
			r.Recorder.Event(cloudTask, corev1.EventTypeNormal, "Completed", "Task completed successfully")
			return ctrl.Result{}, nil

		case tasksv1.PhaseFailed:
			// Check if we should retry
			if cloudTask.Status.RetryCount < cloudTask.Spec.Retries {
				log.Infof("Retrying CloudTask (attempt %d/%d)", cloudTask.Status.RetryCount+1, cloudTask.Spec.Retries)
				r.Recorder.Event(cloudTask, corev1.EventTypeNormal, "Retrying", fmt.Sprintf("Retrying task (attempt %d/%d)", cloudTask.Status.RetryCount+1, cloudTask.Spec.Retries))

				cloudTask.Status.Phase = tasksv1.PhasePending
				cloudTask.Status.RetryCount++
				cloudTask.Status.PodName = ""
				cloudTask.Status.Message = fmt.Sprintf("Retrying (attempt %d/%d)", cloudTask.Status.RetryCount, cloudTask.Spec.Retries)

				if err := r.Status().Update(ctx, cloudTask); err != nil {
					log.Errorf("Failed to update CloudTask status for retry: %v", err)
					return ctrl.Result{}, err
				}
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}

			log.Errorf("CloudTask failed after %d retries", cloudTask.Spec.Retries)
			r.Recorder.Event(cloudTask, corev1.EventTypeWarning, "Failed", "Task failed after retries exhausted")
			return ctrl.Result{}, nil

		case tasksv1.PhaseRunning:
			log.Debugf("CloudTask is running")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	// If no Pod exists and CloudTask is Pending, create one
	if pod == nil || pod.Name == "" {
		if cloudTask.Status.Phase == "" || cloudTask.Status.Phase == tasksv1.PhasePending {
			log.Infof("Creating Pod for CloudTask")
			newPod := r.constructPod(cloudTask)
			if err := controllerutil.SetControllerReference(cloudTask, newPod, r.Scheme); err != nil {
				log.Errorf("Failed to set controller reference: %v", err)
				return ctrl.Result{}, err
			}
			if err := r.Create(ctx, newPod); err != nil {
				log.Errorf("Failed to create Pod: %v", err)
				r.Recorder.Event(cloudTask, corev1.EventTypeWarning, "PodCreationFailed", fmt.Sprintf("Failed to create Pod: %v", err))
				return ctrl.Result{RequeueAfter: 10 * time.Second}, err
			}

			log.Infof("Created Pod %s", newPod.Name)
			r.Recorder.Event(cloudTask, corev1.EventTypeNormal, "PodCreated", fmt.Sprintf("Created Pod %s", newPod.Name))

			// Update status
			now := metav1.Now()
			cloudTask.Status.Phase = tasksv1.PhaseRunning
			cloudTask.Status.PodName = newPod.Name
			cloudTask.Status.StartTime = &now
			cloudTask.Status.Message = "Pod created and running"
			if err := r.Status().Update(ctx, cloudTask); err != nil {
				log.Errorf("Failed to update CloudTask status: %v", err)
				return ctrl.Result{}, err
			}

			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// updateStatusFromPod updates the CloudTask status based on the Pod status
func (r *CloudTaskReconciler) updateStatusFromPod(ctx context.Context, cloudTask *tasksv1.CloudTask, pod *corev1.Pod) bool {
	log := r.Log.WithFields(logrus.Fields{
		"cloudtask": client.ObjectKeyFromObject(cloudTask),
		"pod":       client.ObjectKeyFromObject(pod),
	})

	updated := false
	oldPhase := cloudTask.Status.Phase

	// Update status based on Pod phase
	switch pod.Status.Phase {
	case corev1.PodRunning:
		if cloudTask.Status.Phase != tasksv1.PhaseRunning {
			cloudTask.Status.Phase = tasksv1.PhaseRunning
			cloudTask.Status.Message = "Pod is running"
			updated = true
		}

	case corev1.PodSucceeded:
		if cloudTask.Status.Phase != tasksv1.PhaseCompleted {
			now := metav1.Now()
			cloudTask.Status.Phase = tasksv1.PhaseCompleted
			cloudTask.Status.CompletionTime = &now
			cloudTask.Status.Message = "Pod succeeded"
			updated = true
		}

	case corev1.PodFailed:
		if cloudTask.Status.Phase != tasksv1.PhaseFailed {
			now := metav1.Now()
			cloudTask.Status.Phase = tasksv1.PhaseFailed
			cloudTask.Status.CompletionTime = &now
			// Get failure reason from Pod
			reason := "Unknown"
			if pod.Status.Reason != "" {
				reason = pod.Status.Reason
			}
			if len(pod.Status.ContainerStatuses) > 0 && pod.Status.ContainerStatuses[0].State.Terminated != nil {
				reason = pod.Status.ContainerStatuses[0].State.Terminated.Reason
			}
			cloudTask.Status.Message = fmt.Sprintf("Pod failed: %s", reason)
			updated = true
		}

	case corev1.PodPending:
		if cloudTask.Status.Phase != tasksv1.PhaseRunning && cloudTask.Status.Phase != tasksv1.PhasePending {
			cloudTask.Status.Phase = tasksv1.PhasePending
			cloudTask.Status.Message = "Pod is pending"
			updated = true
		}
	}

	if updated {
		now := metav1.Now()
		cloudTask.Status.LastUpdateTime = &now
		log.Debugf("Status updated from %s to %s", oldPhase, cloudTask.Status.Phase)
	}

	return updated
}

// constructPod creates a Pod specification from a CloudTask
func (r *CloudTaskReconciler) constructPod(cloudTask *tasksv1.CloudTask) *corev1.Pod {
	podName := fmt.Sprintf("%s-pod", cloudTask.Name)

	// Build labels
	labels := map[string]string{
		podNameLabel:     cloudTask.Name,
		podTenantLabel:   cloudTask.Spec.TenantID,
		podPriorityLabel: strconv.Itoa(int(cloudTask.Spec.Priority)),
	}
	if cloudTask.Spec.Labels != nil {
		for k, v := range cloudTask.Spec.Labels {
			labels[k] = v
		}
	}

	// Build annotations
	annotations := map[string]string{}
	if cloudTask.Spec.Annotations != nil {
		for k, v := range cloudTask.Spec.Annotations {
			annotations[k] = v
		}
	}

	// Parse timeout
	defaultTTL := int64(300) // 5 minutes default
	if cloudTask.Spec.Timeout != "" {
		if d, err := time.ParseDuration(cloudTask.Spec.Timeout); err == nil {
			defaultTTL = int64(d.Seconds())
		}
	}

	// Build container
	container := corev1.Container{
		Name:  "task-container",
		Image: cloudTask.Spec.Image,
	}

	if len(cloudTask.Spec.Command) > 0 {
		container.Command = cloudTask.Spec.Command
	}
	if len(cloudTask.Spec.Args) > 0 {
		container.Args = cloudTask.Spec.Args
	}

	// Set resources
	if cloudTask.Spec.Resources != nil {
		resources := corev1.ResourceRequirements{}
		if cloudTask.Spec.Resources.Requests != nil {
			requests := corev1.ResourceList{}
			if cloudTask.Spec.Resources.Requests.CPU != "" {
				requests[corev1.ResourceCPU] = utils.ParseQuantity(cloudTask.Spec.Resources.Requests.CPU)
			}
			if cloudTask.Spec.Resources.Requests.Memory != "" {
				requests[corev1.ResourceMemory] = utils.ParseQuantity(cloudTask.Spec.Resources.Requests.Memory)
			}
			resources.Requests = requests
		}
		if cloudTask.Spec.Resources.Limits != nil {
			limits := corev1.ResourceList{}
			if cloudTask.Spec.Resources.Limits.CPU != "" {
				limits[corev1.ResourceCPU] = utils.ParseQuantity(cloudTask.Spec.Resources.Limits.CPU)
			}
			if cloudTask.Spec.Resources.Limits.Memory != "" {
				limits[corev1.ResourceMemory] = utils.ParseQuantity(cloudTask.Spec.Resources.Limits.Memory)
			}
			resources.Limits = limits
		}
		container.Resources = resources
	}

	// Set environment variables
	if len(cloudTask.Spec.Env) > 0 {
		container.Env = cloudTask.Spec.Env
	}

	// Set volume mounts
	if len(cloudTask.Spec.VolumeMounts) > 0 {
		container.VolumeMounts = cloudTask.Spec.VolumeMounts
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   cloudTask.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			Containers:                   []corev1.Container{container},
			ActiveDeadlineSeconds:        &defaultTTL,
			TerminationGracePeriodSeconds: pointer(int64(30)),
		},
	}

	return pod
}

// deleteExternalResources deletes any external resources associated with the CloudTask
func (r *CloudTaskReconciler) deleteExternalResources(ctx context.Context, cloudTask *tasksv1.CloudTask) error {
	// Delete the associated Pod if it exists
	if cloudTask.Status.PodName != "" {
		pod := &corev1.Pod{}
		err := r.Get(ctx, types.NamespacedName{Name: cloudTask.Status.PodName, Namespace: cloudTask.Namespace}, pod)
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		if err == nil {
			if err := r.Delete(ctx, pod, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CloudTaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Set up the EventRecorder
	r.Recorder = mgr.GetEventRecorderFor("cloudtask-controller")

	// Set up indexing for Pods owned by CloudTasks
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, cloudTaskOwnerKey, func(rawObj client.Object) []string {
		pod := rawObj.(*corev1.Pod)
		owner := metav1.GetControllerOf(pod)
		if owner == nil {
			return nil
		}
		if owner.APIVersion != apiGVStr || owner.Kind != "CloudTask" {
			return nil
		}
		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&tasksv1.CloudTask{}).
		Owns(&corev1.Pod{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}

func pointer(i int64) *int64 {
	return &i
}
