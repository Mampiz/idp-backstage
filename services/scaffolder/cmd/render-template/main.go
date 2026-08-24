// Command render-template writes the scaffolded service template to a directory.
//
// It exists so the F6 verifier can build the documentation a generated service
// ships with, without creating a repository to look at.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Mampiz/idp-backstage/services/scaffolder/internal/template"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: render-template <directory>")
		os.Exit(2)
	}

	files, err := template.Render(template.Params{
		Name:        "rendered-service",
		Owner:       "Mampiz",
		Description: "Rendered to check the template's output.",
		Image:       "ghcr.io/mampiz/rendered-service:0.1.0",
		Namespace:   "idp-apps",
		Port:        8080,
		Replicas:    2,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	for path, content := range files {
		full := filepath.Join(os.Args[1], path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("rendered %d files to %s\n", len(files), os.Args[1])
}
