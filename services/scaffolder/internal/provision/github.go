package provision

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/go-github/v77/github"
)

// Topic markers put on the repository so the outcome of a provisioning run is
// discoverable from GitHub itself, without consulting this service.
const (
	// TopicManaged marks a repository the platform provisioned end to end.
	TopicManaged = "idp-managed"
	// TopicIncomplete marks a repository whose custom resource was never
	// applied. Re-running the same request clears it.
	TopicIncomplete = "idp-provisioning-incomplete"
)

// RepoSpec describes the repository to create.
type RepoSpec struct {
	Owner       string
	Name        string
	Description string
	Private     bool
	Files       map[string][]byte
	// CommitMessage is the message of the single initial commit.
	CommitMessage string
	Branch        string
}

// RepoOutcome says what happened to the repository.
type RepoOutcome struct {
	URL           string `json:"url"`
	DefaultBranch string `json:"defaultBranch"`
	// Created is false when the repository was already there, which is the
	// normal case for a retried request.
	Created bool `json:"created"`
	// ContentPushed is false when the repository already had commits and was
	// therefore left alone.
	ContentPushed bool `json:"contentPushed"`
}

// GitHub creates repositories and pushes the scaffolded content into them.
type GitHub struct {
	client *github.Client
}

// NewGitHub builds a GitHub from an authenticated client.
func NewGitHub(client *github.Client) *GitHub {
	return &GitHub{client: client}
}

// EnsureRepository creates the repository if it does not exist and pushes the
// scaffolded files as a single initial commit.
//
// It is safe to call repeatedly. An existing repository is not recreated, and
// content is only pushed into a repository that has no commits yet, so a retry
// never overwrites work someone has since done.
func (g *GitHub) EnsureRepository(ctx context.Context, spec RepoSpec) (RepoOutcome, error) {
	if spec.Branch == "" {
		spec.Branch = "main"
	}

	repo, existed, err := g.ensureRepo(ctx, spec)
	if err != nil {
		return RepoOutcome{}, err
	}

	outcome := RepoOutcome{
		URL:           repo.GetHTMLURL(),
		DefaultBranch: repo.GetDefaultBranch(),
		Created:       !existed,
	}
	if outcome.DefaultBranch == "" {
		outcome.DefaultBranch = spec.Branch
	}

	scaffolded, err := g.hasScaffoldedContent(ctx, spec.Owner, spec.Name)
	if err != nil {
		return outcome, err
	}
	if scaffolded {
		// A previous run already put the content here. Overwriting it would
		// throw away whatever has been done to the repository since.
		return outcome, nil
	}

	// The git data API refuses to create a tree in a repository with no
	// commits ("409 Git Repository is empty"), so a repository that exists but
	// is empty has to be bootstrapped through the contents API first. Newly
	// created repositories get their first commit from auto-init.
	empty, err := g.isEmpty(ctx, spec.Owner, spec.Name)
	if err != nil {
		return outcome, err
	}
	if empty {
		if err := g.bootstrap(ctx, spec, outcome.DefaultBranch); err != nil {
			return outcome, err
		}
	}

	// Right after a repository's first commit, the git data API can still answer
	// 404 or 409 for a moment before it catches up with the new state. That is a
	// timing artefact, not a real failure, so it is retried rather than surfaced
	// as a half-finished provisioning run.
	if err := retryWhileSettling(ctx, func() error {
		return g.pushCommit(ctx, spec, outcome.DefaultBranch)
	}); err != nil {
		return outcome, err
	}
	outcome.ContentPushed = true
	return outcome, nil
}

// markerFile is the file whose presence means the repository has already been
// scaffolded. It is one the platform always writes and nobody deletes casually.
const markerFile = "catalog-info.yaml"

func (g *GitHub) hasScaffoldedContent(ctx context.Context, owner, name string) (bool, error) {
	_, _, resp, err := g.client.Repositories.GetContents(ctx, owner, name, markerFile, nil)
	if err == nil {
		return true, nil
	}
	if resp != nil && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusConflict) {
		return false, nil
	}
	return false, fmt.Errorf("checking %s/%s for %s: %w", owner, name, markerFile, err)
}

// bootstrap creates the very first commit through the contents API, which is
// the only endpoint that works against a repository with no commits.
func (g *GitHub) bootstrap(ctx context.Context, spec RepoSpec, branch string) error {
	readme := spec.Files["README.md"]
	if len(readme) == 0 {
		readme = []byte("# " + spec.Name + "\n")
	}

	_, _, err := g.client.Repositories.CreateFile(ctx, spec.Owner, spec.Name, "README.md",
		&github.RepositoryContentFileOptions{
			Message: github.Ptr("Initialise repository"),
			Content: readme,
			Branch:  github.Ptr(branch),
		})
	if err != nil {
		return fmt.Errorf("bootstrapping the empty repository: %w", err)
	}
	return nil
}

func (g *GitHub) ensureRepo(ctx context.Context, spec RepoSpec) (*github.Repository, bool, error) {
	repo, resp, err := g.client.Repositories.Get(ctx, spec.Owner, spec.Name)
	if err == nil {
		return repo, true, nil
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		return nil, false, fmt.Errorf("looking up %s/%s: %w", spec.Owner, spec.Name, err)
	}

	// An empty org string creates the repository under the authenticated user,
	// which is what a personal account needs.
	org := ""
	if isOrganization, err := g.isOrganization(ctx, spec.Owner); err != nil {
		return nil, false, err
	} else if isOrganization {
		org = spec.Owner
	}

	created, _, err := g.client.Repositories.Create(ctx, org, &github.Repository{
		Name:          github.Ptr(spec.Name),
		Description:   github.Ptr(spec.Description),
		Private:       github.Ptr(spec.Private),
		DefaultBranch: github.Ptr(spec.Branch),
		// Auto-init is required, not cosmetic: the git data API cannot create a
		// tree in a repository with no commits, so there has to be something to
		// build the scaffold commit on top of.
		AutoInit:  github.Ptr(true),
		HasIssues: github.Ptr(true),
	})
	if err != nil {
		return nil, false, fmt.Errorf("creating %s/%s: %w", spec.Owner, spec.Name, err)
	}
	return created, false, nil
}

func (g *GitHub) isOrganization(ctx context.Context, owner string) (bool, error) {
	user, _, err := g.client.Users.Get(ctx, owner)
	if err != nil {
		return false, fmt.Errorf("resolving owner %q: %w", owner, err)
	}
	return user.GetType() == "Organization", nil
}

// isEmpty reports whether the repository has no commits yet.
func (g *GitHub) isEmpty(ctx context.Context, owner, name string) (bool, error) {
	_, resp, err := g.client.Repositories.ListCommits(ctx, owner, name, &github.CommitsListOptions{
		ListOptions: github.ListOptions{PerPage: 1},
	})
	if err == nil {
		return false, nil
	}
	// A repository with no commits answers 409 Conflict ("Git Repository is
	// empty"), and one whose default branch does not exist yet answers 404.
	if resp != nil && (resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusNotFound) {
		return true, nil
	}
	return false, fmt.Errorf("checking whether %s/%s is empty: %w", owner, name, err)
}

// pushCommit writes every file in a single commit through the git data API,
// rather than one commit per file through the contents API.
func (g *GitHub) pushCommit(ctx context.Context, spec RepoSpec, branch string) error {
	ref, _, err := g.client.Git.GetRef(ctx, spec.Owner, spec.Name, "refs/heads/"+branch)
	if err != nil {
		return fmt.Errorf("reading refs/heads/%s: %w", branch, err)
	}
	parent := ref.GetObject().GetSHA()

	base, _, err := g.client.Git.GetCommit(ctx, spec.Owner, spec.Name, parent)
	if err != nil {
		return fmt.Errorf("reading the parent commit: %w", err)
	}

	entries := make([]*github.TreeEntry, 0, len(spec.Files))
	for _, path := range sortedFilePaths(spec.Files) {
		entries = append(entries, &github.TreeEntry{
			Path:    github.Ptr(path),
			Mode:    github.Ptr("100644"),
			Type:    github.Ptr("blob"),
			Content: github.Ptr(string(spec.Files[path])),
		})
	}
	if len(entries) == 0 {
		return errors.New("no files to commit")
	}

	tree, _, err := g.client.Git.CreateTree(ctx, spec.Owner, spec.Name, base.GetTree().GetSHA(), entries)
	if err != nil {
		return fmt.Errorf("creating the git tree: %w", explainWorkflowScope(err, entries))
	}

	message := spec.CommitMessage
	if message == "" {
		message = "Initial commit from the IDP scaffolder"
	}
	commit, _, err := g.client.Git.CreateCommit(ctx, spec.Owner, spec.Name, github.Commit{
		Message: github.Ptr(message),
		Tree:    tree,
		Parents: []*github.Commit{{SHA: github.Ptr(parent)}},
	}, nil)
	if err != nil {
		return fmt.Errorf("creating the scaffold commit: %w", err)
	}

	if _, _, err := g.client.Git.UpdateRef(ctx, spec.Owner, spec.Name, "refs/heads/"+branch,
		github.UpdateRef{SHA: commit.GetSHA()}); err != nil {
		return fmt.Errorf("updating refs/heads/%s: %w", branch, err)
	}
	return nil
}

// ErrWorkflowScope is returned when the token cannot write workflow files.
var ErrWorkflowScope = errors.New(
	"the GitHub token is missing the \"workflow\" scope, which is required to commit files under " +
		".github/workflows/. GitHub reports this as a bare 404 rather than a permission error. " +
		"Fix it with: gh auth refresh -h github.com -s workflow   (or use a PAT with repo + workflow)")

// explainWorkflowScope turns GitHub's unhelpful 404 into the actual cause.
//
// Writing a tree that contains anything under .github/workflows/ needs the
// "workflow" scope. Without it the git data API answers 404 Not Found with an
// empty body, which reads like the repository does not exist.
func explainWorkflowScope(err error, entries []*github.TreeEntry) error {
	var apiErr *github.ErrorResponse
	if !errors.As(err, &apiErr) || apiErr.Response == nil || apiErr.Response.StatusCode != http.StatusNotFound {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.GetPath(), ".github/workflows/") {
			return fmt.Errorf("%w (original error: %v)", ErrWorkflowScope, err)
		}
	}
	return err
}

// CheckTokenScopes reports whether the token can do everything the scaffolder
// needs, so the problem surfaces at startup instead of half way through
// provisioning somebody's repository.
//
// Fine-grained tokens do not report scopes at all; an empty header is treated as
// "cannot tell" rather than as a failure.
func (g *GitHub) CheckTokenScopes(ctx context.Context) error {
	_, resp, err := g.client.Users.Get(ctx, "")
	if err != nil {
		return fmt.Errorf("checking the GitHub token: %w", err)
	}
	scopes := resp.Header.Get("X-OAuth-Scopes")
	if scopes == "" {
		return nil
	}
	for _, scope := range strings.Split(scopes, ",") {
		if strings.TrimSpace(scope) == "workflow" {
			return nil
		}
	}
	return ErrWorkflowScope
}

// SetTopics replaces the platform's marker topics, preserving any others.
func (g *GitHub) SetTopics(ctx context.Context, owner, name string, add []string, remove []string) error {
	current, _, err := g.client.Repositories.ListAllTopics(ctx, owner, name)
	if err != nil {
		return fmt.Errorf("listing topics of %s/%s: %w", owner, name, err)
	}

	keep := map[string]bool{}
	for _, topic := range current {
		keep[topic] = true
	}
	for _, topic := range remove {
		delete(keep, topic)
	}
	for _, topic := range add {
		keep[topic] = true
	}

	topics := make([]string, 0, len(keep))
	for topic := range keep {
		topics = append(topics, topic)
	}
	sort.Strings(topics)

	if _, _, err := g.client.Repositories.ReplaceAllTopics(ctx, owner, name, topics); err != nil {
		return fmt.Errorf("setting topics on %s/%s: %w", owner, name, err)
	}
	return nil
}

// retryWhileSettling retries an operation while GitHub reports the repository
// state it needs as missing.
func retryWhileSettling(ctx context.Context, operation func() error) error {
	const attempts = 6

	var err error
	for attempt := range attempts {
		if err = operation(); err == nil {
			return nil
		}
		if !isSettling(err) {
			return err
		}
		delay := time.Duration(1<<attempt) * 250 * time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return fmt.Errorf("the repository was still settling after %d attempts: %w", attempts, err)
}

func isSettling(err error) bool {
	var apiErr *github.ErrorResponse
	if !errors.As(err, &apiErr) || apiErr.Response == nil {
		return false
	}
	return apiErr.Response.StatusCode == http.StatusNotFound ||
		apiErr.Response.StatusCode == http.StatusConflict
}

func sortedFilePaths(files map[string][]byte) []string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}
