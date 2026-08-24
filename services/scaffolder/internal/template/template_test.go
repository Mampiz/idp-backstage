package template

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func params() Params {
	return Params{
		Name:        "my-api",
		Owner:       "Mampiz",
		Description: "A scaffolded service.",
		Image:       "ghcr.io/mampiz/my-api:0.1.0",
		Namespace:   "idp-apps",
		Port:        8080,
		Replicas:    2,
	}
}

func TestRenderProducesEveryExpectedFile(t *testing.T) {
	out, err := Render(params())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	for _, want := range []string{
		"main.go",
		"main_test.go",
		"metrics.go",
		"go.mod",
		"Dockerfile",
		"Makefile",
		"README.md",
		"catalog-info.yaml",
		"webapp.yaml",
		".gitignore",
		".github/workflows/ci.yml",
	} {
		if _, ok := out[want]; !ok {
			t.Errorf("missing %s from the rendered output", want)
		}
	}
}

func TestRenderInterpolatesTheParameters(t *testing.T) {
	out, err := Render(params())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if got := string(out["go.mod"]); !strings.Contains(got, "module github.com/Mampiz/my-api") {
		t.Errorf("go.mod = %q", got)
	}
	webapp := string(out["webapp.yaml"])
	for _, want := range []string{"name: my-api", "namespace: idp-apps", "image: ghcr.io/mampiz/my-api:0.1.0", "replicas: 2", "port: 8080"} {
		if !strings.Contains(webapp, want) {
			t.Errorf("webapp.yaml is missing %q:\n%s", want, webapp)
		}
	}
	if got := string(out["catalog-info.yaml"]); !strings.Contains(got, "platform.miportfolio.com/webapp: idp-apps/my-api") {
		t.Errorf("catalog-info.yaml is missing the webapp annotation:\n%s", got)
	}
}

// The workflow has to keep GitHub Actions expressions intact rather than let
// text/template eat them.
func TestRenderKeepsGitHubActionsExpressions(t *testing.T) {
	out, err := Render(params())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	workflow := string(out[".github/workflows/ci.yml"])
	for _, want := range []string{
		"ghcr.io/${{ github.repository }}",
		"${{ github.actor }}",
		"${{ secrets.GITHUB_TOKEN }}",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("workflow lost the expression %q:\n%s", want, workflow)
		}
	}
}

// Prometheus metric names cannot contain hyphens, and service names routinely do.
func TestMetricPrefixIsAValidMetricName(t *testing.T) {
	p := params()
	if got := p.MetricPrefix(); got != "my_api" {
		t.Errorf("MetricPrefix() = %q, want my_api", got)
	}

	p.Name = "weird.name-with/chars"
	if got := p.MetricPrefix(); got != "weird_name_with_chars" {
		t.Errorf("MetricPrefix() = %q", got)
	}
}

func TestRenderRequiresNameAndOwner(t *testing.T) {
	if _, err := Render(Params{Owner: "Mampiz"}); err == nil {
		t.Error("expected an error without a name")
	}
	if _, err := Render(Params{Name: "my-api"}); err == nil {
		t.Error("expected an error without an owner")
	}
}

// The point of the template is that what it produces actually works, so the
// rendered service is compiled and its own tests are run.
func TestRenderedServiceBuildsAndPassesItsTests(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles the rendered service; skipped in short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain available")
	}

	out, err := Render(params())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	dir := t.TempDir()
	for path, content := range out {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}

	for _, args := range [][]string{
		{"vet", "./..."},
		{"test", "./..."},
	} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		// The rendered service has no dependencies, so this must not need the
		// network; GOFLAGS=-mod=mod keeps it from reaching for a proxy.
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s failed on the rendered service: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
}
