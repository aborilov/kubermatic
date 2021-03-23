package mla

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	grafanasdk "github.com/aborilov/sdk"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kubermaticv1 "k8c.io/kubermatic/v2/pkg/crd/kubermatic/v1"
	"k8c.io/kubermatic/v2/pkg/kubernetes"
	kubermaticlog "k8c.io/kubermatic/v2/pkg/log"
)

func newTestProjectReconciler(t *testing.T, objects []ctrlruntimeclient.Object, handlerFunc http.HandlerFunc) (*projectReconciler, *httptest.Server) {
	dynamicClient := ctrlruntimefakeclient.
		NewClientBuilder().
		WithObjects(objects...).
		Build()
	ts := httptest.NewServer(handlerFunc)

	grafanaClient := grafanasdk.NewClient(ts.URL, "admin:admin", ts.Client())

	reconciler := projectReconciler{
		Client:        dynamicClient,
		grafanaClient: grafanaClient,
		log:           kubermaticlog.Logger,
		recorder:      record.NewFakeRecorder(10),
	}
	return &reconciler, ts
}

func TestProjectReconcile(t *testing.T) {
	testCases := []struct {
		name          string
		project       *kubermaticv1.Project
		handlerFunc   http.HandlerFunc
		requestName   string
		requestCode   int
		request       *grafanasdk.Org
		statusMessage grafanasdk.StatusMessage
	}{
		{
			name: "create org for project",
			project: &kubermaticv1.Project{
				ObjectMeta: metav1.ObjectMeta{
					Name: "create",
				},
				Spec: kubermaticv1.ProjectSpec{
					Name: "projectName",
				},
			},
			requestName: "create",
			requestCode: 200,
			request: &grafanasdk.Org{
				Name: "projectName-create",
			},
		},
	}

	for idx := range testCases {
		tc := testCases[idx]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handled := false
			handlerFunc := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				decoder := json.NewDecoder(r.Body)
				org := &grafanasdk.Org{}
				if err := decoder.Decode(org); err != nil {
					t.Fatalf("unmarshal grafana org failed: %v", err)
				}
				assert.Equal(t, tc.request, org)
				w.WriteHeader(tc.requestCode)
				encoder := json.NewEncoder(w)
				encoder.Encode(tc.statusMessage)
				handled = true
			})
			ctx := context.Background()
			objects := []ctrlruntimeclient.Object{tc.project}
			controller, server := newTestProjectReconciler(t, objects, handlerFunc)
			request := reconcile.Request{NamespacedName: types.NamespacedName{Name: tc.requestName}}
			if _, err := controller.Reconcile(ctx, request); err != nil {
				t.Fatalf("reconciling failed: %v", err)
			}
			project := &kubermaticv1.Project{}
			if err := controller.Get(ctx, request.NamespacedName, project); err != nil {
				t.Fatalf("unable to get project: %v", err)
			}
			assert.True(t, kubernetes.HasFinalizer(project, mlaFinalizer))
			assert.True(t, handled)
			defer server.Close()
		})
	}

}
