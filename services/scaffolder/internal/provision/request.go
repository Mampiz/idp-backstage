package provision

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Request is the body of POST /scaffold.
type Request struct {
	// Name is the repository and service name. It also becomes the name of the
	// WebApp custom resource, so it has to be a valid DNS label.
	Name string `json:"name"`
	// Owner is the GitHub account. Optional: falls back to the configured owner,
	// or to whatever RepoURL carries.
	Owner string `json:"owner,omitempty"`
	// RepoURL accepts what Backstage's RepoUrlPicker produces
	// ("github.com?owner=X&repo=Y") as well as a plain repository URL.
	RepoURL string `json:"repoUrl,omitempty"`
	// Image is the container image the WebApp runs. It must carry an explicit,
	// non-latest tag.
	Image string `json:"image"`
	// Port is the port the service listens on.
	Port int32 `json:"port"`
	// Replicas is the desired replica count.
	Replicas int32 `json:"replicas"`
	// Description is a one-line summary. Optional.
	Description string `json:"description,omitempty"`
	// Namespace is where the custom resource is applied. Optional.
	Namespace string `json:"namespace,omitempty"`
	// CatalogOwner is the Backstage entity that owns the component. Optional.
	CatalogOwner string `json:"catalogOwner,omitempty"`
}

// ValidationError carries every problem found with a request, so a form can show
// them all at once instead of one per round trip.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "invalid request: " + strings.Join(e.Problems, "; ")
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// Normalise fills in defaults and validates. defaultOwner and defaultNamespace
// come from the service configuration.
func (r *Request) Normalise(defaultOwner, defaultNamespace string) error {
	var problems []string

	r.Name = strings.TrimSpace(r.Name)
	r.Owner = strings.TrimSpace(r.Owner)
	r.Image = strings.TrimSpace(r.Image)

	if owner, repo, ok := parseRepoURL(r.RepoURL); ok {
		if r.Owner == "" {
			r.Owner = owner
		}
		if r.Name == "" {
			r.Name = repo
		}
	}
	if r.Owner == "" {
		r.Owner = defaultOwner
	}
	if r.Namespace == "" {
		r.Namespace = defaultNamespace
	}

	switch {
	case r.Name == "":
		problems = append(problems, "name is required")
	case len(r.Name) > 63:
		problems = append(problems, "name must be at most 63 characters")
	case !dnsLabel.MatchString(r.Name):
		problems = append(problems, fmt.Sprintf("name %q must be a lowercase DNS label (letters, digits and hyphens, not starting or ending with a hyphen)", r.Name))
	}

	if r.Owner == "" {
		problems = append(problems, "owner is required and no default is configured")
	}

	if err := validateImage(r.Image); err != nil {
		problems = append(problems, err.Error())
	}

	if r.Port < 1 || r.Port > 65535 {
		problems = append(problems, fmt.Sprintf("port %d must be between 1 and 65535", r.Port))
	}
	if r.Replicas < 0 {
		problems = append(problems, fmt.Sprintf("replicas %d cannot be negative", r.Replicas))
	}

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

// validateImage enforces the same rule as the operator's validating webhook.
//
// This check exists here so the request is rejected before anything is created.
// Without it a repository would be created and then the custom resource refused
// by admission, leaving exactly the half-finished state this service is built
// to avoid.
func validateImage(image string) error {
	if image == "" {
		return errors.New("image is required")
	}

	// A digest-pinned reference is immutable and always acceptable.
	if strings.Contains(image, "@sha256:") {
		return nil
	}

	// Strip a registry host with a port so its colon is not read as a tag.
	nameStart := image
	if slash := strings.LastIndex(image, "/"); slash != -1 {
		nameStart = image[slash+1:]
	}
	colon := strings.LastIndex(nameStart, ":")
	if colon == -1 {
		return fmt.Errorf("image %q must specify an explicit tag; the operator rejects implicit tags", image)
	}
	tag := nameStart[colon+1:]
	if tag == "" {
		return fmt.Errorf("image %q has an empty tag", image)
	}
	if tag == "latest" {
		return fmt.Errorf("image %q uses the mutable \"latest\" tag; the operator rejects it, pin an explicit version", image)
	}
	return nil
}

// parseRepoURL understands Backstage's RepoUrlPicker format
// ("github.com?owner=X&repo=Y") and ordinary repository URLs.
func parseRepoURL(raw string) (owner, repo string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}

	if strings.Contains(raw, "?") && !strings.Contains(raw, "://") {
		parsed, err := url.Parse("//" + raw)
		if err != nil {
			return "", "", false
		}
		query := parsed.Query()
		return query.Get("owner"), strings.TrimSuffix(query.Get("repo"), ".git"), true
	}

	trimmed := strings.TrimSuffix(raw, ".git")
	trimmed = strings.TrimPrefix(trimmed, "https://")
	trimmed = strings.TrimPrefix(trimmed, "http://")
	trimmed = strings.TrimPrefix(trimmed, "git@")
	trimmed = strings.ReplaceAll(trimmed, ":", "/")

	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) < 3 {
		return "", "", false
	}
	return parts[len(parts)-2], parts[len(parts)-1], true
}
