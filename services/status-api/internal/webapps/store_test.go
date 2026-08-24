package webapps

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
)

const (
	testUID = types.UID("11111111-2222-3333-4444-555555555555")
	testNS  = "idp-demo"
)

// webappCR builds an unstructured WebApp the way the API server would store it.
func webappCR(namespace, name string, opts ...func(map[string]any)) *unstructured.Unstructured {
	obj := map[string]any{
		"apiVersion": "platform.miportfolio.com/v1",
		"kind":       "WebApp",
		"metadata": map[string]any{
			"name":              name,
			"namespace":         namespace,
			"uid":               string(testUID),
			"creationTimestamp": "2026-08-24T10:00:00Z",
		},
		"spec": map[string]any{
			"image":    "nginx:1.27-alpine",
			"replicas": int64(3),
			"port":     int64(80),
		},
	}
	for _, opt := range opts {
		opt(obj)
	}
	return &unstructured.Unstructured{Object: obj}
}

func withAvailable(status, reason, message string) func(map[string]any) {
	return func(obj map[string]any) {
		obj["status"] = map[string]any{
			"conditions": []any{
				map[string]any{
					"type":               "Available",
					"status":             status,
					"reason":             reason,
					"message":            message,
					"lastTransitionTime": "2026-08-24T10:05:00Z",
				},
			},
		}
	}
}

func withAutoscaling(minR, maxR, cpu int64) func(map[string]any) {
	return func(obj map[string]any) {
		spec, _ := obj["spec"].(map[string]any)
		spec["autoscaling"] = map[string]any{
			"minReplicas":         minR,
			"maxReplicas":         maxR,
			"cpuThresholdPercent": cpu,
		}
	}
}

// ownedDeployment builds the Deployment the operator would create, complete with
// the controller owner reference the store matches on.
func ownedDeployment(namespace, name string, owner types.UID, replicas, ready int32, image string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "platform.miportfolio.com/v1",
				Kind:       "WebApp",
				Name:       "demo",
				UID:        owner,
				Controller: ptr.To(true),
			}},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(replicas),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "webapp", Image: image}},
				},
			},
		},
		Status: appsv1.DeploymentStatus{ReadyReplicas: ready},
	}
}

// newStore wires a Store over fake clients and waits for its caches.
func newStore(t *testing.T, crs []runtime.Object, deployments []runtime.Object) *Store {
	t.Helper()

	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(GVR.GroupVersion().WithKind("WebAppList"), &unstructured.UnstructuredList{})
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{GVR: "WebAppList"},
		crs...,
	)
	typed := k8sfake.NewSimpleClientset(deployments...)

	store := NewStore(dyn, typed, time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	if err := store.Start(ctx); err != nil {
		t.Fatalf("starting the store: %v", err)
	}
	return store
}

func TestListReturnsTheCustomResourcesInTheCache(t *testing.T) {
	store := newStore(t,
		[]runtime.Object{
			webappCR(testNS, "beta", withAvailable("True", "DeploymentReady", "all replicas are ready")),
			webappCR(testNS, "alpha"),
		},
		nil,
	)

	got := store.List()
	if len(got) != 2 {
		t.Fatalf("got %d webapps, want 2", len(got))
	}
	// Sorted by namespace then name, so the output is stable between calls.
	if got[0].Name != "alpha" || got[1].Name != "beta" {
		t.Errorf("unsorted result: %s, %s", got[0].Name, got[1].Name)
	}
}

func TestConvertReadsSpecAndCondition(t *testing.T) {
	store := newStore(t,
		[]runtime.Object{webappCR(testNS, "demo",
			withAutoscaling(2, 6, 70),
			withAvailable("True", "DeploymentReady", "all replicas are ready"),
		)},
		nil,
	)

	app, found := store.Get(testNS, "demo")
	if !found {
		t.Fatal("demo was not found")
	}
	if !app.Available {
		t.Error("Available = false, want true")
	}
	if app.Condition == nil || app.Condition.Reason != "DeploymentReady" {
		t.Errorf("condition = %+v", app.Condition)
	}
	if app.Condition.LastTransitionTime.IsZero() {
		t.Error("lastTransitionTime was not parsed")
	}
	if app.Replicas.Desired != 3 {
		t.Errorf("desired replicas = %d, want 3", app.Replicas.Desired)
	}
	if app.Image.Desired != "nginx:1.27-alpine" {
		t.Errorf("desired image = %q", app.Image.Desired)
	}
	if app.Port != 80 {
		t.Errorf("port = %d, want 80", app.Port)
	}
	if app.Auto == nil || app.Auto.MaxReplicas != 6 || app.Auto.CPUThresholdPercent != 70 {
		t.Errorf("autoscaling = %+v", app.Auto)
	}
}

// A WebApp whose Deployment is not ready must not report Available.
func TestConvertReportsAnUnavailableCondition(t *testing.T) {
	store := newStore(t,
		[]runtime.Object{webappCR(testNS, "demo",
			withAvailable("False", "DeploymentNotReady", "1/3 replicas ready"))},
		nil,
	)

	app, _ := store.Get(testNS, "demo")
	if app.Available {
		t.Error("Available = true for a False condition")
	}
	if app.Condition.Message != "1/3 replicas ready" {
		t.Errorf("message = %q", app.Condition.Message)
	}
}

// Before the operator has reconciled, there is no status at all.
func TestConvertToleratesAMissingStatus(t *testing.T) {
	store := newStore(t, []runtime.Object{webappCR(testNS, "demo")}, nil)

	app, found := store.Get(testNS, "demo")
	if !found {
		t.Fatal("demo was not found")
	}
	if app.Available || app.Condition != nil {
		t.Errorf("expected no condition, got available=%v condition=%+v", app.Available, app.Condition)
	}
}

func TestEnrichFromDeploymentUsesTheOwnerReference(t *testing.T) {
	store := newStore(t,
		[]runtime.Object{webappCR(testNS, "demo")},
		[]runtime.Object{
			// A Deployment in the same namespace owned by something else must
			// not be picked up.
			ownedDeployment(testNS, "unrelated-deployment", "some-other-uid", 5, 5, "other:1.0"),
			ownedDeployment(testNS, "demo-deployment", testUID, 3, 2, "nginx:1.27-alpine"),
		},
	)

	app, _ := store.Get(testNS, "demo")
	if app.DeploymentName != "demo-deployment" {
		t.Errorf("deployment = %q, want demo-deployment", app.DeploymentName)
	}
	if app.Replicas.Ready != 2 {
		t.Errorf("ready = %d, want 2", app.Replicas.Ready)
	}
	if app.Replicas.Effective != 3 {
		t.Errorf("effective = %d, want 3", app.Replicas.Effective)
	}
	if app.Image.Deployed != "nginx:1.27-alpine" {
		t.Errorf("deployed image = %q", app.Image.Deployed)
	}
}

// With autoscaling on, the operator stops managing spec.replicas and the HPA
// owns it, so the effective count legitimately differs from what the user asked
// for. The API has to show both rather than pretend they are one number.
func TestEffectiveReplicasCanDifferFromDesiredUnderAutoscaling(t *testing.T) {
	store := newStore(t,
		[]runtime.Object{webappCR(testNS, "demo", withAutoscaling(2, 10, 70))},
		[]runtime.Object{ownedDeployment(testNS, "demo-deployment", testUID, 7, 7, "nginx:1.27-alpine")},
	)

	app, _ := store.Get(testNS, "demo")
	if app.Replicas.Desired != 3 {
		t.Errorf("desired = %d, want the 3 from the custom resource", app.Replicas.Desired)
	}
	if app.Replicas.Effective != 7 {
		t.Errorf("effective = %d, want the 7 the HPA set", app.Replicas.Effective)
	}
}

// Between creating the custom resource and the operator reconciling it there is
// no Deployment at all, and that must not break the read path.
func TestConvertToleratesAMissingDeployment(t *testing.T) {
	store := newStore(t, []runtime.Object{webappCR(testNS, "demo")}, nil)

	app, _ := store.Get(testNS, "demo")
	if app.DeploymentName != "" || app.Replicas.Ready != 0 || app.Image.Deployed != "" {
		t.Errorf("expected empty deployment data, got %+v", app)
	}
}

func TestGetReportsMissingResources(t *testing.T) {
	store := newStore(t, []runtime.Object{webappCR(testNS, "demo")}, nil)

	if _, found := store.Get(testNS, "does-not-exist"); found {
		t.Error("found a WebApp that does not exist")
	}
	if _, found := store.Get("other-namespace", "demo"); found {
		t.Error("found demo in the wrong namespace")
	}
}

func TestHasSyncedReportsTrueOnceStarted(t *testing.T) {
	store := newStore(t, []runtime.Object{webappCR(testNS, "demo")}, nil)
	if !store.HasSynced() {
		t.Error("HasSynced = false after a successful Start")
	}
}
