package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
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
	Recorder events.EventRecorder
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

	if !tns.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.finalize(ctx, &tns, owner)
	}
	if controllerutil.AddFinalizer(&tns, finalizer) {
		if err := r.Update(ctx, &tns); err != nil {
			return ctrl.Result{}, err
		}
	}

	want := temporal.Namespace{
		Name:        tns.Name,
		Description: tns.Spec.Description,
		Retention:   tns.Spec.Retention.Duration,
		Owner:       owner,
	}
	got, err := r.Temporal.Describe(ctx, want.Name)
	if err != nil {
		return ctrl.Result{}, errors.Join(err, r.setReady(ctx, &tns, metav1.ConditionFalse, "TemporalError", err.Error()))
	}

	switch {
	case got == nil:
		err = r.Temporal.Register(ctx, want)
		if err == nil {
			r.event(&tns, corev1.EventTypeNormal, "Created", "Register", "Created Temporal namespace %q", want.Name)
		}
	case got.Owner != owner:
		msg := fmt.Sprintf("Temporal namespace %q belongs to %s", want.Name, ownerName(got.Owner))
		r.event(&tns, corev1.EventTypeWarning, "Conflict", "Describe", "%s", msg)
		return ctrl.Result{RequeueAfter: resyncPeriod}, r.setReady(ctx, &tns, metav1.ConditionFalse, "Conflict", msg)
	case drifted(*got, want):
		err = r.Temporal.Update(ctx, want)
		if err == nil {
			r.event(&tns, corev1.EventTypeNormal, "Updated", "Update", "Set Temporal namespace %q to match spec", want.Name)
		}
	}
	if err != nil {
		return ctrl.Result{}, errors.Join(err, r.setReady(ctx, &tns, metav1.ConditionFalse, "TemporalError", err.Error()))
	}

	if err := r.setReady(ctx, &tns, metav1.ConditionTrue, "Synced", "Temporal namespace matches spec"); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: resyncPeriod}, nil
}

// finalize deletes the Temporal namespace, then lets Kubernetes remove the object.
func (r *TemporalNamespaceReconciler) finalize(ctx context.Context, tns *temporalv1alpha1.TemporalNamespace, owner string) error {
	if !controllerutil.ContainsFinalizer(tns, finalizer) {
		return nil
	}
	got, err := r.Temporal.Describe(ctx, tns.Name)
	if err != nil {
		return err
	}
	// An object in Conflict never owned the namespace, so it must leave it alone.
	if got != nil && got.Owner == owner {
		if err := r.Temporal.Delete(ctx, tns.Name); err != nil {
			return err
		}
		r.event(tns, corev1.EventTypeNormal, "Deleted", "Delete", "Deleted Temporal namespace %q", tns.Name)
	}
	controllerutil.RemoveFinalizer(tns, finalizer)
	return r.Update(ctx, tns)
}

// setReady writes the Ready condition, and skips the write when nothing changed.
// A resync that finds no drift then costs no API write.
func (r *TemporalNamespaceReconciler) setReady(ctx context.Context, tns *temporalv1alpha1.TemporalNamespace, status metav1.ConditionStatus, reason, message string) error {
	changed := meta.SetStatusCondition(&tns.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: tns.Generation,
	})
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
	if got.Retention != want.Retention {
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
