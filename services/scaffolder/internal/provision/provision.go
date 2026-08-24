// Package provision turns one request into a GitHub repository and a running
// WebApp custom resource.
package provision

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Mampiz/idp-backstage/services/scaffolder/internal/template"
)

// RepositoryProvisioner creates repositories and marks them.
type RepositoryProvisioner interface {
	EnsureRepository(ctx context.Context, spec RepoSpec) (RepoOutcome, error)
	SetTopics(ctx context.Context, owner, name string, add, remove []string) error
}

// WebAppProvisioner applies the custom resource.
type WebAppProvisioner interface {
	Apply(ctx context.Context, spec WebAppSpec) (WebAppOutcome, error)
}

// Result is the body of a successful POST /scaffold.
type Result struct {
	Name       string        `json:"name"`
	Repository RepoOutcome   `json:"repository"`
	WebApp     WebAppOutcome `json:"webapp"`
}

// PartialError is returned when the repository exists but the custom resource
// could not be applied.
//
// The repository is deliberately NOT deleted. Deleting a GitHub repository is
// irreversible and, if the name happened to already be in use, it would destroy
// somebody's work to tidy up after our own failure. Instead the run stops in a
// state that is explicit and resumable: the repository is tagged with the
// "idp-provisioning-incomplete" topic, the response names the step that failed,
// and re-sending the same request finishes the job, because every step is
// idempotent.
type PartialError struct {
	Repository RepoOutcome
	Step       string
	Err        error
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("provisioning stopped at %s: %v", e.Step, e.Err)
}

func (e *PartialError) Unwrap() error { return e.Err }

// Service runs the provisioning steps.
type Service struct {
	repos            RepositoryProvisioner
	cluster          WebAppProvisioner
	logger           *slog.Logger
	defaultOwner     string
	defaultNamespace string
	private          bool
}

// Options configures a Service.
type Options struct {
	DefaultOwner     string
	DefaultNamespace string
	// Private creates repositories as private.
	Private bool
}

// NewService wires a Service.
func NewService(repos RepositoryProvisioner, cluster WebAppProvisioner, logger *slog.Logger, opts Options) *Service {
	if opts.DefaultNamespace == "" {
		opts.DefaultNamespace = "idp-apps"
	}
	return &Service{
		repos:            repos,
		cluster:          cluster,
		logger:           logger,
		defaultOwner:     opts.DefaultOwner,
		defaultNamespace: opts.DefaultNamespace,
		private:          opts.Private,
	}
}

// Scaffold validates the request, creates the repository and applies the custom
// resource, in that order.
//
// The order matters: the repository is the artefact a human keeps, so it is
// created first and the cluster resource, which is cheap to recreate, second.
func (s *Service) Scaffold(ctx context.Context, req Request) (Result, error) {
	if err := req.Normalise(s.defaultOwner, s.defaultNamespace); err != nil {
		return Result{}, err
	}

	files, err := template.Render(template.Params{
		Name:         req.Name,
		Owner:        req.Owner,
		Description:  req.Description,
		Image:        req.Image,
		Namespace:    req.Namespace,
		CatalogOwner: req.CatalogOwner,
		Port:         req.Port,
		Replicas:     req.Replicas,
	})
	if err != nil {
		return Result{}, fmt.Errorf("rendering the template: %w", err)
	}

	repo, err := s.repos.EnsureRepository(ctx, RepoSpec{
		Owner:         req.Owner,
		Name:          req.Name,
		Description:   req.Description,
		Private:       s.private,
		Files:         files,
		CommitMessage: fmt.Sprintf("Scaffold %s\n\nCreated by the IDP scaffolder, together with the WebApp\ncustom resource that runs it in the cluster.", req.Name),
	})
	if err != nil {
		return Result{}, fmt.Errorf("provisioning the repository: %w", err)
	}
	s.logger.Info("repository ready", "repo", repo.URL, "created", repo.Created, "contentPushed", repo.ContentPushed)

	app, err := s.cluster.Apply(ctx, WebAppSpec{
		Namespace: req.Namespace,
		Name:      req.Name,
		Image:     req.Image,
		Port:      req.Port,
		Replicas:  req.Replicas,
		RepoURL:   repo.URL,
	})
	if err != nil {
		// Mark the repository so the half-finished state is visible from GitHub
		// itself. A failure to mark it must not hide the original error.
		if markErr := s.repos.SetTopics(ctx, req.Owner, req.Name,
			[]string{TopicIncomplete}, []string{TopicManaged}); markErr != nil {
			s.logger.Error("could not mark the repository as incomplete", "error", markErr)
		}
		return Result{Name: req.Name, Repository: repo},
			&PartialError{Repository: repo, Step: "webapp", Err: err}
	}

	if err := s.repos.SetTopics(ctx, req.Owner, req.Name,
		[]string{TopicManaged}, []string{TopicIncomplete}); err != nil {
		// Everything real succeeded; a missing topic is not worth failing over.
		s.logger.Warn("could not mark the repository as managed", "error", err)
	}

	return Result{Name: req.Name, Repository: repo, WebApp: app}, nil
}
