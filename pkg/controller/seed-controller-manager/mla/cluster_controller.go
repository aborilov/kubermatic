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
	"bytes"
	"context"
	"fmt"
	"strings"
	"text/template"

	"k8c.io/kubermatic/v2/pkg/controller/operator/common"
	"k8c.io/kubermatic/v2/pkg/controller/util/predicate"
	kubermaticapiv1 "k8c.io/kubermatic/v2/pkg/crd/kubermatic/v1"
	kubermaticv1 "k8c.io/kubermatic/v2/pkg/crd/kubermatic/v1"
	"k8c.io/kubermatic/v2/pkg/kubernetes"
	"k8c.io/kubermatic/v2/pkg/resources"
	"k8c.io/kubermatic/v2/pkg/resources/reconciling"
	"k8c.io/kubermatic/v2/pkg/version/kubermatic"

	"github.com/Masterminds/sprig/v3"
	grafanasdk "github.com/aborilov/sdk"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
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

	kubernetes.AddFinalizer(cluster, mlaFinalizer)
	if err := r.Update(ctx, cluster); err != nil {
		return reconcile.Result{}, fmt.Errorf("updating finalizers: %w", err)
	}
	if err := r.ensureConfigMaps(ctx, cluster); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to reconcile ConfigMaps in namespace %s: %v", cluster.Status.NamespaceName, err)
	}

	if err := r.ensureDeployments(ctx, cluster); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to reconcile Deployments in namespace %s: %v", cluster.Status.NamespaceName, err)
	}
	if err := r.ensureServices(ctx, cluster); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to reconcile Services in namespace %s: %v", "mla", err)
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
		Name:   "Loki",
		Type:   "loki",
		Access: "proxy",
		URL:    fmt.Sprintf("http://mla-gateway.%s.svc.cluster.local", cluster.Status.NamespaceName),
	}
	if status, err := r.grafanaClient.CreateDatasource(ctx, lokiDS); err != nil {
		return reconcile.Result{}, fmt.Errorf("unable to add loki datasource: %w (status: %s, message: %s)",
			err, pointer.StringPtrDerefOr(status.Status, "no status"), pointer.StringPtrDerefOr(status.Message, "no message"))
	}
	prometheusDS := grafanasdk.Datasource{
		OrgID:  org.ID,
		Name:   "Prometheus",
		Type:   "prometheus",
		Access: "proxy",
		URL:    fmt.Sprintf("http://mla-gateway.%s.svc.cluster.local/api/prom", cluster.Status.NamespaceName),
	}
	if status, err := r.grafanaClient.CreateDatasource(ctx, prometheusDS); err != nil {
		return reconcile.Result{}, fmt.Errorf("unable to add prometheus datasource: %w (status: %s, message: %s)",
			err, pointer.StringPtrDerefOr(status.Status, "no status"), pointer.StringPtrDerefOr(status.Message, "no message"))
	}

	return reconcile.Result{}, nil
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

func GatewayAlertServiceCreator() reconciling.NamedServiceCreatorGetter {
	return func() (string, reconciling.ServiceCreator) {
		return "mla-gateway-alert", func(s *corev1.Service) (*corev1.Service, error) {
			s.Spec.Type = corev1.ServiceTypeLoadBalancer
			s.Spec.Ports = []corev1.ServicePort{
				{
					Name:       "http-alert",
					Protocol:   corev1.ProtocolTCP,
					Port:       80,
					TargetPort: intstr.FromString("http-alert"),
				},
			}
			return s, nil
		}
	}
}

func GatewayInternalServiceCreator() reconciling.NamedServiceCreatorGetter {
	return func() (string, reconciling.ServiceCreator) {
		return "mla-gateway", func(s *corev1.Service) (*corev1.Service, error) {
			s.Spec.Type = corev1.ServiceTypeClusterIP
			s.Spec.Ports = []corev1.ServicePort{
				{
					Name:       "http-int",
					Protocol:   corev1.ProtocolTCP,
					Port:       80,
					TargetPort: intstr.FromString("http-int"),
				},
			}
			return s, nil
		}
	}
}

func GatewayExternalServiceCreator() reconciling.NamedServiceCreatorGetter {
	return func() (string, reconciling.ServiceCreator) {
		return "mla-gateway-ext", func(s *corev1.Service) (*corev1.Service, error) {
			s.Spec.Type = corev1.ServiceTypeClusterIP
			s.Spec.Ports = []corev1.ServicePort{
				{
					Name:       "http-ext",
					Protocol:   corev1.ProtocolTCP,
					Port:       80,
					TargetPort: intstr.FromString("http-ext"),
				},
			}
			return s, nil
		}
	}
}

func GatewayDeploymentCreator() reconciling.NamedDeploymentCreatorGetter {
	return func() (string, reconciling.DeploymentCreator) {
		return "mla-gateway", func(d *appsv1.Deployment) (*appsv1.Deployment, error) {
			d.SetName("mla-gateway")
			d.Spec.Replicas = pointer.Int32Ptr(1)
			d.Spec.Selector = &metav1.LabelSelector{
				MatchLabels: map[string]string{
					common.NameLabel: "mla",
				},
			}
			d.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
				FSGroup:      pointer.Int64Ptr(1001),
				RunAsGroup:   pointer.Int64Ptr(2001),
				RunAsUser:    pointer.Int64Ptr(1001),
				RunAsNonRoot: pointer.BoolPtr(true),
			}
			d.Spec.Template.Labels = d.Spec.Selector.MatchLabels
			d.Spec.Template.Spec.Containers = []corev1.Container{
				{
					Name:            "nginx",
					Image:           "docker.io/nginxinc/nginx-unprivileged:1.19-alpine",
					ImagePullPolicy: corev1.PullIfNotPresent,
					Ports: []corev1.ContainerPort{
						{
							Name:          "http-ext",
							ContainerPort: 8080,
							Protocol:      corev1.ProtocolTCP,
						},
						{
							Name:          "http-int",
							ContainerPort: 8081,
							Protocol:      corev1.ProtocolTCP,
						},
						{
							Name:          "http-alert",
							ContainerPort: 8082,
							Protocol:      corev1.ProtocolTCP,
						},
					},
					ReadinessProbe: &corev1.Probe{
						Handler: corev1.Handler{
							HTTPGet: &corev1.HTTPGetAction{
								Path:   "/",
								Port:   intstr.FromString("http-int"),
								Scheme: corev1.URISchemeHTTP,
							},
						},
						InitialDelaySeconds: 15,
						TimeoutSeconds:      1,
						PeriodSeconds:       10,
						SuccessThreshold:    1,
						FailureThreshold:    3,
					},
					SecurityContext: &corev1.SecurityContext{
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
						ReadOnlyRootFilesystem:   pointer.BoolPtr(true),
						AllowPrivilegeEscalation: pointer.BoolPtr(false),
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "config",
							MountPath: "/etc/nginx",
						},
						{
							Name:      "tmp",
							MountPath: "/tmp",
						},
						{
							Name:      "docker-entrypoint-d-override",
							MountPath: "/docker-entrypoint.d",
						},
					},
				},
			}
			d.Spec.Template.Spec.Volumes = []corev1.Volume{
				{
					Name: "config",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "mla-gateway",
							},
						},
					},
				},
				{
					Name:         "tmp",
					VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
				{
					Name:         "docker-entrypoint-d-override",
					VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
				},
			}
			return d, nil
		}
	}
}

const nginxConfig = `worker_processes  1;
error_log  /dev/stderr;
pid        /tmp/nginx.pid;
worker_rlimit_nofile 8192;

events {
  worker_connections  1024;
}

http {
  client_body_temp_path /tmp/client_temp;
  proxy_temp_path       /tmp/proxy_temp_path;
  fastcgi_temp_path     /tmp/fastcgi_temp;
  uwsgi_temp_path       /tmp/uwsgi_temp;
  scgi_temp_path        /tmp/scgi_temp;

  default_type application/octet-stream;
  log_format   main '$remote_addr - $remote_user [$time_local]  $status '
	'"$request" $body_bytes_sent "$http_referer" '
	'"$http_user_agent" "$http_x_forwarded_for" $http_x_scope_orgid';
  access_log   /dev/stderr  main;
  sendfile     on;
  tcp_nopush   on;
  resolver kube-dns.kube-system.svc.cluster.local;

  # write path - exposed to user clusters
  server {
	listen             8080;
	proxy_set_header X-Scope-OrgID {{ .TenantID}};

	# Loki Config
	location = /loki/api/v1/push {
	  proxy_pass       http://loki-distributed-distributor.{{ .Namespace}}.svc.cluster.local:3100$request_uri;
	}

	# Cortex Config
	location = /api/v1/push {
	  proxy_pass      http://cortex-distributor.{{ .Namespace}}.svc.cluster.local:8080$request_uri;
	}
  }

  # read path - cluster-local access only
  server {
	listen             8081;
	proxy_set_header   X-Scope-OrgID {{ .TenantID}};

	# k8s probes
	location = / {
	  return 200 'OK';
	  auth_basic off;
	}

	# location = /api/prom/tail {
	#   proxy_pass       http://loki-distributed-querier.{{ .Namespace}}.svc.cluster.local:3100$request_uri;
	#   proxy_set_header Upgrade $http_upgrade;
	#   proxy_set_header Connection "upgrade";
	# }

	# location = /loki/api/v1/tail {
	#   proxy_pass       http://loki-distributed-querier.{{ .Namespace}}.svc.cluster.local:3100$request_uri;
	#   proxy_set_header Upgrade $http_upgrade;
	#   proxy_set_header Connection "upgrade";
	# }

	location ~ /loki/api/.* {
	  proxy_pass       http://loki-distributed-query-frontend.{{ .Namespace}}.svc.cluster.local:3100$request_uri;
	}

	# Cortex Config
	location ~ /api/prom/.* {
	  proxy_pass       http://cortex-query-frontend.{{ .Namespace}}.svc.cluster.local:8080$request_uri;
	}
  }

  # public read and write path - used for alertmanager only
  server {
	listen             8082;
	proxy_set_header   X-Scope-OrgID {{ .TenantID}};

	# Alertmanager Config
	location ~ /api/prom/alertmanager.* {
	  proxy_pass      http://cortex-alertmanager.{{ .Namespace}}.svc.cluster.local:8080$request_uri;
	}
	location ~ /api/v1/alerts {
	  proxy_pass      http://cortex-alertmanager.{{ .Namespace}}.svc.cluster.local:8080$request_uri;
	}
	location ~ /multitenant_alertmanager/status {
	  proxy_pass      http://cortex-alertmanager.{{ .Namespace}}.svc.cluster.local:8080$request_uri;
	}
  }
}
`

type configTemplateData struct {
	Namespace string
	TenantID  string
}

func renderTemplate(tpl string, data interface{}) (string, error) {
	t, err := template.New("base").Funcs(sprig.TxtFuncMap()).Parse(tpl)
	if err != nil {
		return "", fmt.Errorf("failed to parse as Go template: %v", err)
	}

	output := bytes.Buffer{}
	if err := t.Execute(&output, data); err != nil {
		return "", fmt.Errorf("failed to render template: %v", err)
	}

	return strings.TrimSpace(output.String()), nil
}

func GatewayConfigMapCreator(c *kubermaticv1.Cluster) reconciling.NamedConfigMapCreatorGetter {
	return func() (string, reconciling.ConfigMapCreator) {
		return "mla-gateway", func(cm *corev1.ConfigMap) (*corev1.ConfigMap, error) {
			if cm.Data == nil {
				configData := configTemplateData{
					Namespace: "mla",
					TenantID:  c.Name,
				}
				config, err := renderTemplate(nginxConfig, configData)
				if err != nil {
					return nil, fmt.Errorf("failed to render Prometheus config: %v", err)
				}

				cm.Data = map[string]string{
					"nginx.conf": config}
			}
			return cm, nil
		}
	}
}

func (r *clusterReconciler) handleDeletion(ctx context.Context, cluster *kubermaticv1.Cluster) error {
	kubernetes.RemoveFinalizer(cluster, mlaFinalizer)
	if err := r.Update(ctx, cluster); err != nil {
		return fmt.Errorf("updating Cluster: %w", err)
	}
	return nil
}
