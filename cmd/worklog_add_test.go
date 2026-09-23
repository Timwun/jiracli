package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cgrossde/jiracli/internal/jira"
)

func newWorklogTestClient(srv *httptest.Server) *jira.Client {
	return &jira.Client{BaseURL: srv.URL, HTTP: srv.Client()}
}

func TestWorklogAdd_HTTPRequestAndSuccess(t *testing.T) {
	var methods []string
	var gotBody map[string]any
	var gotAdjustEstimate string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path != "/rest/api/2/issue/ACME-123" || r.URL.Query().Get("fields") != "key" {
				t.Errorf("unexpected issue validation request: %s?%s", r.URL.Path, r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"key":"ACME-123"}`))
		case http.MethodPost:
			if r.URL.Path != "/rest/api/2/issue/ACME-123/worklog" {
				t.Errorf("unexpected worklog path: %s", r.URL.Path)
			}
			gotAdjustEstimate = r.URL.Query().Get("adjustEstimate")
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Errorf("decode worklog request: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"10001","timeSpent":"2h"}`))
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer srv.Close()

	out, err := worklogAddWithClient(context.Background(), newWorklogTestClient(srv), srv.URL, WorklogAddFlags{
		Comment:        "Implementation",
		Started:        "2026-09-23T09:00:00Z",
		AdjustEstimate: "auto",
		Yes:            true,
	}, "ACME-123", "2h")
	if err != nil {
		t.Fatalf("worklogAddWithClient: %v", err)
	}
	if strings.Join(methods, ",") != "GET,POST" {
		t.Fatalf("expected validation then mutation, got %v", methods)
	}
	if gotAdjustEstimate != "auto" {
		t.Fatalf("expected adjustEstimate=auto, got %q", gotAdjustEstimate)
	}
	if gotBody["timeSpent"] != "2h" || gotBody["comment"] != "Implementation" || gotBody["started"] != "2026-09-23T09:00:00.000+0000" {
		t.Fatalf("unexpected worklog body: %#v", gotBody)
	}
	if !strings.Contains(out, "✓ logged 2h on ACME-123 (worklog id 10001)") || !strings.Contains(out, "→ jiracli show worklogs ACME-123") {
		t.Fatalf("unexpected success output:\n%s", out)
	}
}

func TestWorklogAdd_LeaveOmitsOptionalFields(t *testing.T) {
	var gotBody map[string]any
	var gotAdjustEstimate string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"key":"ACME-123"}`))
			return
		}
		gotAdjustEstimate = r.URL.Query().Get("adjustEstimate")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode worklog request: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"timeSpent":"30m"}`))
	}))
	defer srv.Close()

	out, err := worklogAddWithClient(context.Background(), newWorklogTestClient(srv), srv.URL, WorklogAddFlags{
		AdjustEstimate: "leave",
		Yes:            true,
	}, "ACME-123", "30m")
	if err != nil {
		t.Fatalf("worklogAddWithClient: %v", err)
	}
	if gotAdjustEstimate != "leave" {
		t.Fatalf("expected adjustEstimate=leave, got %q", gotAdjustEstimate)
	}
	if len(gotBody) != 1 || gotBody["timeSpent"] != "30m" {
		t.Fatalf("optional fields should be omitted: %#v", gotBody)
	}
	if !strings.Contains(out, "✓ logged 30m on ACME-123") || strings.Contains(out, "worklog id") {
		t.Fatalf("unexpected ID fallback output:\n%s", out)
	}
}

func TestWorklogAdd_RejectsInvalidInputsBeforeHTTP(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	client := newWorklogTestClient(srv)

	tests := []struct {
		name  string
		flags WorklogAddFlags
		time  string
		want  string
	}{
		{name: "adjustment", flags: WorklogAddFlags{AdjustEstimate: "manual"}, time: "2h", want: "invalid --adjust-estimate manual"},
		{name: "started", flags: WorklogAddFlags{AdjustEstimate: "auto", Started: "yesterday"}, time: "2h", want: "invalid --started"},
		{name: "duration", flags: WorklogAddFlags{AdjustEstimate: "auto"}, time: "   ", want: "time-spent is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := worklogAddWithClient(context.Background(), client, srv.URL, tt.flags, "ACME-123", tt.time)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("invalid inputs must not make HTTP requests, got %d", calls)
	}
}

func TestWorklogAdd_PostError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"key":"ACME-123"}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errors":{"timeSpent":"invalid duration"}}`))
	}))
	defer srv.Close()

	_, err := worklogAddWithClient(context.Background(), newWorklogTestClient(srv), srv.URL, WorklogAddFlags{
		AdjustEstimate: "auto",
		Yes:            true,
	}, "ACME-123", "2h")
	if err == nil || !strings.Contains(err.Error(), "apply failed: bad request: timeSpent: invalid duration") {
		t.Fatalf("expected corrective Jira POST error, got %v", err)
	}
}
