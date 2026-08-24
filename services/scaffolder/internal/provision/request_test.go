package provision

import (
	"strings"
	"testing"
)

func TestNormaliseAppliesDefaults(t *testing.T) {
	req := Request{Name: "my-api", Image: "ghcr.io/x/y:1.0", Port: 8080, Replicas: 1}

	if err := req.Normalise("Mampiz", "idp-apps"); err != nil {
		t.Fatalf("Normalise: %v", err)
	}
	if req.Owner != "Mampiz" {
		t.Errorf("owner = %q", req.Owner)
	}
	if req.Namespace != "idp-apps" {
		t.Errorf("namespace = %q", req.Namespace)
	}
}

// Backstage's RepoUrlPicker produces "github.com?owner=X&repo=Y", which is the
// format the software template will send.
func TestNormaliseUnderstandsTheRepoUrlPickerFormat(t *testing.T) {
	req := Request{RepoURL: "github.com?owner=Mampiz&repo=my-api", Image: "ghcr.io/x/y:1.0", Port: 80, Replicas: 1}

	if err := req.Normalise("", "idp-apps"); err != nil {
		t.Fatalf("Normalise: %v", err)
	}
	if req.Owner != "Mampiz" || req.Name != "my-api" {
		t.Errorf("owner = %q, name = %q", req.Owner, req.Name)
	}
}

func TestNormaliseUnderstandsPlainRepositoryURLs(t *testing.T) {
	for _, raw := range []string{
		"https://github.com/Mampiz/my-api",
		"https://github.com/Mampiz/my-api.git",
		"git@github.com:Mampiz/my-api.git",
	} {
		req := Request{RepoURL: raw, Image: "ghcr.io/x/y:1.0", Port: 80, Replicas: 1}
		if err := req.Normalise("", "idp-apps"); err != nil {
			t.Fatalf("Normalise(%q): %v", raw, err)
		}
		if req.Owner != "Mampiz" || req.Name != "my-api" {
			t.Errorf("%q gave owner = %q, name = %q", raw, req.Owner, req.Name)
		}
	}
}

// The operator's webhook rejects these, so they have to be rejected here first,
// before a repository exists with no custom resource behind it.
func TestNormaliseRejectsImagesTheOperatorWouldReject(t *testing.T) {
	for _, image := range []string{"nginx:latest", "nginx", "ghcr.io/x/y", "ghcr.io/x/y:", "registry:5000/x/y"} {
		req := Request{Name: "my-api", Image: image, Port: 80, Replicas: 1}
		err := req.Normalise("Mampiz", "idp-apps")
		if err == nil {
			t.Errorf("image %q was accepted, but the operator would reject it", image)
		}
	}
}

func TestNormaliseAcceptsPinnedImages(t *testing.T) {
	for _, image := range []string{
		"nginx:1.27-alpine",
		"ghcr.io/mampiz/my-api:0.1.0",
		"registry:5000/x/y:2.3",
		"nginx@sha256:0000000000000000000000000000000000000000000000000000000000000000",
	} {
		req := Request{Name: "my-api", Image: image, Port: 80, Replicas: 1}
		if err := req.Normalise("Mampiz", "idp-apps"); err != nil {
			t.Errorf("image %q was rejected: %v", image, err)
		}
	}
}

func TestNormaliseRejectsNamesThatCannotBeKubernetesObjects(t *testing.T) {
	for _, name := range []string{"My-API", "my_api", "-leading", "trailing-", "", strings.Repeat("a", 64)} {
		req := Request{Name: name, Image: "ghcr.io/x/y:1.0", Port: 80, Replicas: 1}
		if err := req.Normalise("Mampiz", "idp-apps"); err == nil {
			t.Errorf("name %q was accepted", name)
		}
	}
}

func TestNormaliseRejectsPortsOutOfRange(t *testing.T) {
	for _, port := range []int32{0, -1, 65536} {
		req := Request{Name: "my-api", Image: "ghcr.io/x/y:1.0", Port: port, Replicas: 1}
		if err := req.Normalise("Mampiz", "idp-apps"); err == nil {
			t.Errorf("port %d was accepted", port)
		}
	}
}

// Every problem at once, so a form is not resubmitted one mistake at a time.
func TestNormaliseReportsEveryProblemTogether(t *testing.T) {
	req := Request{Name: "Bad Name", Image: "nginx:latest", Port: 0, Replicas: -1}

	err := req.Normalise("Mampiz", "idp-apps")
	invalid, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("err = %v, want a ValidationError", err)
	}
	if len(invalid.Problems) < 4 {
		t.Errorf("problems = %v, want one for each mistake", invalid.Problems)
	}
}
