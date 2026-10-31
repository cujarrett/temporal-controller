package controller

import (
	"context"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	temporalv1alpha1 "github.com/cujarrett/temporal-controller/api/v1alpha1"
	"github.com/cujarrett/temporal-controller/internal/temporal"
)

// Runs the real manager against a real Temporal server.
// TEMPORAL_ADDRESS: set it, such as localhost:7233 for temporal server start-dev, or this skips.
var _ = Describe("TemporalNamespace controller against Temporal", func() {
	It("creates the namespace, then deletes it with the object", func() {
		address := os.Getenv("TEMPORAL_ADDRESS")
		if address == "" {
			Skip("TEMPORAL_ADDRESS not set")
		}

		tc, err := temporal.New(address, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(tc.Close)

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:                 k8sClient.Scheme(),
			Metrics:                metricsserver.Options{BindAddress: "0"},
			HealthProbeBindAddress: "0",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect((&TemporalNamespaceReconciler{
			Client:   mgr.GetClient(),
			Scheme:   mgr.GetScheme(),
			Temporal: tc,
		}).SetupWithManager(mgr)).To(Succeed())

		mgrCtx, stop := context.WithCancel(context.Background())
		DeferCleanup(stop)
		go func() {
			defer GinkgoRecover()
			Expect(mgr.Start(mgrCtx)).To(Succeed())
		}()

		ctx := context.Background()
		name := fmt.Sprintf("tc-it-%d", time.Now().UnixNano())
		obj := &temporalv1alpha1.TemporalNamespace{
			Name:      name,
			Namespace: "default",
			Spec:      temporalv1alpha1.TemporalNamespaceSpec{Retention: metav1.Duration{Duration: 24 * time.Hour}},
		}
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		key := client.ObjectKeyFromObject(obj)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key, obj)).To(Succeed())
			cond := meta.FindStatusCondition(obj.Status.Conditions, "Ready")
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		}, 30*time.Second, time.Second).Should(Succeed())

		got, err := tc.Describe(ctx, name)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).NotTo(BeNil())
		Expect(got.Owner).To(Equal("default/" + name))
		Expect(got.Retention).To(Equal(24 * time.Hour))

		Expect(k8sClient.Delete(ctx, obj)).To(Succeed())
		Eventually(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, key, obj))
		}, 30*time.Second, time.Second).Should(BeTrue())
		Eventually(func() (*temporal.Namespace, error) {
			return tc.Describe(ctx, name)
		}, 30*time.Second, time.Second).Should(BeNil())
	})
})
