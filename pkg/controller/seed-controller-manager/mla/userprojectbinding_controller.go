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
	"encoding/json"
	"fmt"
	"net/http"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"

	"k8c.io/kubermatic/v2/pkg/controller/master-controller-manager/rbac"
	"k8c.io/kubermatic/v2/pkg/controller/util/predicate"

	"k8c.io/kubermatic/v2/pkg/kubernetes"

	grafanasdk "github.com/aborilov/sdk"
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

	log           *zap.SugaredLogger
	workerName    string
	recorder      record.EventRecorder
	versions      kubermatic.Versions
	grafanaURL    string
	grafanaHeader string
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
	grafanaURL string,
	grafanaHeader string,
) error {
	log = log.Named(ControllerName)
	client := mgr.GetClient()

	reconciler := &userProjectBindingReconciler{
		Client:        client,
		grafanaClient: grafanaClient,

		log:           log,
		workerName:    workerName,
		recorder:      mgr.GetEventRecorderFor(ControllerName),
		versions:      versions,
		grafanaURL:    grafanaURL,
		grafanaHeader: grafanaHeader,
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

	// checking if user already exists corresponding organization
	user, err := r.getGrafanaOrgUser(ctx, userProjectBinding)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("unable to get user : %w", err)
	}
	// if there is no such user in project organization, let's create one
	if user == nil {
		if _, err := r.addGrafanaOrgUser(ctx, userProjectBinding); err != nil {
			return reconcile.Result{}, fmt.Errorf("unable to add grafana user : %w", err)
		}
		return reconcile.Result{}, nil
	}

	group := rbac.ExtractGroupPrefix(userProjectBinding.Spec.Group)
	role := groupToRole[group]

	if user.Role != string(role) {
		userRole := grafanasdk.UserRole{
			LoginOrEmail: userProjectBinding.Spec.UserEmail,
			Role:         string(role),
		}
		if status, err := r.grafanaClient.UpdateOrgUser(ctx, userRole, user.OrgId, user.ID); err != nil {

			return reconcile.Result{}, fmt.Errorf("unable to upate grafana user role: %w (status: %s, message: %s)", err, pointer.StringPtrDerefOr(status.Status, "no status"), pointer.StringPtrDerefOr(status.Message, "no message"))
		}
	}

	return reconcile.Result{}, nil
}

func (r *userProjectBindingReconciler) handleDeletion(ctx context.Context, userProjectBinding *kubermaticv1.UserProjectBinding) error {
	user, err := r.getGrafanaOrgUser(ctx, userProjectBinding)
	if err != nil {
		return fmt.Errorf("unable to get user : %w", err)
	}
	if user != nil {
		status, err := r.grafanaClient.DeleteOrgUser(ctx, user.OrgId, user.ID)
		if err != nil {
			return fmt.Errorf("failed to delete org user: %w (status: %s, message: %s)", err, pointer.StringPtrDerefOr(status.Status, "no status"), pointer.StringPtrDerefOr(status.Message, "no message"))
		}
	}

	kubernetes.RemoveFinalizer(userProjectBinding, mlaFinalizer)
	if err := r.Update(ctx, userProjectBinding); err != nil {
		return fmt.Errorf("updating UserProjectBinding: %w", err)
	}

	return nil
}

func (r *userProjectBindingReconciler) getGrafanaOrgUser(ctx context.Context, userProjectBinding *kubermaticv1.UserProjectBinding) (*grafanasdk.OrgUser, error) {
	project := &kubermaticv1.Project{}
	if err := r.Get(ctx, types.NamespacedName{
		Name: userProjectBinding.Spec.ProjectID,
	}, project); err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}

	org, err := r.grafanaClient.GetOrgByOrgName(ctx, getOrgNameForProject(project))
	if err != nil {
		return nil, err
	}

	users, err := r.grafanaClient.GetOrgUsers(ctx, org.ID)
	if err != nil {
		return nil, err
	}

	for _, user := range users {
		if user.Email == userProjectBinding.Spec.UserEmail {
			return &user, nil
		}
	}
	return nil, nil
}

func (r *userProjectBindingReconciler) addGrafanaOrgUser(ctx context.Context, userProjectBinding *kubermaticv1.UserProjectBinding) (*grafanasdk.OrgUser, error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", r.grafanaURL+"/api/user", nil)
	req.Header.Add(r.grafanaHeader, userProjectBinding.Spec.UserEmail)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	type response struct {
		ID uint `json:"id"`
	}
	res := &response{}
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(res); err != nil || res.ID == 0 {
		return nil, fmt.Errorf("unable to decode responce : %w", err)
	}
	if status, err := r.grafanaClient.DeleteOrgUser(ctx, defaultOrgID, res.ID); err != nil {
		return nil, fmt.Errorf("failed to delete grafana user from default org: %w (status: %s, message: %s)", err, pointer.StringPtrDerefOr(status.Status, "no status"), pointer.StringPtrDerefOr(status.Message, "no message"))
	}

	project := &kubermaticv1.Project{}
	if err := r.Get(ctx, types.NamespacedName{
		Name: userProjectBinding.Spec.ProjectID,
	}, project); err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}

	org, err := r.grafanaClient.GetOrgByOrgName(ctx, getOrgNameForProject(project))
	if err != nil {
		return nil, err
	}

	group := rbac.ExtractGroupPrefix(userProjectBinding.Spec.Group)
	role := groupToRole[group]
	userRole := grafanasdk.UserRole{
		LoginOrEmail: userProjectBinding.Spec.UserEmail,
		Role:         string(role),
	}
	if status, err := r.grafanaClient.AddOrgUser(ctx, userRole, org.ID); err != nil {
		return nil, fmt.Errorf("failed to add grafana user to org: %w (status: %s, message: %s)", err, pointer.StringPtrDerefOr(status.Status, "no status"), pointer.StringPtrDerefOr(status.Message, "no message"))
	}
	return &grafanasdk.OrgUser{
		ID:    res.ID,
		OrgId: org.ID,
		Email: userProjectBinding.Spec.UserEmail,
		Login: userProjectBinding.Spec.UserEmail,
		Role:  string(role),
	}, nil
}
