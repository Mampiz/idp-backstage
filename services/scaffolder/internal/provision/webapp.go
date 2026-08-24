package provision

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// WebAppGVR is the custom resource this service applies. As in the status API,
// it is handled dynamically so this repository imports nothing from the
// operator's Go module.
var WebAppGVR = schema.GroupVersionResource{
	Group:    "platform.miportfolio.com",
	Version:  "v1",
	Resource: "webapps",
}

// fieldManager identifies this service in server-side apply, so the ownership of
// the fields it sets is visible in managedFields and conflicts are real errors
// rather than silent overwrites.
const fieldManager = "idp-scaffolder"

// WebAppSpec is the custom resource to apply.
type WebAppSpec struct {
	Namespace string
	Name      string
	Image     string
	Port      int32
	Replicas  int32
	// RepoURL is recorded on the resource so anything looking at the cluster can
	// find the code that produced it.
	RepoURL string
}

// WebAppOutcome says what happened to the custom resource.
type WebAppOutcome struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Applied   bool   `json:"applied"`
}

// Cluster applies WebApp custom resources.
type Cluster struct {
	dynamic dynamic.Interface
	typed   kubernetes.Interface
}

// NewCluster builds a Cluster from the two clients.
func NewCluster(dyn dynamic.Interface, typed kubernetes.Interface) *Cluster {
	return &Cluster{dynamic: dyn, typed: typed}
}

// Apply creates or updates the custom resource with a server-side apply, which
// makes repeating the same request a no-op rather than a conflict.
func (c *Cluster) Apply(ctx context.Context, spec WebAppSpec) (WebAppOutcome, error) {
	if err := c.ensureNamespace(ctx, spec.Namespace); err != nil {
		return WebAppOutcome{}, err
	}

	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": WebAppGVR.GroupVersion().String(),
		"kind":       "WebApp",
		"metadata": map[string]any{
			"name":      spec.Name,
			"namespace": spec.Namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": fieldManager,
				"app.kubernetes.io/part-of":    "idp",
			},
			"annotations": map[string]any{
				"platform.miportfolio.com/source-repository": spec.RepoURL,
			},
		},
		"spec": map[string]any{
			"image":    spec.Image,
			"replicas": int64(spec.Replicas),
			"port":     int64(spec.Port),
		},
	}}

	_, err := c.dynamic.Resource(WebAppGVR).Namespace(spec.Namespace).Apply(
		ctx, spec.Name, object, metav1.ApplyOptions{FieldManager: fieldManager, Force: true})
	if err != nil {
		return WebAppOutcome{Namespace: spec.Namespace, Name: spec.Name},
			fmt.Errorf("applying WebApp %s/%s: %w", spec.Namespace, spec.Name, err)
	}

	return WebAppOutcome{Namespace: spec.Namespace, Name: spec.Name, Applied: true}, nil
}

func (c *Cluster) ensureNamespace(ctx context.Context, name string) error {
	_, err := c.typed.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("looking up namespace %q: %w", name, err)
	}

	_, err = c.typed.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"app.kubernetes.io/part-of": "idp"},
		},
	}, metav1.CreateOptions{})
	// Losing a race to create it is not a failure.
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating namespace %q: %w", name, err)
	}
	return nil
}
