package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	temporalv1alpha1 "github.com/cujarrett/temporal-controller/api/v1alpha1"
	"github.com/cujarrett/temporal-controller/internal/temporal"
)

const (
	finalizer = "temporal.local.lab/namespace"

	// Temporal sends no events, so a change made with the temporal CLI only shows up on this timer.
	resyncPeriod = 5 * time.Minute

	// Temporal Cloud creates and deletes in the background, in about a minute.
	pendingPeriod = 15 * time.Second

	publicCloud = "public-cloud"
)

// Temporal is the part of the Temporal API the reconciler uses. Tests swap in a fake.
type Temporal interface {
	Describe(ctx context.Context, name string) (*temporal.Namespace, error)
	Register(ctx context.Context, ns temporal.Namespace) error
	Update(ctx context.Context, ns temporal.Namespace) error
	Delete(ctx context.Context, name string) error
}

// TemporalNamespaceReconciler makes a Temporal namespace match each TemporalNamespace.
type TemporalNamespaceReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Temporal Temporal
	// PublicCloud is Temporal Cloud. Nil when the controller has no Temporal Cloud key.
	PublicCloud Temporal
	Recorder    events.EventRecorder
}

// +kubebuilder:rbac:groups=temporal.local.lab,resources=temporalnamespaces,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=temporal.local.lab,resources=temporalnamespaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=temporal.local.lab,resources=temporalnamespaces/finalizers,verbs=update
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

func (r *TemporalNamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var tns temporalv1alpha1.TemporalNamespace
	if err := r.Get(ctx, req.NamespacedName, &tns); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Temporal namespace names are global across Kubernetes namespaces.
	// The owner record stops two objects with the same name sharing one.
	owner := req.String()

	// Temporal Cloud creates, changes and deletes in the background.
	cloud := tns.Spec.Backend == publicCloud
	backend := r.Temporal
	if cloud {
		if r.PublicCloud == nil {
			msg := "This controller has no Temporal Cloud key, so public-cloud is turned off"
			return ctrl.Result{}, r.setNotReady(ctx, &tns, "PublicCloudOff", msg)
		}
		backend = r.PublicCloud
	}

	if !tns.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, &tns, backend, owner, cloud)
	}
	if controllerutil.AddFinalizer(&tns, finalizer) {
		if err := r.Update(ctx, &tns); err != nil {
			return ctrl.Result{}, err
		}
	}

	want := desired(&tns, owner)
	got, err := backend.Describe(ctx, want.Name)
	if err != nil {
		return ctrl.Result{}, errors.Join(err, r.setNotReady(ctx, &tns, "TemporalError", err.Error()))
	}

	switch {
	case got == nil:
		err = backend.Register(ctx, want)
		if err == nil {
			r.event(&tns, corev1.EventTypeNormal, "Created", "Register", "Created Temporal namespace %q", want.Name)
		}
		if err == nil && cloud {
			return r.provisioning(ctx, &tns, got)
		}
	case got.Owner != owner:
		msg := fmt.Sprintf("Temporal namespace %q belongs to %s", want.Name, ownerName(got.Owner))
		r.event(&tns, corev1.EventTypeWarning, "Conflict", "Describe", "%s", msg)
		return ctrl.Result{RequeueAfter: resyncPeriod}, r.setNotReady(ctx, &tns, "Conflict", msg)
	case got.Pending:
		return r.provisioning(ctx, &tns, got)
	case drifted(*got, want):
		err = backend.Update(ctx, want)
		if err == nil {
			r.event(&tns, corev1.EventTypeNormal, "Updated", "Update", "Set Temporal namespace %q to match spec", want.Name)
		}
		if err == nil && cloud {
			return r.provisioning(ctx, &tns, got)
		}
	}
	if err != nil {
		return ctrl.Result{}, errors.Join(err, r.setNotReady(ctx, &tns, "TemporalError", err.Error()))
	}

	if err := r.setStatus(ctx, &tns, cloudStatus(got), metav1.ConditionTrue, "Synced", "Temporal namespace matches spec"); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: resyncPeriod}, nil
}

// finalize deletes the Temporal namespace, then lets Kubernetes remove the object.
func (r *TemporalNamespaceReconciler) finalize(ctx context.Context, tns *temporalv1alpha1.TemporalNamespace, backend Temporal, owner string, cloud bool) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(tns, finalizer) {
		return ctrl.Result{}, nil
	}
	got, err := backend.Describe(ctx, tns.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	// An object in Conflict never owned the namespace, so it must leave it alone.
	if got != nil && got.Owner == owner {
		if err := backend.Delete(ctx, tns.Name); err != nil {
			return ctrl.Result{}, err
		}
		r.event(tns, corev1.EventTypeNormal, "Deleted", "Delete", "Deleted Temporal namespace %q", tns.Name)
		// The finalizer stays until Temporal Cloud is done,
		// so a namespace that still costs money never loses its owner.
		if cloud {
			return ctrl.Result{RequeueAfter: pendingPeriod}, nil
		}
	}
	controllerutil.RemoveFinalizer(tns, finalizer)
	return ctrl.Result{}, r.Update(ctx, tns)
}

// desired is the namespace the spec asks for.
func desired(tns *temporalv1alpha1.TemporalNamespace, owner string) temporal.Namespace {
	want := temporal.Namespace{
		Name:        tns.Name,
		Description: tns.Spec.Description,
		Retention:   tns.Spec.Retention.Duration,
		Owner:       owner,
	}
	if tns.Spec.Backend != publicCloud {
		return want
	}
	// Temporal Cloud keeps the owner in the description and counts retention in days.
	want.Description = ""
	want.Retention = temporal.CloudRetention(want.Retention)
	// Only workloads in this object's own namespace may connect.
	want.ClientNamespace = tns.Namespace
	return want
}

func cloudStatus(got *temporal.Namespace) *temporalv1alpha1.PublicCloudStatus {
	if got == nil || got.FullName == "" {
		return nil
	}
	return &temporalv1alpha1.PublicCloudStatus{Namespace: got.FullName, Endpoint: got.Endpoint}
}

// provisioning reports Ready=False while Temporal Cloud works, and checks again soon.
func (r *TemporalNamespaceReconciler) provisioning(ctx context.Context, tns *temporalv1alpha1.TemporalNamespace, got *temporal.Namespace) (ctrl.Result, error) {
	msg := "Temporal Cloud is applying a change to the namespace"
	return ctrl.Result{RequeueAfter: pendingPeriod}, r.setStatus(ctx, tns, cloudStatus(got), metav1.ConditionFalse, "Provisioning", msg)
}

// setNotReady writes Ready=False.
func (r *TemporalNamespaceReconciler) setNotReady(ctx context.Context, tns *temporalv1alpha1.TemporalNamespace, reason, message string) error {
	return r.setStatus(ctx, tns, tns.Status.PublicCloud, metav1.ConditionFalse, reason, message)
}

// setStatus writes the Ready condition and where workers reach a Temporal Cloud namespace.
// It skips the write when nothing changed, so a resync that finds no drift costs no API write.
func (r *TemporalNamespaceReconciler) setStatus(ctx context.Context, tns *temporalv1alpha1.TemporalNamespace, cloud *temporalv1alpha1.PublicCloudStatus, status metav1.ConditionStatus, reason, message string) error {
	changed := meta.SetStatusCondition(&tns.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: tns.Generation,
	})
	if !equality.Semantic.DeepEqual(tns.Status.PublicCloud, cloud) {
		tns.Status.PublicCloud = cloud
		changed = true
	}
	if !changed && tns.Status.ObservedGeneration == tns.Generation {
		return nil
	}
	tns.Status.ObservedGeneration = tns.Generation
	return r.Status().Update(ctx, tns)
}

func (r *TemporalNamespaceReconciler) event(tns *temporalv1alpha1.TemporalNamespace, eventType, reason, action, note string, args ...any) {
	if r.Recorder != nil {
		r.Recorder.Eventf(tns, nil, eventType, reason, action, note, args...)
	}
}

// drifted reports whether Temporal differs from the spec.
// An empty spec description is ignored. Temporal's update skips empty fields, so it could never clear one.
func drifted(got, want temporal.Namespace) bool {
	if got.Retention != want.Retention || got.ClientNamespace != want.ClientNamespace || got.ClientCAStale {
		return true
	}
	return want.Description != "" && got.Description != want.Description
}

func ownerName(owner string) string {
	if owner == "" {
		return "something outside temporal-controller"
	}
	return owner
}

func (r *TemporalNamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&temporalv1alpha1.TemporalNamespace{}).
		Named("temporalnamespace").
		Complete(r)
}
