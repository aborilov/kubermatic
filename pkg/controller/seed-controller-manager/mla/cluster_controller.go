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
	"reflect"
	"strconv"

	"k8c.io/kubermatic/v2/pkg/controller/util/predicate"
	kubermaticapiv1 "k8c.io/kubermatic/v2/pkg/crd/kubermatic/v1"
	kubermaticv1 "k8c.io/kubermatic/v2/pkg/crd/kubermatic/v1"
	"k8c.io/kubermatic/v2/pkg/kubernetes"
	"k8c.io/kubermatic/v2/pkg/resources"
	"k8c.io/kubermatic/v2/pkg/resources/reconciling"
	"k8c.io/kubermatic/v2/pkg/version/kubermatic"

	grafanasdk "github.com/aborilov/sdk"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"

	"go.uber.org/zap"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	lokiDatasourceAnnotationKey       = "mla.k8s.io/loki"
	prometheusDatasourceAnnotationKey = "mla.k8s.io/prometheus"
)

// clusterReconciler stores necessary components that are required to manage MLA(Monitoring, Logging, and Alerting) setup.
type clusterReconciler struct {
	ctrlruntimeclient.Client
	grafanaClient *grafanasdk.Client

	log        *zap.SugaredLogger
	workerName string
	recorder   record.EventRecorder
	versions   kubermatic.Versions
}

func newClusterReconciler(
	mgr manager.Manager,
	log *zap.SugaredLogger,
	numWorkers int,
	workerName string,
	versions kubermatic.Versions,
	grafanaClient *grafanasdk.Client,
) error {
	log = log.Named(ControllerName)
	client := mgr.GetClient()

	reconciler := &clusterReconciler{
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

	if err := c.Watch(&source.Kind{Type: &kubermaticv1.Cluster{}}, &handler.EnqueueRequestForObject{}, debugPredicate); err != nil {
		return fmt.Errorf("failed to watch Clusters: %v", err)
	}
	return err
}

func (r *clusterReconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := r.log.With("request", request)
	log.Debug("Processing")

	cluster := &kubermaticv1.Cluster{}
	if err := r.Get(ctx, request.NamespacedName, cluster); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	if !cluster.DeletionTimestamp.IsZero() {
		if err := r.handleDeletion(ctx, cluster); err != nil {
			return reconcile.Result{}, fmt.Errorf("handling deletion: %w", err)
		}
		return reconcile.Result{}, nil
	}

	if !kubernetes.HasFinalizer(cluster, mlaFinalizer) {
		kubernetes.AddFinalizer(cluster, mlaFinalizer)
		if err := r.Update(ctx, cluster); err != nil {
			return reconcile.Result{}, fmt.Errorf("updating finalizers: %w", err)
		}
	}
	if err := r.ensureConfigMaps(ctx, cluster); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to reconcile ConfigMaps in namespace %s: %w", cluster.Status.NamespaceName, err)
	}

	if err := r.ensureDeployments(ctx, cluster); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to reconcile Deployments in namespace %s: %w", cluster.Status.NamespaceName, err)
	}
	if err := r.ensureServices(ctx, cluster); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to reconcile Services in namespace %s: %w", "mla", err)
	}

	projectID, ok := cluster.GetLabels()[kubermaticapiv1.ProjectIDLabelKey]
	if !ok {
		return reconcile.Result{}, fmt.Errorf("unable to get project name from label")
	}

	project := &kubermaticv1.Project{}
	if err := r.Get(ctx, types.NamespacedName{Name: projectID}, project); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get project: %w", err)
	}

	org, err := r.grafanaClient.GetOrgByOrgName(ctx, getOrgNameForProject(project))
	if err != nil {
		return reconcile.Result{}, err
	}

	lokiDS := grafanasdk.Datasource{
		OrgID:  org.ID,
		Name:   getLokiDatasourceNameForCluster(cluster),
		Type:   "loki",
		Access: "proxy",
		URL:    fmt.Sprintf("http://mla-gateway.%s.svc.cluster.local", cluster.Status.NamespaceName),
	}
	if err := r.ensureDatasource(ctx, cluster, lokiDS, lokiDatasourceAnnotationKey); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to ensure Grafana Loki Datasources: %w", err)
	}

	prometheusDS := grafanasdk.Datasource{
		OrgID:  org.ID,
		Name:   getPrometheusDatasourceNameForCluster(cluster),
		Type:   "prometheus",
		Access: "proxy",
		URL:    fmt.Sprintf("http://mla-gateway.%s.svc.cluster.local/api/prom", cluster.Status.NamespaceName),
	}
	if err := r.ensureDatasource(ctx, cluster, prometheusDS, prometheusDatasourceAnnotationKey); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to ensure Grafana Prometheus Datasources: %w", err)
	}

	return reconcile.Result{}, nil
}

func (r *clusterReconciler) setAnnotation(ctx context.Context, cluster *kubermaticv1.Cluster, key, value string) error {
	annotations := cluster.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[key] = value
	cluster.SetAnnotations(annotations)
	if err := r.Update(ctx, cluster); err != nil {
		return fmt.Errorf("updating Cluster: %w", err)
	}
	return nil
}

func (r *clusterReconciler) ensureDatasource(ctx context.Context, cluster *kubermaticv1.Cluster, expected grafanasdk.Datasource, annotationKey string) error {
	dsID, ok := cluster.GetAnnotations()[annotationKey]
	if !ok {
		status, err := r.grafanaClient.CreateDatasource(ctx, expected)
		if err != nil {
			return fmt.Errorf("unable to add datasource: %w (status: %s, message: %s)",
				err, pointer.StringPtrDerefOr(status.Status, "no status"), pointer.StringPtrDerefOr(status.Message, "no message"))
		}
		if status.ID == nil {
			return fmt.Errorf("datasource ID is nil")
		}
		return r.setAnnotation(ctx, cluster, annotationKey, strconv.FormatUint(uint64(*status.ID), 10))
	}
	id, err := strconv.ParseUint(dsID, 10, 32)
	if err != nil {
		return err
	}

	ds, err := r.grafanaClient.GetDatasource(ctx, uint(id))
	if err != nil {
		// possibly not found
		status, err := r.grafanaClient.CreateDatasource(ctx, expected)
		if err != nil {
			return fmt.Errorf("unable to add datasource: %w (status: %s, message: %s)",
				err, pointer.StringPtrDerefOr(status.Status, "no status"), pointer.StringPtrDerefOr(status.Message, "no message"))
		}
		if status.ID == nil {
			return fmt.Errorf("datasource ID is nil")
		}
		return r.setAnnotation(ctx, cluster, annotationKey, strconv.FormatUint(uint64(*status.ID), 10))
	}
	expected.ID = uint(id)
	if !reflect.DeepEqual(ds, expected) {
		if status, err := r.grafanaClient.UpdateDatasource(ctx, expected); err != nil {
			return fmt.Errorf("unable to update datasource: %w (status: %s, message: %s)",
				err, pointer.StringPtrDerefOr(status.Status, "no status"), pointer.StringPtrDerefOr(status.Message, "no message"))
		}
	}
	return nil

}
func (r *clusterReconciler) ensureDeployments(ctx context.Context, c *kubermaticv1.Cluster) error {
	creators := []reconciling.NamedDeploymentCreatorGetter{
		GatewayDeploymentCreator(),
	}
	if err := reconciling.ReconcileDeployments(ctx, creators, c.Status.NamespaceName, r.Client, reconciling.OwnerRefWrapper(resources.GetClusterRef(c))); err != nil {
		return err
	}
	return nil
}

func (r *clusterReconciler) ensureConfigMaps(ctx context.Context, c *kubermaticv1.Cluster) error {
	creators := []reconciling.NamedConfigMapCreatorGetter{
		GatewayConfigMapCreator(c),
	}
	if err := reconciling.ReconcileConfigMaps(ctx, creators, c.Status.NamespaceName, r.Client, reconciling.OwnerRefWrapper(resources.GetClusterRef(c))); err != nil {
		return fmt.Errorf("failed to ensure that the ConfigMap exists: %v", err)
	}
	return nil
}

func (r *clusterReconciler) ensureServices(ctx context.Context, c *kubermaticv1.Cluster) error {
	creators := []reconciling.NamedServiceCreatorGetter{
		GatewayAlertServiceCreator(),
		GatewayInternalServiceCreator(),
		GatewayExternalServiceCreator(),
	}
	return reconciling.ReconcileServices(ctx, creators, c.Status.NamespaceName, r.Client, reconciling.OwnerRefWrapper(resources.GetClusterRef(c)))
}

func (r *clusterReconciler) handleDeletion(ctx context.Context, cluster *kubermaticv1.Cluster) error {
	projectID, ok := cluster.GetLabels()[kubermaticapiv1.ProjectIDLabelKey]
	if !ok {
		return fmt.Errorf("unable to get project name from label")
	}

	project := &kubermaticv1.Project{}
	if err := r.Get(ctx, types.NamespacedName{Name: projectID}, project); err != nil {
		return fmt.Errorf("failed to get project: %w", err)
	}

	org, err := r.grafanaClient.GetOrgByOrgName(ctx, getOrgNameForProject(project))
	if err != nil {
		return err
	}
	if status, err := r.grafanaClient.SwitchActualUserContext(ctx, org.ID); err != nil {
		return fmt.Errorf("unable to switch context to org %d: %w (status: %s, message: %s)",
			org.ID, err, pointer.StringPtrDerefOr(status.Status, "no status"), pointer.StringPtrDerefOr(status.Message, "no message"))
	}
	if status, err := r.grafanaClient.DeleteDatasourceByName(ctx, getPrometheusDatasourceNameForCluster(cluster)); err != nil {
		return fmt.Errorf("unable to delete datasource: %w (status: %s, message: %s)",
			err, pointer.StringPtrDerefOr(status.Status, "no status"), pointer.StringPtrDerefOr(status.Message, "no message"))
	}
	if status, err := r.grafanaClient.DeleteDatasourceByName(ctx, getLokiDatasourceNameForCluster(cluster)); err != nil {
		return fmt.Errorf("unable to delete datasource: %w (status: %s, message: %s)",
			err, pointer.StringPtrDerefOr(status.Status, "no status"), pointer.StringPtrDerefOr(status.Message, "no message"))
	}

	kubernetes.RemoveFinalizer(cluster, mlaFinalizer)
	if err := r.Update(ctx, cluster); err != nil {
		return fmt.Errorf("updating Cluster: %w", err)
	}
	return nil
}
