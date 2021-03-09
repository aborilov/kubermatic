/*
Copyright 2021 The Kubermatic Kubernetes Platform contributors.

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

package mla

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/types"

	"k8c.io/kubermatic/v2/pkg/controller/util/predicate"

	"k8c.io/kubermatic/v2/pkg/kubernetes"

	grafanasdk "github.com/grafana-tools/sdk"
	"go.uber.org/zap"

	kubermaticv1 "k8c.io/kubermatic/v2/pkg/crd/kubermatic/v1"
	"k8c.io/kubermatic/v2/pkg/version/kubermatic"

	"k8s.io/client-go/tools/record"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// userProjectBindingReconciler stores necessary components that are required to manage MLA(Monitoring, Logging, and Alerting) setup.
type userProjectBindingReconciler struct {
	ctrlruntimeclient.Client
	grafanaClient *grafanasdk.Client

	log        *zap.SugaredLogger
	workerName string
	recorder   record.EventRecorder
	versions   kubermatic.Versions
}

// Add creates a new MLA controller that is responsible for
// managing Monitoring, Logging and Alerting for user clusters.
func newUserProjectBindingReconciler(
	mgr manager.Manager,
	log *zap.SugaredLogger,
	numWorkers int,
	workerName string,
	versions kubermatic.Versions,
	grafanaClient *grafanasdk.Client,
) error {
	log = log.Named(ControllerName)
	client := mgr.GetClient()

	reconciler := &userProjectBindingReconciler{
		Client:        client,
		grafanaClient: grafanaClient,

		log:        log,
		workerName: workerName,
		recorder:   mgr.GetEventRecorderFor(ControllerName),
		versions:   versions,
	}

	ctrlOptions := controller.Options{
		Reconciler:              reconciler,
		MaxConcurrentReconciles: numWorkers,
	}
	c, err := controller.New(ControllerName, mgr, ctrlOptions)
	if err != nil {
		return err
	}

	debugPredicate := predicate.ByLabel(kubermaticv1.WorkerNameLabelKey, workerName)

	if err := c.Watch(&source.Kind{Type: &kubermaticv1.UserProjectBinding{}}, &handler.EnqueueRequestForObject{}, debugPredicate); err != nil {
		return fmt.Errorf("failed to watch UserProjectBindings: %v", err)
	}
	return err
}

func (r *userProjectBindingReconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := r.log.With("request", request)
	log.Debug("Processing")

	userProjectBinding := &kubermaticv1.UserProjectBinding{}
	if err := r.Get(ctx, request.NamespacedName, userProjectBinding); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	if !userProjectBinding.DeletionTimestamp.IsZero() {
		if err := r.handleDeletion(ctx, userProjectBinding); err != nil {
			return reconcile.Result{}, fmt.Errorf("handling deletion: %w", err)
		}
		return reconcile.Result{}, nil
	}

	kubernetes.AddFinalizer(userProjectBinding, mlaFinalizer)
	if err := r.Update(ctx, userProjectBinding); err != nil {
		return reconcile.Result{}, fmt.Errorf("updating finalizers: %w", err)
	}

	project := &kubermaticv1.Project{}
	if err := r.Get(ctx, types.NamespacedName{
		Name: userProjectBinding.Spec.ProjectID,
	}, project); err != nil {
		return reconcile.Result{}, fmt.Errorf("faileding to get project: %w", err)
	}

	org, err := r.grafanaClient.GetOrgByOrgName(ctx, getOrgNameForProject(project))
	if err != nil {
		return reconcile.Result{}, err
	}

	globalUser := grafanasdk.User{
		Login:    userProjectBinding.Spec.UserEmail,
		Name:     userProjectBinding.Spec.UserEmail,
		Email:    userProjectBinding.Spec.UserEmail,
		Password: "password",
		OrgID:    org.ID,
	}

	_, err = r.grafanaClient.CreateUser(ctx, globalUser)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to create grafana user: %w", err)
	}

	return reconcile.Result{}, nil
}

func (r *userProjectBindingReconciler) handleDeletion(ctx context.Context, userProjectBinding *kubermaticv1.UserProjectBinding) error {
	project := &kubermaticv1.Project{}
	if err := r.Get(ctx, types.NamespacedName{
		Name: userProjectBinding.Spec.ProjectID,
	}, project); err != nil {
		return fmt.Errorf("faileding to get project: %w", err)
	}

	org, err := r.grafanaClient.GetOrgByOrgName(ctx, getOrgNameForProject(project))
	if err != nil {
		return err
	}

	users, err := r.grafanaClient.GetOrgUsers(ctx, org.ID)
	if err != nil {
		return err
	}

	for _, user := range users {
		if user.Email == userProjectBinding.Spec.UserEmail {
			_, err = r.grafanaClient.DeleteOrgUser(ctx, org.ID, user.ID)
			if err != nil {
				return err
			}
		}
	}

	kubernetes.RemoveFinalizer(userProjectBinding, mlaFinalizer)
	if err := r.Update(ctx, userProjectBinding); err != nil {
		return fmt.Errorf("updating UserProjectBinding: %w", err)
	}

	return nil
}
