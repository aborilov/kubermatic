package kyma

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	kymaReconciler "github.com/kyma-incubator/reconciler/pkg/reconciler"
	"go.uber.org/zap"
	clusterclient "k8c.io/kubermatic/v2/pkg/cluster/client"
	predicateutil "k8c.io/kubermatic/v2/pkg/controller/util/predicate"
	kubermaticv1 "k8c.io/kubermatic/v2/pkg/crd/kubermatic/v1"
	kubermaticv1helper "k8c.io/kubermatic/v2/pkg/crd/kubermatic/v1/helper"
	"k8c.io/kubermatic/v2/pkg/version/kubermatic"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	// ControllerName name of etcd backup controller.
	ControllerName = "kyma_controller"
)

type Reconciler struct {
	ctrlruntimeclient.Client

	log                *zap.SugaredLogger
	scheme             *runtime.Scheme
	workerName         string
	recorder           record.EventRecorder
	versions           kubermatic.Versions
	KubeconfigProvider KubeconfigProvider
}

// KubeconfigProvider provides functionality to get a clusters admin kubeconfig
type KubeconfigProvider interface {
	GetAdminKubeconfig(ctx context.Context, c *kubermaticv1.Cluster) ([]byte, error)
	GetClient(ctx context.Context, c *kubermaticv1.Cluster, options ...clusterclient.ConfigOption) (ctrlruntimeclient.Client, error)
}

// Add creates a new Backup controller that is responsible for
// managing cluster etcd backups
func Add(
	mgr manager.Manager,
	log *zap.SugaredLogger,
	numWorkers int,
	workerName string,
	versions kubermatic.Versions,
	kubeconfigProvider KubeconfigProvider,
) error {
	log = log.Named(ControllerName)
	client := mgr.GetClient()

	reconciler := &Reconciler{
		Client:             client,
		log:                log,
		scheme:             mgr.GetScheme(),
		workerName:         workerName,
		recorder:           mgr.GetEventRecorderFor(ControllerName),
		versions:           versions,
		KubeconfigProvider: kubeconfigProvider,
	}

	ctrlOptions := controller.Options{
		Reconciler:              reconciler,
		MaxConcurrentReconciles: numWorkers,
	}
	c, err := controller.New(ControllerName, mgr, ctrlOptions)
	if err != nil {
		return err
	}
	predicates := []predicate.Predicate{}
	if workerName != "" {
		debugPredicate := predicateutil.ByLabel(kubermaticv1.WorkerNameLabelKey, workerName)
		predicates = append(predicates, debugPredicate)
	}

	return c.Watch(&source.Kind{Type: &kubermaticv1.Cluster{}}, &handler.EnqueueRequestForObject{}, predicates...)
}

// Reconcile handle etcd backups reconciliation.
func (r *Reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := r.log.With("request", request)
	log.Debug("Processing")

	cluster := &kubermaticv1.Cluster{}
	if err := r.Get(ctx, request.NamespacedName, cluster); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	if cluster.Status.NamespaceName == "" {
		log.Debug("Skipping cluster reconciling because it has no namespace yet")
		return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Add a wrapping here so we can emit an event on error
	result, err := kubermaticv1helper.ClusterReconcileWrapper(
		ctx,
		r.Client,
		r.workerName,
		cluster,
		r.versions,
		kubermaticv1.ClusterConditionMLAControllerReconcilingSuccess,
		func() (*reconcile.Result, error) {
			return r.reconcile(ctx, cluster)
		},
	)
	if err != nil {
		r.log.Errorw("Failed to reconcile cluster", "cluster", cluster.Name, zap.Error(err))
		r.recorder.Event(cluster, corev1.EventTypeWarning, "ReconcilingError", err.Error())
	}
	if result == nil {
		result = &reconcile.Result{}
	}
	return *result, err
}

func (r *Reconciler) reconcile(ctx context.Context, cluster *kubermaticv1.Cluster) (*reconcile.Result, error) {
	baseUrl := "http://a8849d68e916340e699f9889bcfedab1-894106571.eu-central-1.elb.amazonaws.com:8080/v1/run"
	istioUrl := "http://af4dacee147d342c59180fc4189adab6-10373660.eu-central-1.elb.amazonaws.com:8080/v1/run"
	kubeConfig, err := r.KubeconfigProvider.GetAdminKubeconfig(ctx, cluster)
	if err != nil {
		return nil, err
	}
	kr := kymaReconciler.Reconciliation{
		Component: "cluster-essentials",
		Namespace: "kyma-system",
		Version:   "main",
		Profile:   "Evaluation",
		Configuration: map[string]interface{}{
			"global": map[string]interface{}{
				"domainName":  "example.com",
				"installCRDs": true,
			},
		},
		Kubeconfig:    string(kubeConfig),
		CallbackURL:   "https://httpbin.org/post",
		CorrelationID: "1-2-3-4-5",
	}
	data, err := json.Marshal(kr)
	if err != nil {
		return nil, err
	}

	resp, err := http.Post(baseUrl, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	fmt.Println(string(respBody))

	kr.Component = "istio"
	kr.Namespace = "istio-system"
	data, err = json.Marshal(kr)
	if err != nil {
		return nil, err
	}

	resp, err = http.Post(istioUrl, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	fmt.Println(string(respBody))

	return nil, nil
}
