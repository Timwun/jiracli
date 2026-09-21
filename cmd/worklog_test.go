package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cgrossde/jiracli/internal/jira"
	"github.com/cgrossde/jiracli/internal/keychain"
)

type worklogStub struct {
	issues jira.SearchResponse
	logs   map[string][]jira.Worklog
	me     jira.User
}

func (s worklogStub) Search(context.Context, string, int, int, []string) (jira.SearchResponse, error) {
	return s.issues, nil
}
func (s worklogStub) GetIssueWorklogs(_ context.Context, key string) ([]jira.Worklog, error) {
	return s.logs[key], nil
}
func (s worklogStub) SearchUsers(context.Context, string, int, bool) ([]jira.UserRef, error) {
	return nil, nil
}
func (s worklogStub) Myself(context.Context) (jira.User, error) { return s.me, nil }

func TestParseTimeExpressionRollingWeeks(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	got, dateOnly, err := parseTimeExpression("4w", now)
	if err != nil || dateOnly || !got.Equal(now.Add(-28*24*time.Hour)) {
		t.Fatalf("got %v dateOnly=%v err=%v", got, dateOnly, err)
	}
	until, dateOnly, err := parseTimeExpression("2026-09-21", now)
	if err != nil || !dateOnly || !until.Equal(time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("date parse = %v dateOnly=%v err=%v", until, dateOnly, err)
	}
}

func TestParseSinceUsesCallerSpecificError(t *testing.T) {
	_, err := parseSince("bad")
	if err == nil || !strings.Contains(err.Error(), "invalid --since value") || strings.Contains(err.Error(), "worklog") {
		t.Fatalf("parseSince error = %v", err)
	}
}

func TestWorklogLatestSupportsScopeFlags(t *testing.T) {
	cmd := newWorklogLatestCmd()
	for _, name := range []string{"issue", "project", "all-issues"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("worklog latest missing --%s", name)
		}
	}
	show := NewShowWorklogsCmd()
	for _, name := range []string{"issue", "project", "all-issues"} {
		if show.Flags().Lookup(name) != nil {
			t.Errorf("show worklogs unexpectedly exposes --%s", name)
		}
	}
}

func worklogTestStub() worklogStub {
	issue := jira.IssueRaw{Key: "ACME-1"}
	issue.Fields.Summary = "Example issue"
	issue.Fields.Project.Key = "ACME"
	return worklogStub{
		issues: jira.SearchResponse{Total: 1, Issues: []jira.IssueRaw{issue}},
		logs: map[string][]jira.Worklog{"ACME-1": {
			{ID: "2", IssueID: "1", Author: jira.WorklogUser{Name: "alice", DisplayName: "Alice"}, Started: "2026-09-20T09:00:00.000+0000", Created: "2026-09-20T10:00:00.000+0000", Updated: "2026-09-20T11:00:00.000+0000", TimeSpent: "1h", TimeSpentSeconds: 3600},
			{ID: "1", IssueID: "1", Author: jira.WorklogUser{Name: "bob", DisplayName: "Bob"}, Started: "2026-09-21T09:00:00.000+0000", Created: "2026-09-21T08:00:00.000+0000", Updated: "2026-09-21T09:30:00.000+0000", TimeSpentSeconds: 0},
		}},
		me: jira.User{Name: "alice", DisplayName: "Alice"},
	}
}

func TestWorklogShowWithoutAuthorKeepsAllAuthors(t *testing.T) {
	stub := worklogTestStub()
	flags := worklogFlags{Issue: "ACME-1", DateField: "started", Sort: "asc", Limit: 50, Page: 1, JSON: true}
	out, err := runWorklogWithClient(context.Background(), stub, keychain.Entry{}, flags, "show", time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, `"issueKey":"ACME-1"`) != 2 || !strings.Contains(out, `"name":"bob"`) {
		t.Fatalf("unexpected show output: %s", out)
	}
}

func TestWorklogLatestUsesCreatedByDefault(t *testing.T) {
	stub := worklogTestStub()
	stub.logs["ACME-1"] = []jira.Worklog{
		{ID: "2", IssueID: "1", Author: jira.WorklogUser{Name: "alice"}, Started: "2026-09-20T09:00:00.000+0000", Created: "2026-09-21T08:00:00.000+0000", Updated: "2026-09-21T09:00:00.000+0000", TimeSpentSeconds: 3600},
		{ID: "1", IssueID: "1", Author: jira.WorklogUser{Name: "alice"}, Started: "2026-09-20T10:00:00.000+0000", Created: "2026-09-21T10:00:00.000+0000", Updated: "2026-09-21T11:00:00.000+0000", TimeSpentSeconds: 60},
	}
	flags := worklogFlags{Author: "me", DateField: "created", JSON: true}
	out, err := runWorklogWithClient(context.Background(), stub, keychain.Entry{User: "alice"}, flags, "latest", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var got worklogRecord
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "1" {
		t.Fatalf("latest id = %s, want created-newest id 1", got.ID)
	}
}

func TestWorklogSummaryJSON(t *testing.T) {
	stub := worklogTestStub()
	flags := worklogFlags{Author: "me", DateField: "started", GroupBy: "issue", JSON: true}
	out, err := runWorklogWithClient(context.Background(), stub, keychain.Entry{User: "alice", DisplayName: "Alice Example"}, flags, "summary", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var got worklogSummaryRecord
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Author != "alice" || got.AuthorDisplayName != "Alice Example" || got.TimeSpentSeconds != 3600 || got.TimeSpent != "1h" {
		t.Fatalf("summary identity/totals = %+v", got)
	}
	if got.Groups == nil || len(*got.Groups) != 1 || (*got.Groups)[0].Group != "ACME-1" || (*got.Groups)[0].EntryCount != 1 {
		t.Fatalf("summary groups = %+v", got.Groups)
	}

	stub.logs["ACME-1"] = []jira.Worklog{{ID: "3", Author: jira.WorklogUser{Name: "alice"}, Started: "2026-09-21T09:00:00.000+0000", Created: "2026-09-21T09:00:00.000+0000", Updated: "2026-09-21T09:00:00.000+0000"}}

	emptyStub := worklogStub{issues: jira.SearchResponse{}, logs: map[string][]jira.Worklog{}}
	flags.GroupBy = "issue"
	out, err = runWorklogWithClient(context.Background(), emptyStub, keychain.Entry{User: "alice"}, flags, "summary", time.Now())
	if err != nil || !strings.Contains(out, `"groups":[]`) {
		t.Fatalf("empty grouped summary = %s err=%v", out, err)
	}

	flags.GroupBy = ""
	out, err = runWorklogWithClient(context.Background(), stub, keychain.Entry{User: "alice"}, flags, "summary", time.Now())
	if err != nil || !strings.Contains(out, `"timeSpent":"0h"`) {
		t.Fatalf("zero summary = %s err=%v", out, err)
	}
}

func TestWorklogSummaryPlainIssueTable(t *testing.T) {
	issueA := jira.IssueRaw{Key: "ACME-123"}
	issueA.Fields.Summary = "First"
	issueA.Fields.Project.Key = "ACME"
	issueB := jira.IssueRaw{Key: "ACME-124"}
	issueB.Fields.Summary = "Second"
	issueB.Fields.Project.Key = "ACME"
	makeLog := func(id string, seconds int64) jira.Worklog {
		return jira.Worklog{
			ID: id, Author: jira.WorklogUser{Name: "achen", DisplayName: "Alex Chen"},
			Started: "2026-09-20T09:00:00.000+0000", Created: "2026-09-20T10:00:00.000+0000", Updated: "2026-09-20T10:00:00.000+0000",
			TimeSpentSeconds: seconds,
		}
	}
	stub := worklogStub{
		issues: jira.SearchResponse{Total: 2, Issues: []jira.IssueRaw{issueA, issueB}},
		logs: map[string][]jira.Worklog{
			"ACME-123": {makeLog("1", 10*60*60), makeLog("2", 10*60*60), makeLog("3", 10*60*60), makeLog("4", 10*60*60)},
			"ACME-124": {makeLog("5", 40*60*60), makeLog("6", 40*60*60)},
		},
	}
	flags := worklogFlags{Author: "me", DateField: "started", Since: "4w", GroupBy: "issue"}
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	out, err := runWorklogWithClient(context.Background(), stub, keychain.Entry{User: "achen", DisplayName: "Alex Chen"}, flags, "summary", now)
	if err != nil {
		t.Fatal(err)
	}
	want := "Worklog summary for Alex Chen\n" +
		"Period:   2026-08-24 → 2026-09-21 (rolling 28 days)\n" +
		"Based on: work date\n\n" +
		"Issue       Entries  Time\n" +
		"ACME-123          4    1w\n" +
		"ACME-124          2    2w\n" +
		strings.Repeat("─", 25) + "\n" +
		"Total             6    3w\n"
	if out != want {
		t.Fatalf("summary output:\n%s\nwant:\n%s", out, want)
	}
}

func TestWorklogCandidateCap(t *testing.T) {
	stub := worklogTestStub()
	stub.issues.Total = 501
	_, err := runWorklogWithClient(context.Background(), stub, keychain.Entry{User: "alice"}, worklogFlags{Author: "me", DateField: "started"}, "summary", time.Now())
	if err == nil || !strings.Contains(err.Error(), "--all-issues") {
		t.Fatalf("expected cap error, got %v", err)
	}
}

func TestWorklogListPaginationTrailerAndBounds(t *testing.T) {
	stub := worklogTestStub()
	stub.logs["ACME-1"] = []jira.Worklog{
		{ID: "2", IssueID: "1", Author: jira.WorklogUser{Name: "alice"}, Started: "2026-09-21T00:00:00.000+0000", Created: "2026-09-21T00:00:00.000+0000", Updated: "2026-09-21T00:00:00.000+0000", TimeSpentSeconds: 60},
		{ID: "1", IssueID: "1", Author: jira.WorklogUser{Name: "alice"}, Started: "2026-09-21T00:00:00.000+0000", Created: "2026-09-21T00:00:00.000+0000", Updated: "2026-09-21T00:00:00.000+0000", TimeSpentSeconds: 120},
	}
	flags := worklogFlags{Author: "me", DateField: "started", Sort: "asc", Limit: 1, Page: 1, Since: "2026-09-21", Until: "2026-09-21", JSON: true}
	out, err := runWorklogWithClient(context.Background(), stub, keychain.Entry{User: "alice"}, flags, "list", time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC))
	if err != nil || !strings.Contains(out, `"_pagination"`) || !strings.Contains(out, `"next_page":2`) {
		t.Fatalf("pagination output = %s err=%v", out, err)
	}
}
