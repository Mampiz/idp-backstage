package provision

import (
	"errors"
	"net/http"
	"testing"

	"github.com/google/go-github/v77/github"
)

func notFound() error {
	return &github.ErrorResponse{
		Response: &http.Response{StatusCode: http.StatusNotFound},
		Message:  "",
	}
}

// GitHub answers a bare 404 when a token without the "workflow" scope tries to
// write anything under .github/workflows/, which reads like a missing
// repository. That has to be translated or it is undiagnosable.
func TestExplainWorkflowScopeTranslatesTheOpaque404(t *testing.T) {
	entries := []*github.TreeEntry{
		{Path: github.Ptr("main.go")},
		{Path: github.Ptr(".github/workflows/ci.yml")},
	}

	err := explainWorkflowScope(notFound(), entries)

	if !errors.Is(err, ErrWorkflowScope) {
		t.Fatalf("err = %v, want it to wrap ErrWorkflowScope", err)
	}
}

func TestExplainWorkflowScopeLeavesOtherFailuresAlone(t *testing.T) {
	plain := []*github.TreeEntry{{Path: github.Ptr("main.go")}}
	if err := explainWorkflowScope(notFound(), plain); errors.Is(err, ErrWorkflowScope) {
		t.Error("a 404 without workflow files was blamed on the scope")
	}

	withWorkflow := []*github.TreeEntry{
		{Path: github.Ptr("main.go")},
		{Path: github.Ptr(".github/workflows/ci.yml")},
	}
	other := &github.ErrorResponse{Response: &http.Response{StatusCode: http.StatusInternalServerError}}
	if err := explainWorkflowScope(other, withWorkflow); errors.Is(err, ErrWorkflowScope) {
		t.Error("a 500 was blamed on the scope")
	}
}

func TestIsSettlingOnlyMatchesTransientStatuses(t *testing.T) {
	for status, want := range map[int]bool{
		http.StatusNotFound:            true,
		http.StatusConflict:            true,
		http.StatusUnauthorized:        false,
		http.StatusInternalServerError: false,
	} {
		err := &github.ErrorResponse{Response: &http.Response{StatusCode: status}}
		if got := isSettling(err); got != want {
			t.Errorf("isSettling(%d) = %v, want %v", status, got, want)
		}
	}
	if isSettling(errors.New("not an api error")) {
		t.Error("a plain error was treated as transient")
	}
}
