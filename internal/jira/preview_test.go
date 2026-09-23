package jira

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPreview_RenderAndExecute_Query(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"7"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	p := Preview{
		Method: "POST",
		Path:   "/issue/ACME-123/worklog",
		Query:  url.Values{"adjustEstimate": []string{"auto"}},
		Body:   map[string]string{"timeSpent": "2h"},
	}
	preview := p.Render(srv.URL)
	if !strings.Contains(preview, "/rest/api/2/issue/ACME-123/worklog?adjustEstimate=auto") {
		t.Fatalf("preview should include encoded query, got:\n%s", preview)
	}
	if _, err := p.Execute(context.Background(), c); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotQuery != "adjustEstimate=auto" {
		t.Fatalf("expected query adjustEstimate=auto, got %q", gotQuery)
	}
}

func TestPreview_Render_Agile(t *testing.T) {
	p := Preview{
		Agile:       true,
		Method:      "POST",
		Path:        "/epic/EPIC-1/issue",
		Description: "epic → EPIC-1",
		Validation:  []ValidationRow{{Status: "✓", Message: "epic EPIC-1 (via Agile API)"}},
	}
	out := p.Render("https://jira.example.com")
	if !strings.Contains(out, "/rest/agile/1.0/epic/EPIC-1/issue") {
		t.Errorf("Render output should contain agile URL, got:\n%s", out)
	}
	if strings.Contains(out, "/rest/api/2") {
		t.Errorf("Render output must NOT contain /rest/api/2 for Agile preview, got:\n%s", out)
	}
}

func TestPreview_Execute_Agile_targetsAgileEndpoint(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	p := Preview{
		Agile:  true,
		Method: "POST",
		Path:   "/epic/EPIC-1/issue",
		Body:   map[string]any{"issues": []string{"PROJ-2"}},
	}
	if _, err := p.Execute(context.Background(), c); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotPath != "/rest/agile/1.0/epic/EPIC-1/issue" {
		t.Errorf("expected path /rest/agile/1.0/epic/EPIC-1/issue, got %s", gotPath)
	}
}

func TestPreview_Execute_NonAgile_usesAPIv2(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	p := Preview{
		Method: "DELETE",
		Path:   "/issue/comment/12345",
	}
	if _, err := p.Execute(context.Background(), c); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotPath != "/rest/api/2/issue/comment/12345" {
		t.Errorf("expected /rest/api/2/issue/comment/12345, got %s", gotPath)
	}
}

func TestPreview_Execute_Agile_200alsoCounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}")) //nolint:errcheck
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	p := Preview{
		Agile:  true,
		Method: "POST",
		Path:   "/epic/EPIC-1/issue",
		Body:   map[string]any{"issues": []string{"PROJ-2"}},
	}
	if _, err := p.Execute(context.Background(), c); err != nil {
		t.Fatalf("Execute with 200: %v", err)
	}
}
