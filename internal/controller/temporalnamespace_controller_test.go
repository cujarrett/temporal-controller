package controller

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	temporalv1alpha1 "github.com/cujarrett/temporal-controller/api/v1alpha1"
	"github.com/cujarrett/temporal-controller/internal/temporal"
)

// fakeTemporal keeps namespaces in memory and behaves like the real update.
type fakeTemporal struct {
	namespaces map[string]temporal.Namespace
	updates    int
}

func (f *fakeTemporal) Describe(_ context.Context, name string) (*temporal.Namespace, error) {
	ns, ok := f.namespaces[name]
	if !ok {
		return nil, nil
	}
	return &ns, nil
}

func (f *fakeTemporal) Register(_ context.Context, ns temporal.Namespace) error {
	if _, ok := f.namespaces[ns.Name]; ok {
		return fmt.Errorf("namespace %q already exists", ns.Name)
	}
	f.namespaces[ns.Name] = ns
	return nil
}

func (f *fakeTemporal) Update(_ context.Context, ns temporal.Namespace) error {
	f.updates++
	got := f.namespaces[ns.Name]
	got.Retention = ns.Retention
	if ns.Description != "" {
		got.Description = ns.Description
	}
	f.namespaces[ns.Name] = got
	return nil
}

func (f *fakeTemporal) Delete(_ context.Context, name string) error {
	delete(f.namespaces, name)
	return nil
}

var _ = Describe("TemporalNamespace controller", func() {
	ctx := context.Background()
	var fake *fakeTemporal
	var r *TemporalNamespaceReconciler

	BeforeEach(func() {
		fake = &fakeTemporal{namespaces: map[string]temporal.Namespace{}}
		r = &TemporalNamespaceReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Temporal: fake}
	})

	create := func(name string) types.NamespacedName {
		obj := &temporalv1alpha1.TemporalNamespace{
			Name:      name,
			Namespace: "default",
			Spec: temporalv1alpha1.TemporalNamespaceSpec{
				Retention:   metav1.Duration{Duration: 72 * time.Hour},
				Description: "Workflows for " + name,
			},
		}
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		return client.ObjectKeyFromObject(obj)
	}

	reconcile := func(key types.NamespacedName) ctrl.Result {
		result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		return result
	}

	ready := func(key types.NamespacedName) *metav1.Condition {
		var obj temporalv1alpha1.TemporalNamespace
		Expect(k8sClient.Get(ctx, key, &obj)).To(Succeed())
		return meta.FindStatusCondition(obj.Status.Conditions, "Ready")
	}

	// remove deletes the object and runs the finalizer, so no test leaves one behind.
	remove := func(key types.NamespacedName) {
		var obj temporalv1alpha1.TemporalNamespace
		Expect(k8sClient.Get(ctx, key, &obj)).To(Succeed())
		Expect(k8sClient.Delete(ctx, &obj)).To(Succeed())
		reconcile(key)
		err := k8sClient.Get(ctx, key, &obj)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "object should be gone, got %v", err)
	}

	It("creates the namespace with an owner and reports Ready", func() {
		key := create("orders")
		DeferCleanup(remove, key)

		result := reconcile(key)

		Expect(fake.namespaces).To(HaveKeyWithValue("orders", temporal.Namespace{
			Name:        "orders",
			Description: "Workflows for orders",
			Retention:   72 * time.Hour,
			Owner:       "default/orders",
		}))
		Expect(result.RequeueAfter).To(Equal(resyncPeriod))
		Expect(ready(key).Status).To(Equal(metav1.ConditionTrue))
		Expect(ready(key).Reason).To(Equal("Synced"))
	})

	It("puts back retention changed outside Kubernetes", func() {
		key := create("billing")
		DeferCleanup(remove, key)
		reconcile(key)

		drifted := fake.namespaces["billing"]
		drifted.Retention = 24 * time.Hour
		fake.namespaces["billing"] = drifted
		reconcile(key)
		reconcile(key)

		Expect(fake.namespaces["billing"].Retention).To(Equal(72 * time.Hour))
		Expect(fake.updates).To(Equal(1), "a resync with no drift should not update")
	})

	It("refuses a namespace another object owns and leaves it on delete", func() {
		theirs := temporal.Namespace{Name: "payments", Retention: 24 * time.Hour, Owner: "team-a/payments"}
		fake.namespaces["payments"] = theirs
		key := create("payments")

		reconcile(key)

		Expect(ready(key).Status).To(Equal(metav1.ConditionFalse))
		Expect(ready(key).Reason).To(Equal("Conflict"))
		Expect(ready(key).Message).To(ContainSubstring("team-a/payments"))
		Expect(fake.namespaces["payments"]).To(Equal(theirs))

		remove(key)
		Expect(fake.namespaces["payments"]).To(Equal(theirs))
	})

	It("deletes the namespace when the object is deleted", func() {
		key := create("shipping")
		reconcile(key)
		Expect(fake.namespaces).To(HaveKey("shipping"))

		remove(key)

		Expect(fake.namespaces).NotTo(HaveKey("shipping"))
	})
})
