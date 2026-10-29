package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	temporalv1alpha1 "github.com/cujarrett/temporal-controller/api/v1alpha1"
)

var _ = Describe("TemporalNamespace Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "test-resource"
			resourceNamespace = "default"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}
		temporalnamespace := &temporalv1alpha1.TemporalNamespace{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind TemporalNamespace")
			err := k8sClient.Get(ctx, typeNamespacedName, temporalnamespace)
			if err != nil && errors.IsNotFound(err) {
				resource := &temporalv1alpha1.TemporalNamespace{
					Name:      resourceName,
					Namespace: resourceNamespace,
					Spec: temporalv1alpha1.TemporalNamespaceSpec{
						Retention: metav1.Duration{Duration: 72 * time.Hour},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &temporalv1alpha1.TemporalNamespace{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance TemporalNamespace")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &TemporalNamespaceReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
