/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	appv1alpha2 "github.com/arybolovlev/terraform-cloud-kubernetes-operator/api/v1alpha2"
	"github.com/go-logr/logr"

	tfc "github.com/hashicorp/go-tfe"
)

const (
	requeueInterval    = 60 * time.Second
	workspaceFinalizer = "workspace.app.terraform.io/finalizer"
)

type TerraformCloudClient struct {
	Client *tfc.Client
}

// WorkspaceReconciler reconciles a Workspace object
type WorkspaceReconciler struct {
	client.Client
	log      logr.Logger
	Scheme   *runtime.Scheme
	tfClient TerraformCloudClient
}

//+kubebuilder:rbac:groups=app.terraform.io,resources=workspaces,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=app.terraform.io,resources=workspaces/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=app.terraform.io,resources=workspaces/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.1/pkg/reconcile
func (r *WorkspaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.log = log.FromContext(ctx)

	r.log.Info("Reconcile Workspace", "action", "new reconciliation event")

	instance := &appv1alpha2.Workspace{}

	err := r.Client.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		// 'Not found' error occurs when an object is removed from the Kubernetes
		// No actions are required at this state
		if errors.IsNotFound(err) {
			return r.doNotRequeue()
		}
		return r.requeueAfter(requeueInterval)
	}

	token, err := r.getToken(ctx, instance)
	if err != nil {
		r.log.Error(err, "get token")
		return r.requeueAfter(requeueInterval)
	}

	err = r.getClient(token)
	if err != nil {
		return r.requeueAfter(requeueInterval)
	}

	if isDeletionCandidate(instance) {
		err = r.removeWorkspace(ctx, instance)
		if err != nil {
			r.log.Error(err, "remove workspace")
			return r.requeueOnErr(err)
		}

		err = r.removeFinalizer(ctx, instance)
		if err != nil {
			r.log.Error(err, "remove finalizer")
			return r.requeueOnErr(err)
		}

		return r.doNotRequeue()
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkspaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appv1alpha2.Workspace{}).
		Complete(r)
}

func (r *WorkspaceReconciler) doNotRequeue() (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

func (r *WorkspaceReconciler) requeueAfter(duration time.Duration) (reconcile.Result, error) {
	return reconcile.Result{Requeue: true, RequeueAfter: duration}, nil
}

func (r *WorkspaceReconciler) requeueOnErr(err error) (reconcile.Result, error) {
	return reconcile.Result{}, err
}

func (r *WorkspaceReconciler) getSecret(ctx context.Context, instance *appv1alpha2.Workspace) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	err := r.Client.Get(ctx, types.NamespacedName{Namespace: instance.Namespace, Name: instance.Spec.Token.SecretKeyRef.Name}, secret)

	return secret, err
}

func (r *WorkspaceReconciler) getToken(ctx context.Context, instance *appv1alpha2.Workspace) (string, error) {
	var secret *corev1.Secret
	secret, err := r.getSecret(ctx, instance)
	if err != nil {
		return "", err
	}

	return string(secret.Data[instance.Spec.Token.SecretKeyRef.Key]), nil
}

func (r *WorkspaceReconciler) getClient(token string) error {
	config := &tfc.Config{
		Token: token,
	}
	var err error

	r.tfClient.Client, err = tfc.NewClient(config)
	return err
}

func isDeletionCandidate(instance *appv1alpha2.Workspace) bool {
	return !instance.ObjectMeta.DeletionTimestamp.IsZero() && controllerutil.ContainsFinalizer(instance, workspaceFinalizer)
}

func (r *WorkspaceReconciler) removeWorkspace(ctx context.Context, instance *appv1alpha2.Workspace) error {
	return nil
}

func (r *WorkspaceReconciler) removeFinalizer(ctx context.Context, instance *appv1alpha2.Workspace) error {
	controllerutil.RemoveFinalizer(instance, workspaceFinalizer)
	return r.Update(ctx, instance)
}
