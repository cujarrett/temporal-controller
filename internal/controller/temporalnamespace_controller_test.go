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

// testNamespace is the Kubernetes namespace every test object lives in.
const testNamespace = "default"

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

// fakeCloud works like Temporal Cloud: creates and deletes finish later, when settle runs.
type fakeCloud struct {
	namespaces map[string]temporal.Namespace
	deleting   map[string]bool
	updates    int
}

func (f *fakeCloud) Describe(_ context.Context, name string) (*temporal.Namespace, error) {
	ns, ok := f.namespaces[name]
	if !ok {
		return nil, nil
	}
	return &ns, nil
}

func (f *fakeCloud) Register(_ context.Context, ns temporal.Namespace) error {
	ns.FullName = ns.Name + ".acct1"
	ns.Endpoint = ns.FullName + ".tmprl.cloud:7233"
	ns.Pending = true
	f.namespaces[ns.Name] = ns
	return nil
}

func (f *fakeCloud) Update(_ context.Context, ns temporal.Namespace) error {
	f.updates++
	got := f.namespaces[ns.Name]
	got.Retention = ns.Retention
	got.ClientNamespace = ns.ClientNamespace
	got.ClientCAStale = false
	f.namespaces[ns.Name] = got
	return nil
}

func (f *fakeCloud) Delete(_ context.Context, name string) error {
	got := f.namespaces[name]
	got.Pending = true
	f.namespaces[name] = got
	f.deleting[name] = true
	return nil
}

// settle finishes every create and delete in progress.
func (f *fakeCloud) settle() {
	for name, ns := range f.namespaces {
		if f.deleting[name] {
			delete(f.namespaces, name)
			continue
		}
		ns.Pending = false
		f.namespaces[name] = ns
	}
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
			Namespace: testNamespace,
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

	Context("on public-cloud", func() {
		var cloud *fakeCloud

		BeforeEach(func() {
			cloud = &fakeCloud{namespaces: map[string]temporal.Namespace{}, deleting: map[string]bool{}}
			r.PublicCloud = cloud
		})

		createCloud := func(name string, retention time.Duration) types.NamespacedName {
			obj := &temporalv1alpha1.TemporalNamespace{
				Name:      name,
				Namespace: testNamespace,
				Spec: temporalv1alpha1.TemporalNamespaceSpec{
					Retention: metav1.Duration{Duration: retention},
					Backend:   "public-cloud",
				},
			}
			Expect(k8sClient.Create(ctx, obj)).To(Succeed())
			return client.ObjectKeyFromObject(obj)
		}

		// removeCloud deletes the object and lets Temporal Cloud finish, so no test leaves one behind.
		removeCloud := func(key types.NamespacedName) {
			var obj temporalv1alpha1.TemporalNamespace
			Expect(k8sClient.Get(ctx, key, &obj)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &obj)).To(Succeed())
			reconcile(key)
			cloud.settle()
			reconcile(key)
			err := k8sClient.Get(ctx, key, &obj)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "object should be gone, got %v", err)
		}

		It("refuses a description, which holds the owner record", func() {
			obj := &temporalv1alpha1.TemporalNamespace{
				Name:      "described",
				Namespace: testNamespace,
				Spec: temporalv1alpha1.TemporalNamespaceSpec{
					Retention:   metav1.Duration{Duration: 24 * time.Hour},
					Backend:     "public-cloud",
					Description: "foo",
				},
			}
			err := k8sClient.Create(ctx, obj)
			Expect(err).To(MatchError(ContainSubstring("description is not supported when backend is public-cloud")))
		})

		It("refuses a change of backend", func() {
			key := createCloud("moving", 24*time.Hour)
			DeferCleanup(remove, key)
			var obj temporalv1alpha1.TemporalNamespace
			Expect(k8sClient.Get(ctx, key, &obj)).To(Succeed())
			obj.Spec.Backend = "private-cloud"
			err := k8sClient.Update(ctx, &obj)
			Expect(err).To(MatchError(ContainSubstring("backend can't be changed after creation")))
		})

		It("is turned off when the controller has no Temporal Cloud key", func() {
			r.PublicCloud = nil
			key := createCloud("off", 24*time.Hour)
			DeferCleanup(remove, key)

			reconcile(key)

			Expect(ready(key).Status).To(Equal(metav1.ConditionFalse))
			Expect(ready(key).Reason).To(Equal("PublicCloudOff"))
		})

		It("reports Provisioning, then Ready with where workers connect", func() {
			key := createCloud("invoices", 24*time.Hour)
			DeferCleanup(removeCloud, key)

			result := reconcile(key)
			Expect(result.RequeueAfter).To(Equal(pendingPeriod))
			Expect(ready(key).Reason).To(Equal("Provisioning"))
			Expect(cloud.namespaces["invoices"].Owner).To(Equal("default/invoices"))
			Expect(cloud.namespaces["invoices"].ClientNamespace).To(Equal(testNamespace))

			cloud.settle()
			reconcile(key)

			Expect(ready(key).Status).To(Equal(metav1.ConditionTrue))
			var obj temporalv1alpha1.TemporalNamespace
			Expect(k8sClient.Get(ctx, key, &obj)).To(Succeed())
			Expect(obj.Status.PublicCloud).To(Equal(&temporalv1alpha1.PublicCloudStatus{
				Namespace: "invoices.acct1",
				Endpoint:  "invoices.acct1.tmprl.cloud:7233",
			}))
		})

		It("counts retention in whole days, so a resync does not update again", func() {
			key := createCloud("refunds", 36*time.Hour)
			DeferCleanup(removeCloud, key)
			reconcile(key)
			cloud.settle()
			reconcile(key)

			Expect(cloud.namespaces["refunds"].Retention).To(Equal(48 * time.Hour))
			Expect(cloud.updates).To(Equal(0))
		})

		It("puts back the client CA when the SPIRE bundle rotates", func() {
			key := createCloud("ledger", 24*time.Hour)
			DeferCleanup(removeCloud, key)
			reconcile(key)
			cloud.settle()

			stale := cloud.namespaces["ledger"]
			stale.ClientCAStale = true
			cloud.namespaces["ledger"] = stale
			reconcile(key)

			Expect(cloud.updates).To(Equal(1))
			Expect(cloud.namespaces["ledger"].ClientCAStale).To(BeFalse())
		})

		It("keeps the finalizer until Temporal Cloud finishes deleting", func() {
			key := createCloud("audit", 24*time.Hour)
			reconcile(key)
			cloud.settle()
			reconcile(key)

			var obj temporalv1alpha1.TemporalNamespace
			Expect(k8sClient.Get(ctx, key, &obj)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &obj)).To(Succeed())
			result := reconcile(key)

			Expect(result.RequeueAfter).To(Equal(pendingPeriod))
			Expect(k8sClient.Get(ctx, key, &obj)).To(Succeed(), "still deleting in Temporal Cloud")

			cloud.settle()
			reconcile(key)

			err := k8sClient.Get(ctx, key, &obj)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "object should be gone, got %v", err)
		})
	})
})
