package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	temporalv1alpha1 "github.com/cujarrett/temporal-controller/api/v1alpha1"
)

// TemporalNamespaceReconciler makes a Temporal namespace match each TemporalNamespace.
type TemporalNamespaceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=temporal.local.lab,resources=temporalnamespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=temporal.local.lab,resources=temporalnamespaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=temporal.local.lab,resources=temporalnamespaces/finalizers,verbs=update

func (r *TemporalNamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx)

	return ctrl.Result{}, nil
}

func (r *TemporalNamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&temporalv1alpha1.TemporalNamespace{}).
		Named("temporalnamespace").
		Complete(r)
}
