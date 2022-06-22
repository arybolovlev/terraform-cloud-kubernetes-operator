package controllers

import (
	"time"

	appv1alpha2 "github.com/arybolovlev/terraform-cloud-kubernetes-operator/api/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// RETURNS
func (r *WorkspaceReconciler) doNotRequeue() (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

func (r *WorkspaceReconciler) requeueAfter(duration time.Duration) (reconcile.Result, error) {
	return reconcile.Result{Requeue: true, RequeueAfter: duration}, nil
}

func (r *WorkspaceReconciler) requeueOnErr(err error) (reconcile.Result, error) {
	return reconcile.Result{}, err
}

// FINALIZERS
func isDeletionCandidate(instance *appv1alpha2.Workspace) bool {
	return !instance.ObjectMeta.DeletionTimestamp.IsZero() && controllerutil.ContainsFinalizer(instance, workspaceFinalizer)
}

func needToAddFinalizer(instance *appv1alpha2.Workspace) bool {
	return instance.ObjectMeta.DeletionTimestamp.IsZero() && !controllerutil.ContainsFinalizer(instance, workspaceFinalizer)
}
