package controller

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	schedulerv1 "github.com/ppurva1711-creator/cloudos-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("TaskJob Controller", func() {
	Context("When reconciling a resource", func() {
		It("should successfully reconcile the resource", func() {
			ctx := context.Background()
			taskJob := &schedulerv1.TaskJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-resource",
					Namespace: "default",
				},
				Spec: schedulerv1.TaskJobSpec{
					Name:          "test-resource",
					Image:         "busybox:latest",
					Command:       []string{"/bin/sh"},
					Args:          []string{"-c", "echo test"},
					Priority:      schedulerv1.PriorityLow,
					Algorithm:     schedulerv1.AlgoFIFO,
					CPURequest:    "100m",
					MemoryRequest: "64Mi",
				},
			}
			Expect(k8sClient.Create(ctx, taskJob)).To(Succeed())

			namespacedName := types.NamespacedName{
				Name:      "test-resource",
				Namespace: "default",
			}
			reconciler := &TaskJobReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcileRequest(namespacedName))
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Delete(ctx, taskJob)).To(Succeed())
		})
	})
})
