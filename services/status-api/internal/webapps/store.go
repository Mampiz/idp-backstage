package webapps

import (
	"context"
	"fmt"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	appslisters "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"
)

// GVR is the group/version/resource of the operator's custom resource. The
// resource is consumed dynamically and on purpose: this service does not import
// the operator's Go types, so it stays decoupled from that repository and keeps
// working across changes to its module layout.
var GVR = schema.GroupVersionResource{
	Group:    "platform.miportfolio.com",
	Version:  "v1",
	Resource: "webapps",
}

// Store keeps a live, in-memory view of the WebApp custom resources and of the
// Deployments the operator creates for them.
//
// It is backed by informers rather than by listing on every request: a watch
// keeps the cache current, so reads are local and the API server is not asked
// anything on the hot path.
type Store struct {
	webappInformer     cache.SharedIndexInformer
	deploymentInformer cache.SharedIndexInformer
	deployments        appslisters.DeploymentLister

	dynamicFactory dynamicinformer.DynamicSharedInformerFactory
	typedFactory   informers.SharedInformerFactory
}

// NewStore wires the informers. Nothing is started until Start is called.
//
// WebApps go through the dynamic client; Deployments go through the typed one.
// The custom resource has to be dynamic (there are no generated types for it on
// this side), but Deployments are a core type and reading readyReplicas out of
// an unstructured map would be noise for no benefit.
func NewStore(dyn dynamic.Interface, typed kubernetes.Interface, resync time.Duration) *Store {
	dynamicFactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(dyn, resync, metav1.NamespaceAll, nil)
	typedFactory := informers.NewSharedInformerFactory(typed, resync)

	deployments := typedFactory.Apps().V1().Deployments()

	return &Store{
		webappInformer:     dynamicFactory.ForResource(GVR).Informer(),
		deploymentInformer: deployments.Informer(),
		deployments:        deployments.Lister(),
		dynamicFactory:     dynamicFactory,
		typedFactory:       typedFactory,
	}
}

// Start runs the informers and blocks until their caches are in sync.
func (s *Store) Start(ctx context.Context) error {
	s.dynamicFactory.Start(ctx.Done())
	s.typedFactory.Start(ctx.Done())

	for _, synced := range []cache.InformerSynced{
		s.webappInformer.HasSynced,
		s.deploymentInformer.HasSynced,
	} {
		if !cache.WaitForCacheSync(ctx.Done(), synced) {
			return fmt.Errorf("informer caches did not sync")
		}
	}
	return nil
}

// HasSynced reports whether both caches are populated. It backs the readiness
// probe: serving an empty list from a cold cache would look like "no WebApps"
// rather than "not ready yet".
func (s *Store) HasSynced() bool {
	return s.webappInformer.HasSynced() && s.deploymentInformer.HasSynced()
}

// List returns every WebApp in the cache, sorted by namespace and name so the
// output is stable between calls.
func (s *Store) List() []WebApp {
	objects := s.webappInformer.GetStore().List()
	out := make([]WebApp, 0, len(objects))
	for _, obj := range objects {
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			continue
		}
		out = append(out, s.convert(u))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Get returns one WebApp by namespace and name.
func (s *Store) Get(namespace, name string) (WebApp, bool) {
	obj, exists, err := s.webappInformer.GetStore().GetByKey(namespace + "/" + name)
	if err != nil || !exists {
		return WebApp{}, false
	}
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return WebApp{}, false
	}
	return s.convert(u), true
}

func (s *Store) convert(u *unstructured.Unstructured) WebApp {
	app := WebApp{
		Name:              u.GetName(),
		Namespace:         u.GetNamespace(),
		CreationTimestamp: u.GetCreationTimestamp().Time,
	}

	app.Image.Desired, _, _ = unstructured.NestedString(u.Object, "spec", "image")
	if replicas, found, _ := unstructured.NestedInt64(u.Object, "spec", "replicas"); found {
		app.Replicas.Desired = int32(replicas)
	}
	if port, found, _ := unstructured.NestedInt64(u.Object, "spec", "port"); found {
		app.Port = int32(port)
	}
	if auto, found, _ := unstructured.NestedMap(u.Object, "spec", "autoscaling"); found {
		app.Auto = &Autoscaling{
			MinReplicas:         nestedInt32(auto, "minReplicas"),
			MaxReplicas:         nestedInt32(auto, "maxReplicas"),
			CPUThresholdPercent: nestedInt32(auto, "cpuThresholdPercent"),
		}
	}

	app.Condition = availableCondition(u)
	app.Available = app.Condition != nil && app.Condition.Status == string(metav1.ConditionTrue)

	s.enrichFromDeployment(&app, u.GetUID())
	return app
}

// enrichFromDeployment fills in what only the workload knows: how many pods are
// actually ready and which image they are actually running.
//
// The Deployment is matched by owner reference rather than by name. The operator
// currently names it "<webapp>-deployment", but the ownership link is the real
// contract and it does not break if that naming ever changes.
func (s *Store) enrichFromDeployment(app *WebApp, owner types.UID) {
	deployments, err := s.deployments.Deployments(app.Namespace).List(labels.Everything())
	if err != nil {
		return
	}
	for _, deploy := range deployments {
		if !ownedBy(deploy, owner) {
			continue
		}
		app.DeploymentName = deploy.Name
		app.Replicas.Ready = deploy.Status.ReadyReplicas
		if deploy.Spec.Replicas != nil {
			app.Replicas.Effective = *deploy.Spec.Replicas
		}
		if containers := deploy.Spec.Template.Spec.Containers; len(containers) > 0 {
			app.Image.Deployed = containers[0].Image
		}
		return
	}
}

func ownedBy(deploy *appsv1.Deployment, owner types.UID) bool {
	for _, ref := range deploy.GetOwnerReferences() {
		if ref.UID == owner {
			return true
		}
	}
	return false
}

func availableCondition(u *unstructured.Unstructured) *Condition {
	conditions, found, err := unstructured.NestedSlice(u.Object, "status", "conditions")
	if !found || err != nil {
		return nil
	}
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if condition["type"] != "Available" {
			continue
		}
		out := &Condition{}
		out.Status, _ = condition["status"].(string)
		out.Reason, _ = condition["reason"].(string)
		out.Message, _ = condition["message"].(string)
		if raw, ok := condition["lastTransitionTime"].(string); ok {
			if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
				out.LastTransitionTime = parsed
			}
		}
		return out
	}
	return nil
}

func nestedInt32(m map[string]any, key string) int32 {
	switch v := m[key].(type) {
	case int64:
		return int32(v)
	case int32:
		return v
	case float64:
		return int32(v)
	}
	return 0
}
