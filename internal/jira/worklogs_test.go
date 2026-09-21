package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestGetIssueWorklogsAllAtOnce(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/2/issue/CAR-1/worklog" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("startAt") != "0" || r.URL.Query().Get("maxResults") != "100" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		json.NewEncoder(w).Encode(WorklogsResponse{StartAt: 0, MaxResults: 100, Total: 2, Worklogs: []Worklog{
			{ID: "1", IssueID: "10", Author: WorklogUser{Name: "alice"}, UpdateAuthor: WorklogUser{Name: "bob"}, Comment: "c", Started: "2026-06-24T08:00:00.000+0000", Created: "2026-06-24T09:00:00.000+0000", Updated: "2026-06-24T10:00:00.000+0000", TimeSpent: "1h", TimeSpentSeconds: 3600},
			{ID: "2", IssueID: "10", TimeSpentSeconds: 60},
		}})
	}))
	defer server.Close()

	logs, err := newTestClient(server.URL).GetIssueWorklogs(context.Background(), "CAR-1")
	if err != nil || len(logs) != 2 {
		t.Fatalf("logs = %#v, err = %v", logs, err)
	}
	if logs[0].Created == logs[0].Started || logs[0].Updated == logs[0].Started || logs[0].Author.Name != "alice" || logs[0].UpdateAuthor.Name != "bob" {
		t.Fatalf("worklog fields were not preserved: %#v", logs[0])
	}
}

func TestGetIssueWorklogsPaginatedAndDuplicateGuard(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		start, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		logs := []Worklog{{ID: "1"}, {ID: "2"}}
		if start > 0 {
			logs = []Worklog{{ID: "2"}, {ID: "3"}}
		}
		json.NewEncoder(w).Encode(WorklogsResponse{StartAt: start, MaxResults: 2, Total: 3, Worklogs: logs})
	}))
	defer server.Close()
	logs, err := newTestClient(server.URL).GetIssueWorklogs(context.Background(), "CAR-1")
	if err != nil || len(logs) != 3 || calls != 2 {
		t.Fatalf("logs = %#v, calls = %d, err = %v", logs, calls, err)
	}

	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(WorklogsResponse{Total: 2, Worklogs: []Worklog{{ID: "1"}}})
	}))
	defer stalled.Close()
	_, err = newTestClient(stalled.URL).GetIssueWorklogs(context.Background(), "CAR-1")
	if err == nil || !strings.Contains(err.Error(), "incomplete worklog response") {
		t.Fatalf("expected incomplete response error, got %v", err)
	}
}

func TestGetIssueWorklogsEmptyAndStatus(t *testing.T) {
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"total":0,"worklogs":[]}`)
	}))
	defer empty.Close()
	logs, err := newTestClient(empty.URL).GetIssueWorklogs(context.Background(), "CAR-1")
	if err != nil || logs == nil || len(logs) != 0 {
		t.Fatalf("empty logs = %#v, err = %v", logs, err)
	}

	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"errorMessages":["no"]}`)
	}))
	defer forbidden.Close()
	_, err = newTestClient(forbidden.URL).GetIssueWorklogs(context.Background(), "CAR-1")
	if err == nil || !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected forbidden, got %v", err)
	}
}
