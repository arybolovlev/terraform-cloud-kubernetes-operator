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
	requeueInterval    = 30 * time.Second
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

	if needToAddFinalizer(instance) {
		err := r.addFinalizer(ctx, instance)
		if err != nil {
			r.log.Error(err, "add finalizer")
			return r.requeueOnErr(err)
		}
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

	// WORKSPACE RECONCILE LOGIC STARTS HERE
	workspace, err := r.reconcileWorkspace(ctx, instance)
	if err != nil {
		r.log.Error(err, "cannot reconcile workspace")
		return r.requeueAfter(requeueInterval)
	}

	// UPDATE OBJECT STATUS LOGIC STARTS HERE
	status := instance.Status
	status.WorkspaceID = workspace.ID

	return r.doNotRequeue()
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

func needToAddFinalizer(instance *appv1alpha2.Workspace) bool {
	return instance.ObjectMeta.DeletionTimestamp.IsZero() && !controllerutil.ContainsFinalizer(instance, workspaceFinalizer)
}

func (r *WorkspaceReconciler) removeWorkspace(ctx context.Context, instance *appv1alpha2.Workspace) error {
	if instance.Status.WorkspaceID == "" {
		return nil
	}
	err := r.tfClient.Client.Workspaces.DeleteByID(ctx, instance.Status.WorkspaceID)
	if err == tfc.ErrResourceNotFound {
		return nil
	}
	return err
}

func (r *WorkspaceReconciler) removeFinalizer(ctx context.Context, instance *appv1alpha2.Workspace) error {
	controllerutil.RemoveFinalizer(instance, workspaceFinalizer)
	return r.Update(ctx, instance)
}

func (r *WorkspaceReconciler) addFinalizer(ctx context.Context, instance *appv1alpha2.Workspace) error {
	controllerutil.AddFinalizer(instance, workspaceFinalizer)
	return r.Update(ctx, instance)
}

func (t *TerraformCloudClient) createWorkspace(ctx context.Context, instance *appv1alpha2.Workspace) (*tfc.Workspace, error) {
	spec := instance.Spec
	options := tfc.WorkspaceCreateOptions{
		Name: tfc.String(spec.Name),
	}
	return t.Client.Workspaces.Create(ctx, spec.Organization, options)
}

func (t *TerraformCloudClient) getWorkspace(ctx context.Context, instance *appv1alpha2.Workspace) (*tfc.Workspace, error) {
	return t.Client.Workspaces.ReadByID(ctx, instance.Status.WorkspaceID)
}

func (r *WorkspaceReconciler) reconcileWorkspace(ctx context.Context, instance *appv1alpha2.Workspace) (*tfc.Workspace, error) {
	var workspace *tfc.Workspace
	var err error

	// create a new workspace if workspace ID is unknown
	if instance.Status.WorkspaceID == "" {
		r.log.Info("Reconcile Workspace", "msg", "workspace ID is empty, creating a new workspace")
		workspace, err = r.tfClient.createWorkspace(ctx, instance)
		if err != nil {
			return workspace, err
		}
		instance.Status.WorkspaceID = workspace.ID
		r.Status().Update(ctx, instance)
	}

	// verify whether the workspace exists
	workspace, err = r.tfClient.getWorkspace(ctx, instance)
	if err != nil {
		if err == tfc.ErrResourceNotFound {
			r.log.Info("Reconcile Workspace", "msg", "workspace is not found, creating a new workspace")
			workspace, err = r.tfClient.createWorkspace(ctx, instance)
			if err != nil {
				return workspace, err
			}
			instance.Status.WorkspaceID = workspace.ID
			r.Status().Update(ctx, instance)
		}
	}
	if err != nil {
		return workspace, err
	}

	return workspace, err
}
