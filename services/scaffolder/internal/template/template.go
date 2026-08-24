// Package template renders the source of a scaffolded service.
//
// The files are embedded in the binary rather than fetched from a template
// repository at run time: the scaffolder then has no network dependency for
// this step, and the exact content it produces is pinned to the version of the
// service that is running.
package template

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"text/template"
)

// all: is required so .github/workflows is included; embed skips paths
// beginning with a dot by default.
//
//go:embed all:files
var files embed.FS

// Params is everything the templates interpolate.
type Params struct {
	// Name is the repository and service name.
	Name string
	// Owner is the GitHub account the repository lives under.
	Owner string
	// Description is a one-line summary shown in GitHub and in the catalog.
	Description string
	// Image is the container image the WebApp will run.
	Image string
	// Namespace is where the WebApp custom resource is applied.
	Namespace string
	// DefaultBranch is the branch CI runs on.
	DefaultBranch string
	// CatalogOwner is the Backstage entity reference owning the component.
	CatalogOwner string
	// Port is the port the service listens on.
	Port int32
	// Replicas is the desired replica count.
	Replicas int32
}

// MetricPrefix is the service name turned into a valid Prometheus metric prefix.
// Metric names allow only [a-zA-Z0-9_:], so hyphens have to go.
func (p Params) MetricPrefix() string {
	return invalidMetricChars.ReplaceAllString(p.Name, "_")
}

var invalidMetricChars = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// renamed maps template file names to their final name, for files that cannot
// be stored under their real name in the repository.
var renamed = map[string]string{
	// A .gitignore inside the embedded tree would apply to this repository's own
	// tooling, so it is stored without the leading dot.
	"gitignore": ".gitignore",
}

// Render returns the files of the scaffolded repository, keyed by path.
func Render(p Params) (map[string][]byte, error) {
	if p.Name == "" || p.Owner == "" {
		return nil, fmt.Errorf("template: name and owner are required")
	}
	if p.DefaultBranch == "" {
		p.DefaultBranch = "main"
	}
	if p.CatalogOwner == "" {
		p.CatalogOwner = "group:default/platform"
	}
	if p.Description == "" {
		p.Description = fmt.Sprintf("Scaffolded Go service %s.", p.Name)
	}

	out := map[string][]byte{}
	err := fs.WalkDir(files, "files", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}

		raw, err := files.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		tmpl, err := template.New(path).Parse(string(raw))
		if err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		var rendered bytes.Buffer
		if err := tmpl.Execute(&rendered, p); err != nil {
			return fmt.Errorf("rendering %s: %w", path, err)
		}

		out[targetPath(path)] = rendered.Bytes()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func targetPath(path string) string {
	trimmed := strings.TrimPrefix(path, "files/")
	trimmed = strings.TrimSuffix(trimmed, ".tmpl")
	if final, ok := renamed[trimmed]; ok {
		return final
	}
	return trimmed
}
