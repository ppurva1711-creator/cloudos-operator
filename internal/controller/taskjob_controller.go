package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	schedulerv1 "github.com/ppurva1711-creator/cloudos-operator/api/v1"
)

type TaskJobReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=scheduler.cloudos.io,resources=taskjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=scheduler.cloudos.io,resources=taskjobs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete

func (r *TaskJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var taskJob schedulerv1.TaskJob
	if err := r.Get(ctx, req.NamespacedName, &taskJob); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("Reconciling TaskJob", "name", taskJob.Name, "phase", taskJob.Status.Phase)

	if taskJob.Status.Phase == schedulerv1.PhaseCompleted ||
		taskJob.Status.Phase == schedulerv1.PhaseFailed {
		return ctrl.Result{}, nil
	}

	depsOk, err := r.checkDependencies(ctx, &taskJob)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !depsOk {
		taskJob.Status.Phase = schedulerv1.PhaseQueued
		taskJob.Status.Message = "Waiting for dependencies"
		if err := r.Status().Update(ctx, &taskJob); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	existingPod, err := r.findPod(ctx, &taskJob)
	if err != nil {
		return ctrl.Result{}, err
	}

	if existingPod == nil {
		if taskJob.Spec.Image == "" {
			taskJob.Status.Phase = schedulerv1.PhaseFailed
			taskJob.Status.Message = "No image specified"
			if err := r.Status().Update(ctx, &taskJob); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}

		pod := r.buildPod(&taskJob)
		if err := r.Create(ctx, pod); err != nil {
			logger.Error(err, "Failed to create Pod")
			taskJob.Status.Phase = schedulerv1.PhaseFailed
			taskJob.Status.Message = fmt.Sprintf("Pod creation failed: %v", err)
			if err := r.Status().Update(ctx, &taskJob); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
		logger.Info("Created pod", "pod", pod.Name)
		taskJob.Status.Phase = schedulerv1.PhaseRunning
		taskJob.Status.PodName = pod.Name
		if err := r.Status().Update(ctx, &taskJob); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	return r.syncStatus(ctx, &taskJob, existingPod)
}

func (r *TaskJobReconciler) checkDependencies(ctx context.Context, tj *schedulerv1.TaskJob) (bool, error) {
	for _, depName := range tj.Spec.DependsOn {
		var dep schedulerv1.TaskJob
		key := client.ObjectKey{Name: depName, Namespace: tj.Namespace}
		if err := r.Get(ctx, key, &dep); err != nil {
			return false, err
		}
		if dep.Status.Phase != schedulerv1.PhaseCompleted {
			return false, nil
		}
	}
	return true, nil
}

func (r *TaskJobReconciler) findPod(ctx context.Context, tj *schedulerv1.TaskJob) (*corev1.Pod, error) {
	var podList corev1.PodList
	if err := r.List(ctx, &podList,
		client.InNamespace(tj.Namespace),
		client.MatchingLabels(map[string]string{
			"cloudos.io/task-id": tj.Name,
		}),
	); err != nil {
		return nil, err
	}
	if len(podList.Items) == 0 {
		return nil, nil
	}
	return &podList.Items[0], nil
}

func (r *TaskJobReconciler) buildPod(tj *schedulerv1.TaskJob) *corev1.Pod {
	cpuReq := tj.Spec.CPURequest
	if cpuReq == "" {
		cpuReq = "500m"
	}
	memReq := tj.Spec.MemoryRequest
	if memReq == "" {
		memReq = "256Mi"
	}

	envVars := make([]corev1.EnvVar, 0, len(tj.Spec.Env)+5)
	envVars = append(envVars,
		corev1.EnvVar{Name: "CLOUDOS_TASK_ID",   Value: tj.Name},
		corev1.EnvVar{Name: "CLOUDOS_NAMESPACE", Value: tj.Namespace},
		corev1.EnvVar{Name: "CLOUDOS_PRIORITY",  Value: string(tj.Spec.Priority)},
		corev1.EnvVar{Name: "CLOUDOS_ALGORITHM", Value: string(tj.Spec.Algorithm)},
		corev1.EnvVar{Name: "REDIS_URL",         Value: "redis://redis-svc:6379"},
	)
	for _, e := range tj.Spec.Env {
		envVars = append(envVars, corev1.EnvVar{Name: e.Name, Value: e.Value})
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("taskjob-%s", tj.Name),
			Namespace: tj.Namespace,
			Labels: map[string]string{
				"cloudos.io/task-id":  tj.Name,
				"cloudos.io/priority": string(tj.Spec.Priority),
				"app":                 "cloudos-worker",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:            "task-runner",
				Image:           tj.Spec.Image,
				Command:         tj.Spec.Command,
				Args:            tj.Spec.Args,
				Env:             envVars,
				ImagePullPolicy: corev1.PullIfNotPresent,
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(cpuReq),
						corev1.ResourceMemory: resource.MustParse(memReq),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(cpuReq),
						corev1.ResourceMemory: resource.MustParse(memReq),
					},
				},
			}},
		},
	}

	if err := controllerutil.SetControllerReference(tj, pod, r.Scheme); err != nil {
		return pod
	}
	return pod
}

func (r *TaskJobReconciler) syncStatus(ctx context.Context, tj *schedulerv1.TaskJob, pod *corev1.Pod) (ctrl.Result, error) {
	switch pod.Status.Phase {
	case corev1.PodRunning:
		tj.Status.Phase = schedulerv1.PhaseRunning
		tj.Status.Message = "Pod is running"
		if tj.Status.StartTime == "" {
			tj.Status.StartTime = time.Now().Format(time.RFC3339)
		}
		if err := r.Status().Update(ctx, tj); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil

	case corev1.PodSucceeded:
		tj.Status.Phase = schedulerv1.PhaseCompleted
		tj.Status.EndTime = time.Now().Format(time.RFC3339)
		tj.Status.Message = "Task completed successfully"
		tj.Status.ExitCode = 0
		if err := r.Status().Update(ctx, tj); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil

	case corev1.PodFailed:
		tj.Status.Phase = schedulerv1.PhaseFailed
		tj.Status.EndTime = time.Now().Format(time.RFC3339)
		tj.Status.Message = "Pod failed"
		if err := r.Status().Update(ctx, tj); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil

	case corev1.PodPending:
		tj.Status.Phase = schedulerv1.PhaseQueued
		tj.Status.Message = "Pod pending scheduling"
		if err := r.Status().Update(ctx, tj); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *TaskJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&schedulerv1.TaskJob{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
