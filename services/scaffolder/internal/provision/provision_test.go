package provision

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
)

type fakeRepos struct {
	outcome RepoOutcome
	err     error
	spec    RepoSpec
	// topics records every SetTopics call as "add|remove".
	topicCalls []string
	topicErr   error
}

func (f *fakeRepos) EnsureRepository(_ context.Context, spec RepoSpec) (RepoOutcome, error) {
	f.spec = spec
	return f.outcome, f.err
}

func (f *fakeRepos) SetTopics(_ context.Context, _, _ string, add, remove []string) error {
	f.topicCalls = append(f.topicCalls, joinTopics(add)+"|"+joinTopics(remove))
	return f.topicErr
}

func joinTopics(topics []string) string {
	out := ""
	for i, topic := range topics {
		if i > 0 {
			out += ","
		}
		out += topic
	}
	return out
}

type fakeCluster struct {
	outcome WebAppOutcome
	err     error
	spec    WebAppSpec
}

func (f *fakeCluster) Apply(_ context.Context, spec WebAppSpec) (WebAppOutcome, error) {
	f.spec = spec
	return f.outcome, f.err
}

func newService(repos RepositoryProvisioner, cluster WebAppProvisioner) *Service {
	return NewService(repos, cluster, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		DefaultOwner:     "Mampiz",
		DefaultNamespace: "idp-apps",
	})
}

func goodRequest() Request {
	return Request{
		Name:     "my-api",
		Image:    "ghcr.io/mampiz/my-api:0.1.0",
		Port:     8080,
		Replicas: 2,
	}
}

func TestScaffoldCreatesTheRepositoryThenTheCustomResource(t *testing.T) {
	repos := &fakeRepos{outcome: RepoOutcome{URL: "https://github.com/Mampiz/my-api", Created: true, ContentPushed: true}}
	cluster := &fakeCluster{outcome: WebAppOutcome{Namespace: "idp-apps", Name: "my-api", Applied: true}}

	result, err := newService(repos, cluster).Scaffold(context.Background(), goodRequest())
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	if !result.Repository.Created || !result.WebApp.Applied {
		t.Errorf("result = %+v", result)
	}
	if repos.spec.Owner != "Mampiz" {
		t.Errorf("owner = %q, want the configured default", repos.spec.Owner)
	}
	// The rendered template must actually reach the repository.
	for _, want := range []string{"main.go", "webapp.yaml", "catalog-info.yaml", ".github/workflows/ci.yml"} {
		if _, ok := repos.spec.Files[want]; !ok {
			t.Errorf("the commit is missing %s", want)
		}
	}
	if cluster.spec.RepoURL != "https://github.com/Mampiz/my-api" {
		t.Errorf("the custom resource does not record the repository: %+v", cluster.spec)
	}
}

func TestScaffoldMarksTheRepositoryAsManagedOnSuccess(t *testing.T) {
	repos := &fakeRepos{outcome: RepoOutcome{URL: "https://github.com/Mampiz/my-api"}}
	cluster := &fakeCluster{outcome: WebAppOutcome{Applied: true}}

	if _, err := newService(repos, cluster).Scaffold(context.Background(), goodRequest()); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	want := TopicManaged + "|" + TopicIncomplete
	if !slices.Contains(repos.topicCalls, want) {
		t.Errorf("topic calls = %v, want one adding %s", repos.topicCalls, TopicManaged)
	}
}

// The whole point of the failure policy: the repository survives, it is marked,
// and the caller is told which step failed.
func TestScaffoldLeavesTheRepositoryInPlaceWhenTheCustomResourceFails(t *testing.T) {
	repos := &fakeRepos{outcome: RepoOutcome{URL: "https://github.com/Mampiz/my-api", Created: true}}
	cluster := &fakeCluster{err: errors.New("admission webhook rejected the request")}

	_, err := newService(repos, cluster).Scaffold(context.Background(), goodRequest())

	var partial *PartialError
	if !errors.As(err, &partial) {
		t.Fatalf("err = %v, want a PartialError", err)
	}
	if partial.Step != "webapp" {
		t.Errorf("failed step = %q, want webapp", partial.Step)
	}
	if partial.Repository.URL == "" {
		t.Error("the partial error does not carry the repository that was created")
	}

	want := TopicIncomplete + "|" + TopicManaged
	if !slices.Contains(repos.topicCalls, want) {
		t.Errorf("topic calls = %v, want one adding %s", repos.topicCalls, TopicIncomplete)
	}
}

// Failing to mark the repository must not replace the error that actually
// matters.
func TestScaffoldKeepsTheOriginalErrorWhenMarkingFails(t *testing.T) {
	repos := &fakeRepos{
		outcome:  RepoOutcome{URL: "https://github.com/Mampiz/my-api"},
		topicErr: errors.New("topics are not available"),
	}
	cluster := &fakeCluster{err: errors.New("the real problem")}

	_, err := newService(repos, cluster).Scaffold(context.Background(), goodRequest())

	var partial *PartialError
	if !errors.As(err, &partial) {
		t.Fatalf("err = %v, want a PartialError", err)
	}
	if partial.Err.Error() != "the real problem" {
		t.Errorf("the marking failure masked the real error: %v", partial.Err)
	}
}

// Nothing is created when the request cannot produce a valid custom resource.
func TestScaffoldRejectsTheRequestBeforeCreatingAnything(t *testing.T) {
	repos := &fakeRepos{}
	cluster := &fakeCluster{}

	_, err := newService(repos, cluster).Scaffold(context.Background(), Request{
		Name:     "my-api",
		Image:    "nginx:latest",
		Port:     80,
		Replicas: 1,
	})

	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want a ValidationError", err)
	}
	if repos.spec.Name != "" {
		t.Error("a repository was created for a request that could never have worked")
	}
}

func TestScaffoldFailsWithoutTouchingTheClusterWhenTheRepositoryFails(t *testing.T) {
	repos := &fakeRepos{err: errors.New("github is down")}
	cluster := &fakeCluster{}

	if _, err := newService(repos, cluster).Scaffold(context.Background(), goodRequest()); err == nil {
		t.Fatal("expected an error")
	}
	if cluster.spec.Name != "" {
		t.Error("the custom resource was applied even though the repository failed")
	}
}
