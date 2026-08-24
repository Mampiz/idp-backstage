// Package webapps turns the WebApp custom resources living in the cluster into
// a shape that is useful to a UI.
package webapps

import "time"

// Replicas separates the three different replica counts that exist for a
// WebApp, which are genuinely different numbers and are routinely conflated.
type Replicas struct {
	// Desired is spec.replicas on the WebApp: what the user asked for.
	Desired int32 `json:"desired"`
	// Effective is spec.replicas on the Deployment. When autoscaling is enabled
	// the operator deliberately stops managing it and the HPA owns it, so this
	// can legitimately differ from Desired.
	Effective int32 `json:"effective"`
	// Ready is how many pods are actually serving.
	Ready int32 `json:"ready"`
}

// Image separates the image the user asked for from the one really running.
// They differ while a rollout is in flight, or if the Deployment was edited
// out of band.
type Image struct {
	Desired  string `json:"desired"`
	Deployed string `json:"deployed,omitempty"`
}

// Autoscaling mirrors the optional autoscaling block of the custom resource.
type Autoscaling struct {
	MinReplicas         int32 `json:"minReplicas"`
	MaxReplicas         int32 `json:"maxReplicas"`
	CPUThresholdPercent int32 `json:"cpuThresholdPercent"`
}

// Condition is the Available condition reported by the operator.
type Condition struct {
	Status             string    `json:"status"`
	Reason             string    `json:"reason,omitempty"`
	Message            string    `json:"message,omitempty"`
	LastTransitionTime time.Time `json:"lastTransitionTime,omitzero"`
}

// WebApp is the summarised view of one custom resource.
type WebApp struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// Available is the operator's Available condition reduced to a boolean, for
	// the common case where a caller only wants a green or red light.
	Available bool         `json:"available"`
	Condition *Condition   `json:"condition,omitempty"`
	Replicas  Replicas     `json:"replicas"`
	Image     Image        `json:"image"`
	Port      int32        `json:"port"`
	Auto      *Autoscaling `json:"autoscaling,omitempty"`
	// DeploymentName is the workload the operator created for this resource.
	DeploymentName    string    `json:"deploymentName,omitempty"`
	CreationTimestamp time.Time `json:"creationTimestamp,omitzero"`
}

// List is the response body of the collection endpoint.
type List struct {
	Items []WebApp `json:"items"`
	Count int      `json:"count"`
}
