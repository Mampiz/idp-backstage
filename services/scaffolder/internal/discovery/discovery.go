// Package discovery finds the repositories of a GitHub account that carry a
// Backstage catalog file, and renders them as a Backstage Location entity.
//
// Why this exists: Backstage's own GithubEntityProvider can only discover
// repositories inside an *organization* -- every GraphQL query it issues is
// organization(login: $org). Against a personal account it fails with
// NOT_FOUND. This package does the same job for user accounts (and for
// organizations) against the REST API, and hands Backstage a plain Location
// entity, which the catalog already knows how to process recursively.
package discovery

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v77/github"
)

// Result is the outcome of one discovery pass.
type Result struct {
	// Targets are URLs of catalog files, ready to be used as Location targets.
	Targets []string
	// Scanned is how many repositories were looked at.
	Scanned int
	// FetchedAt is when GitHub was actually queried.
	FetchedAt time.Time
}

// Discoverer lists the catalog files of a GitHub account, caching the result.
type Discoverer struct {
	client      *github.Client
	owner       string
	catalogPath string
	ttl         time.Duration
	now         func() time.Time

	mu     sync.Mutex
	cached *Result
}

// Option customises a Discoverer.
type Option func(*Discoverer)

// WithClock replaces the clock, for tests.
func WithClock(now func() time.Time) Option {
	return func(d *Discoverer) { d.now = now }
}

// New builds a Discoverer for the given owner.
func New(client *github.Client, owner, catalogPath string, ttl time.Duration, opts ...Option) *Discoverer {
	d := &Discoverer{
		client:      client,
		owner:       owner,
		catalogPath: strings.TrimPrefix(catalogPath, "/"),
		ttl:         ttl,
		now:         time.Now,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Discover returns the catalog targets of the owner. Results are cached for the
// configured TTL: Backstage refreshes locations on its own schedule and there is
// no reason to spend GitHub rate limit on every one of those refreshes.
func (d *Discoverer) Discover(ctx context.Context) (Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.cached != nil && d.now().Sub(d.cached.FetchedAt) < d.ttl {
		return *d.cached, nil
	}

	repos, err := d.listRepositories(ctx)
	if err != nil {
		// Serve stale data rather than dropping the whole catalog because
		// GitHub had a bad minute. A stale target list is far less harmful
		// than an empty one, which would make Backstage orphan every entity.
		if d.cached != nil {
			return *d.cached, nil
		}
		return Result{}, err
	}

	var targets []string
	for _, repo := range repos {
		if repo.GetArchived() || repo.GetDisabled() {
			continue
		}
		ref := repo.GetDefaultBranch()
		if ref == "" {
			continue
		}
		ok, err := d.hasCatalogFile(ctx, repo.GetName(), ref)
		if err != nil {
			return Result{}, fmt.Errorf("checking %s/%s: %w", d.owner, repo.GetName(), err)
		}
		if ok {
			targets = append(targets, fmt.Sprintf("https://github.com/%s/%s/blob/%s/%s",
				d.owner, repo.GetName(), ref, d.catalogPath))
		}
	}
	sort.Strings(targets)

	result := Result{Targets: targets, Scanned: len(repos), FetchedAt: d.now()}
	d.cached = &result
	return result, nil
}

// listRepositories works for both a user account and an organization, so the
// service keeps working if the repositories ever move into an org.
func (d *Discoverer) listRepositories(ctx context.Context) ([]*github.Repository, error) {
	owner, _, err := d.client.Users.Get(ctx, d.owner)
	if err != nil {
		return nil, fmt.Errorf("resolving owner %q: %w", d.owner, err)
	}

	var all []*github.Repository
	page := 1
	for {
		var (
			repos []*github.Repository
			resp  *github.Response
		)
		if owner.GetType() == "Organization" {
			repos, resp, err = d.client.Repositories.ListByOrg(ctx, d.owner,
				&github.RepositoryListByOrgOptions{ListOptions: github.ListOptions{Page: page, PerPage: 100}})
		} else {
			repos, resp, err = d.client.Repositories.ListByUser(ctx, d.owner,
				&github.RepositoryListByUserOptions{ListOptions: github.ListOptions{Page: page, PerPage: 100}})
		}
		if err != nil {
			return nil, fmt.Errorf("listing repositories of %q: %w", d.owner, err)
		}
		all = append(all, repos...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	return all, nil
}

func (d *Discoverer) hasCatalogFile(ctx context.Context, repo, ref string) (bool, error) {
	_, _, resp, err := d.client.Repositories.GetContents(ctx, d.owner, repo, d.catalogPath,
		&github.RepositoryContentGetOptions{Ref: ref})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// LocationEntity renders the result as a Backstage Location entity. Backstage
// reads this from a url location and then processes each target in turn.
func (r Result) LocationEntity(owner string) string {
	var b strings.Builder
	b.WriteString("# Generated by the scaffolder service. Do not edit by hand.\n")
	fmt.Fprintf(&b, "# %d repositories of %q scanned at %s.\n",
		r.Scanned, owner, r.FetchedAt.UTC().Format(time.RFC3339))
	b.WriteString("apiVersion: backstage.io/v1alpha1\n")
	b.WriteString("kind: Location\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: github-discovery\n")
	fmt.Fprintf(&b, "  description: Repositories of %s that contain a catalog file.\n", owner)
	b.WriteString("spec:\n")
	b.WriteString("  type: url\n")
	if len(r.Targets) == 0 {
		b.WriteString("  targets: []\n")
		return b.String()
	}
	b.WriteString("  targets:\n")
	for _, t := range r.Targets {
		fmt.Fprintf(&b, "    - %s\n", t)
	}
	return b.String()
}
