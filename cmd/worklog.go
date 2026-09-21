package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/cgrossde/jiracli/internal/jira"
	"github.com/cgrossde/jiracli/internal/keychain"
)

type worklogClient interface {
	Search(context.Context, string, int, int, []string) (jira.SearchResponse, error)
	GetIssueWorklogs(context.Context, string) ([]jira.Worklog, error)
	SearchUsers(context.Context, string, int, bool) ([]jira.UserRef, error)
	Myself(context.Context) (jira.User, error)
}

type worklogFilter struct {
	Author    string
	Issue     string
	Project   string
	Since     time.Time
	Until     time.Time
	DateField string
	AllIssues bool
}

type worklogFlags struct {
	Profile   string
	Author    string
	Issue     string
	Project   string
	Since     string
	Until     string
	DateField string
	Sort      string
	Limit     int
	Page      int
	AllIssues bool
	GroupBy   string
	JSON      bool
}

type worklogRecord struct {
	ID               string           `json:"id"`
	IssueID          string           `json:"issueId"`
	IssueKey         string           `json:"issueKey"`
	IssueSummary     string           `json:"issueSummary,omitempty"`
	Author           jira.WorklogUser `json:"author"`
	UpdateAuthor     jira.WorklogUser `json:"updateAuthor"`
	Comment          string           `json:"comment"`
	Started          string           `json:"started"`
	Created          string           `json:"created"`
	Updated          string           `json:"updated"`
	TimeSpent        string           `json:"timeSpent"`
	TimeSpentSeconds int64            `json:"timeSpentSeconds"`
}

type worklogPagination struct {
	Pagination struct {
		Page     int  `json:"page"`
		Pages    int  `json:"pages"`
		Total    int  `json:"total"`
		NextPage int  `json:"next_page"`
		HasMore  bool `json:"has_more"`
	} `json:"_pagination"`
}

type worklogSummaryGroup struct {
	Group            string `json:"group"`
	EntryCount       int    `json:"entryCount"`
	TimeSpent        string `json:"timeSpent"`
	TimeSpentSeconds int64  `json:"timeSpentSeconds"`
}

type worklogSummaryRecord struct {
	Author            string                 `json:"author"`
	AuthorDisplayName string                 `json:"authorDisplayName,omitempty"`
	DateField         string                 `json:"dateField"`
	Since             string                 `json:"since,omitempty"`
	Until             string                 `json:"until,omitempty"`
	EntryCount        int                    `json:"entryCount"`
	TimeSpent         string                 `json:"timeSpent"`
	TimeSpentSeconds  int64                  `json:"timeSpentSeconds"`
	GroupBy           string                 `json:"groupBy,omitempty"`
	Groups            *[]worklogSummaryGroup `json:"groups,omitempty"`
}

type worklogIssue struct {
	Key     string
	Summary string
	Project string
}

func NewWorklogCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "worklog",
		Short: "Read Jira worklogs",
		Long:  "Find, list, and summarize Jira worklog entries without changing Jira.",
	}
	root.AddCommand(newWorklogLatestCmd(), newWorklogListCmd(), newWorklogSummaryCmd())
	return root
}

func newWorklogLatestCmd() *cobra.Command {
	flags := worklogFlags{Author: "me", DateField: "created"}
	c := &cobra.Command{
		Use:     "latest",
		Short:   "Show the most recently entered worklog",
		Long:    "Return the visible worklog with the greatest selected timestamp. The default date field is created, which answers when time was entered; use --date-field started for work date.",
		Example: "  jiracli worklog latest\n  jiracli worklog latest --date-field started --since 4w",
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := runWorklog(ctxFor(cmd), flags, "latest")
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), out)
			return err
		},
	}
	addWorklogFlags(c, &flags, false, true)
	return c
}

func newWorklogListCmd() *cobra.Command {
	flags := worklogFlags{Author: "me", DateField: "started", Sort: "desc", Limit: 50, Page: 1}
	c := &cobra.Command{
		Use:     "list",
		Short:   "List worklog entries",
		Long:    "List worklog entries after collecting and filtering complete issue worklogs. The default date field is started, which answers when work was performed; created and updated are also available.",
		Example: "  jiracli worklog list --since 4w\n  jiracli worklog list --project ACME --sort asc --limit 100",
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := runWorklog(ctxFor(cmd), flags, "list")
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), out)
			return err
		},
	}
	addWorklogFlags(c, &flags, true, true)
	return c
}

func newWorklogSummaryCmd() *cobra.Command {
	flags := worklogFlags{Author: "me", DateField: "started"}
	c := &cobra.Command{
		Use:     "summary",
		Short:   "Summarize worklog time",
		Long:    "Aggregate complete worklog entries. The default date field is started; grouping by day or week uses UTC selected timestamps, while issue and project grouping uses issue metadata.",
		Example: "  jiracli worklog summary --since 4w\n  jiracli worklog summary --since 4w --group-by week",
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := runWorklog(ctxFor(cmd), flags, "summary")
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), out)
			return err
		},
	}
	addWorklogFlags(c, &flags, false, true)
	c.Flags().StringVar(&flags.GroupBy, "group-by", "", "Group totals by day, week, issue, or project")
	return c
}

func NewShowWorklogsCmd() *cobra.Command {
	flags := worklogFlags{DateField: "started", Sort: "desc", Limit: 50, Page: 1}
	c := &cobra.Command{
		Use:     "worklogs <KEY>",
		Short:   "List worklogs on an issue",
		Long:    "List every visible author's worklogs on one issue unless --author is supplied. The default date field is started; use created to inspect entry time or updated to inspect edits.",
		Example: "  jiracli show worklogs ACME-123\n  jiracli show worklogs ACME-123 --author me --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.Issue = args[0]
			out, err := runWorklog(ctxFor(cmd), flags, "show")
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), out)
			return err
		},
	}
	addWorklogFlags(c, &flags, true, false)
	return c
}

func ctxFor(c *cobra.Command) context.Context { return c.Context() }

func addWorklogFlags(c *cobra.Command, flags *worklogFlags, paged, scope bool) {
	c.Flags().StringVar(&flags.Profile, "profile", "", "Profile name")
	c.Flags().BoolVar(&flags.JSON, "json", false, "Output NDJSON")
	c.Flags().StringVar(&flags.Author, "author", flags.Author, "Author username, display name, or me")
	c.Flags().StringVar(&flags.Since, "since", "", "Inclusive lower timestamp bound (RFC3339, YYYY-MM-DD, or positive Nw/Nd/Nh)")
	c.Flags().StringVar(&flags.Until, "until", "", "Inclusive upper timestamp bound (RFC3339, YYYY-MM-DD, or positive Nw/Nd/Nh)")
	c.Flags().StringVar(&flags.DateField, "date-field", flags.DateField, "Timestamp to filter and order: started, created, or updated")
	if scope {
		c.Flags().StringVar(&flags.Issue, "issue", "", "Restrict to one issue key")
		c.Flags().StringVar(&flags.Project, "project", "", "Restrict to one project key")
		c.Flags().BoolVar(&flags.AllIssues, "all-issues", false, "Bypass the 500-candidate issue safety cap")
	}
	if paged {
		c.Flags().StringVar(&flags.Sort, "sort", flags.Sort, "Sort by selected timestamp: asc or desc")
		c.Flags().IntVar(&flags.Limit, "limit", flags.Limit, "Entries per page (1-200)")
		c.Flags().IntVar(&flags.Page, "page", flags.Page, "Page number (1-indexed)")
	}
}

func parseTimeExpression(value string, now time.Time) (time.Time, bool, error) {
	if value == "" {
		return time.Time{}, false, nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, false, nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t.UTC(), true, nil
	}
	if len(value) < 2 {
		return time.Time{}, false, invalidTimeExpression(value)
	}
	n, err := strconv.Atoi(value[:len(value)-1])
	if err != nil || n <= 0 {
		return time.Time{}, false, invalidTimeExpression(value)
	}
	var d time.Duration
	switch value[len(value)-1] {
	case 'w':
		d = time.Duration(n) * 7 * 24 * time.Hour
	case 'd':
		d = time.Duration(n) * 24 * time.Hour
	case 'h':
		d = time.Duration(n) * time.Hour
	default:
		return time.Time{}, false, invalidTimeExpression(value)
	}
	return now.Add(-d), false, nil
}

func invalidTimeExpression(value string) error {
	return fmt.Errorf("invalid worklog time %q — use RFC3339, YYYY-MM-DD, or a positive relative value such as 4w, 7d, or 24h", value)
}

func parseWorklogFilter(flags worklogFlags, now time.Time, author string) (worklogFilter, error) {
	if flags.DateField != "started" && flags.DateField != "created" && flags.DateField != "updated" {
		return worklogFilter{}, fmt.Errorf("invalid --date-field %q — choose started, created, or updated", flags.DateField)
	}
	if flags.Issue != "" && flags.Project != "" {
		return worklogFilter{}, fmt.Errorf("--issue and --project are mutually exclusive — choose one")
	}
	since, _, err := parseTimeExpression(flags.Since, now)
	if err != nil {
		return worklogFilter{}, fmt.Errorf("--since: %w", err)
	}
	until, untilDate, err := parseTimeExpression(flags.Until, now)
	if err != nil {
		return worklogFilter{}, fmt.Errorf("--until: %w", err)
	}
	if untilDate {
		until = until.Add(24*time.Hour - time.Nanosecond)
	}
	if !since.IsZero() && !until.IsZero() && until.Before(since) {
		return worklogFilter{}, fmt.Errorf("--until must not be earlier than --since")
	}
	return worklogFilter{Author: author, Issue: flags.Issue, Project: flags.Project, Since: since, Until: until, DateField: flags.DateField, AllIssues: flags.AllIssues}, nil
}

func resolveWorklogAuthor(ctx context.Context, client worklogClient, entry keychain.Entry, requested string, required bool) (string, string, bool, error) {
	if requested == "" && !required {
		return "", "", false, nil
	}
	if requested == "" {
		requested = "me"
	}
	if strings.EqualFold(requested, "me") {
		if entry.User != "" {
			displayName := entry.DisplayName
			if displayName == "" {
				displayName = entry.User
			}
			return entry.User, displayName, true, nil
		}
		if entry.DisplayName != "" {
			return entry.DisplayName, entry.DisplayName, true, nil
		}
		u, err := client.Myself(ctx)
		if err != nil {
			return "", "", false, fmt.Errorf("resolve authenticated worklog author: %w", err)
		}
		name := u.Name
		if name == "" {
			name = u.DisplayName
		}
		displayName := u.DisplayName
		if displayName == "" {
			displayName = name
		}
		return name, displayName, true, nil
	}
	users, err := client.SearchUsers(ctx, requested, 50, true)
	if err != nil {
		return "", "", false, fmt.Errorf("lookup worklog author %q: %w", requested, err)
	}
	users = jira.FilterUsersByQuery(users, requested)
	if len(users) != 1 {
		return "", "", false, fmt.Errorf("worklog author %q is not unique — run: jiracli lookup users %s", requested, strconv.Quote(requested))
	}
	displayName := users[0].DisplayName
	if displayName == "" {
		displayName = users[0].Name
	}
	return users[0].Name, displayName, false, nil
}

func worklogAuthorClause(author string, current bool) string {
	if current {
		return "worklogAuthor = currentUser()"
	}
	return `worklogAuthor = "` + strings.ReplaceAll(author, `"`, `\"`) + `"`
}

func collectWorklogs(ctx context.Context, client worklogClient, filter worklogFilter, authorCurrent bool) ([]worklogRecord, error) {
	jql := ""
	fields := []string{"summary", "project"}
	if filter.Issue != "" {
		jql = `key = "` + strings.ReplaceAll(filter.Issue, `"`, `\"`) + `"`
	} else {
		jql = worklogAuthorClause(filter.Author, authorCurrent)
		if filter.Project != "" {
			jql += ` AND project = "` + strings.ReplaceAll(filter.Project, `"`, `\"`) + `"`
		}
		if filter.DateField == "started" && (!filter.Since.IsZero() || !filter.Until.IsZero()) {
			if !filter.Since.IsZero() {
				jql += ` AND worklogDate >= "` + filter.Since.Add(-24*time.Hour).UTC().Format("2006-01-02") + `"`
			}
			if !filter.Until.IsZero() {
				jql += ` AND worklogDate <= "` + filter.Until.Add(24*time.Hour).UTC().Format("2006-01-02") + `"`
			}
		}
	}
	issues, err := collectWorklogIssues(ctx, client, jql, fields, filter.AllIssues)
	if err != nil {
		return nil, err
	}
	return fetchWorklogRecords(ctx, client, issues, filter)
}

func collectWorklogIssues(ctx context.Context, client worklogClient, jql string, fields []string, all bool) ([]worklogIssue, error) {
	const capIssues = 500
	first, err := client.Search(ctx, jql, 1, 100, fields)
	if err != nil {
		return nil, fmt.Errorf("search worklog issues: %w", err)
	}
	if first.Total > capIssues && !all {
		return nil, fmt.Errorf("worklog search matched %d candidate issues, exceeding the 500-issue safety cap — narrow with --since, --project, or --issue, or pass --all-issues", first.Total)
	}
	issues := make([]worklogIssue, 0, first.Total)
	appendIssues := func(resp jira.SearchResponse) {
		for _, raw := range resp.Issues {
			issues = append(issues, worklogIssue{Key: raw.Key, Summary: raw.Fields.Summary, Project: raw.Fields.Project.Key})
		}
	}
	appendIssues(first)
	page := 2
	for len(issues) < first.Total {
		before := len(issues)
		resp, err := client.Search(ctx, jql, page, 100, fields)
		if err != nil {
			return nil, fmt.Errorf("search worklog issues page %d: %w", page, err)
		}
		appendIssues(resp)
		if len(issues) == before || len(resp.Issues) == 0 {
			return nil, fmt.Errorf("incomplete worklog issue search (%d of %d) — retry or narrow the query", len(issues), first.Total)
		}
		page++
	}
	return issues, nil
}

func fetchWorklogRecords(ctx context.Context, client worklogClient, issues []worklogIssue, filter worklogFilter) ([]worklogRecord, error) {
	if len(issues) == 0 {
		return []worklogRecord{}, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan worklogIssue)
	results := make(chan []worklogRecord, len(issues))
	errs := make(chan error, 1)
	var wg sync.WaitGroup
	workers := 8
	if len(issues) < workers {
		workers = len(issues)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for issue := range jobs {
				logs, err := client.GetIssueWorklogs(ctx, issue.Key)
				if err != nil {
					select {
					case errs <- fmt.Errorf("fetch worklogs for %s: %w", issue.Key, err):
						cancel()
					default:
					}
					continue
				}
				records := make([]worklogRecord, 0, len(logs))
				for _, log := range logs {
					if !worklogMatches(log, filter) {
						continue
					}
					records = append(records, worklogRecord{ID: log.ID, IssueID: log.IssueID, IssueKey: issue.Key, IssueSummary: issue.Summary, Author: log.Author, UpdateAuthor: log.UpdateAuthor, Comment: log.Comment, Started: log.Started, Created: log.Created, Updated: log.Updated, TimeSpent: log.TimeSpent, TimeSpentSeconds: log.TimeSpentSeconds})
				}
				results <- records
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, issue := range issues {
			select {
			case jobs <- issue:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	var records []worklogRecord
	for batch := range results {
		records = append(records, batch...)
	}
	select {
	case err := <-errs:
		return nil, err
	default:
	}
	return records, nil
}

func worklogMatches(log jira.Worklog, filter worklogFilter) bool {
	if filter.Author != "" && log.Author.Name != filter.Author && log.Author.DisplayName != filter.Author {
		return false
	}
	raw := log.Started
	switch filter.DateField {
	case "created":
		raw = log.Created
	case "updated":
		raw = log.Updated
	}
	t, err := parseJiraTime(raw)
	if err != nil {
		return false
	}
	if !filter.Since.IsZero() && t.Before(filter.Since) {
		return false
	}
	if !filter.Until.IsZero() && t.After(filter.Until) {
		return false
	}
	return true
}

func worklogTime(rec worklogRecord, field string) (time.Time, error) {
	raw := rec.Started
	switch field {
	case "created":
		raw = rec.Created
	case "updated":
		raw = rec.Updated
	}
	return parseJiraTime(raw)
}

func sortWorklogRecords(records []worklogRecord, field, direction string) {
	sort.SliceStable(records, func(i, j int) bool {
		ti, _ := worklogTime(records[i], field)
		tj, _ := worklogTime(records[j], field)
		if !ti.Equal(tj) {
			if direction == "desc" {
				return ti.After(tj)
			}
			return ti.Before(tj)
		}
		if records[i].IssueKey != records[j].IssueKey {
			if direction == "desc" {
				return records[i].IssueKey > records[j].IssueKey
			}
			return records[i].IssueKey < records[j].IssueKey
		}
		li, ei := strconv.ParseInt(records[i].ID, 10, 64)
		lj, ej := strconv.ParseInt(records[j].ID, 10, 64)
		if ei == nil && ej == nil {
			if direction == "desc" {
				return li > lj
			}
			return li < lj
		}
		if direction == "desc" {
			return records[i].ID > records[j].ID
		}
		return records[i].ID < records[j].ID
	})
}

func formatWorklogDuration(seconds int64) string {
	if seconds == 0 {
		return "0h"
	}
	return jira.FormatSeconds(seconds)
}

func runWorklog(ctx context.Context, flags worklogFlags, mode string) (string, error) {
	now := time.Now()
	if flags.DateField != "started" && flags.DateField != "created" && flags.DateField != "updated" {
		return "", fmt.Errorf("invalid --date-field %q — choose started, created, or updated", flags.DateField)
	}
	if flags.GroupBy != "" && mode != "summary" {
		return "", fmt.Errorf("--group-by is only valid for worklog summary")
	}
	if flags.GroupBy != "" && flags.GroupBy != "day" && flags.GroupBy != "week" && flags.GroupBy != "issue" && flags.GroupBy != "project" {
		return "", fmt.Errorf("invalid --group-by %q — choose day, week, issue, or project", flags.GroupBy)
	}
	if flags.Issue != "" && flags.Project != "" {
		return "", fmt.Errorf("--issue and --project are mutually exclusive — choose one")
	}
	if flags.Issue != "" {
		ref, err := jira.ParseRef(flags.Issue)
		if err != nil || ref.Kind != jira.RefIssue {
			return "", fmt.Errorf("--issue requires a plain issue key — run: jiracli show %s", flags.Issue)
		}
		flags.Issue = ref.Key
	}
	if mode == "list" || mode == "show" {
		if flags.Sort != "" && flags.Sort != "asc" && flags.Sort != "desc" {
			return "", fmt.Errorf("invalid --sort %q — choose asc or desc", flags.Sort)
		}
		if flags.Page < 1 {
			flags.Page = 1
		}
		if flags.Limit <= 0 {
			flags.Limit = 50
		}
		if flags.Limit > 200 {
			flags.Limit = 200
		}
	}
	entry, err := resolveEntry(flags.Profile)
	if err != nil {
		return "", err
	}
	return runWorklogWithClient(ctx, jira.New(entry), entry, flags, mode, now)
}

func runWorklogWithClient(ctx context.Context, client worklogClient, entry keychain.Entry, flags worklogFlags, mode string, now time.Time) (string, error) {
	requiredAuthor := mode != "show"
	author, authorDisplayName, authorCurrent, err := resolveWorklogAuthor(ctx, client, entry, flags.Author, requiredAuthor)
	if err != nil {
		return "", err
	}
	filter, err := parseWorklogFilter(flags, now, author)
	if err != nil {
		return "", err
	}
	records, err := collectWorklogs(ctx, client, filter, authorCurrent)
	if err != nil {
		return "", err
	}
	if mode == "latest" {
		return renderLatest(records, filter, flags.JSON)
	}
	if mode == "summary" {
		return renderSummary(records, filter, author, authorDisplayName, flags, now)
	}
	if flags.Sort == "" {
		flags.Sort = "desc"
	}
	sortWorklogRecords(records, filter.DateField, flags.Sort)
	return renderWorklogList(records, filter, authorDisplayName, flags, mode == "show")
}

func renderLatest(records []worklogRecord, filter worklogFilter, jsonMode bool) (string, error) {
	if len(records) == 0 {
		if jsonMode {
			return "", nil
		}
		return fmt.Sprintf("No visible worklogs found for %s.\n", filterScope(filter)), nil
	}
	sortWorklogRecords(records, filter.DateField, "desc")
	if jsonMode {
		b, err := json.Marshal(records[0])
		return string(b) + "\n", err
	}
	r := records[0]
	var sb strings.Builder
	fmt.Fprintf(&sb, "Entered: %s\nWork started: %s\nUpdated: %s\nDuration: %s\nIssue: %s\nAuthor: %s\nOrdered by: %s\n", formatJiraTimestamp(r.Created), formatJiraTimestamp(r.Started), formatJiraTimestamp(r.Updated), formatWorklogDuration(r.TimeSpentSeconds), r.IssueKey, authorDisplay(r.Author), filter.DateField)
	if r.Comment != "" {
		fmt.Fprintf(&sb, "Comment: %s\n", r.Comment)
	}
	return sb.String(), nil
}

func renderWorklogList(records []worklogRecord, filter worklogFilter, author string, flags worklogFlags, show bool) (string, error) {
	page := flags.Page
	if page < 1 {
		page = 1
	}
	limit := flags.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	start := (page - 1) * limit
	end := start + limit
	if start > len(records) {
		start = len(records)
	}
	if end > len(records) {
		end = len(records)
	}
	pageRecords := records[start:end]
	hasMore := end < len(records)
	if flags.JSON {
		var sb strings.Builder
		for _, r := range pageRecords {
			b, err := json.Marshal(r)
			if err != nil {
				return "", err
			}
			sb.Write(b)
			sb.WriteByte('\n')
		}
		if hasMore {
			p := worklogPagination{}
			p.Pagination = struct {
				Page     int  `json:"page"`
				Pages    int  `json:"pages"`
				Total    int  `json:"total"`
				NextPage int  `json:"next_page"`
				HasMore  bool `json:"has_more"`
			}{Page: page, Pages: (len(records) + limit - 1) / limit, Total: len(records), NextPage: page + 1, HasMore: true}
			b, _ := json.Marshal(p)
			sb.Write(b)
			sb.WriteByte('\n')
		}
		return sb.String(), nil
	}
	if len(records) == 0 {
		if show {
			return fmt.Sprintf("%s has no visible worklogs matching the filters.\n", filter.Issue), nil
		}
		return fmt.Sprintf("No visible worklogs found for %s.\n", filterScope(filter)), nil
	}
	if author == "" {
		author = "all visible authors"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Author: %s\nScope: %s\nDate field: %s\n", author, filterScope(filter), filter.DateField)
	for i, r := range pageRecords {
		t, _ := worklogTime(r, filter.DateField)
		fmt.Fprintf(&sb, "[%d] %s  %s  %s  %s\n", start+i+1, r.IssueKey, t.UTC().Format("2006-01-02 15:04"), formatWorklogDuration(r.TimeSpentSeconds), authorDisplay(r.Author))
		fmt.Fprintf(&sb, "    entered %s  updated %s", formatJiraTimestamp(r.Created), formatJiraTimestamp(r.Updated))
		if r.IssueSummary != "" {
			fmt.Fprintf(&sb, "  %q", jira.TruncateString(r.IssueSummary, 80))
		}
		sb.WriteByte('\n')
		if r.Comment != "" {
			for _, line := range strings.Split(jira.WrapAt(r.Comment, 76, 4), "\n") {
				fmt.Fprintf(&sb, "    > %s\n", line)
			}
		}
	}
	pages := (len(records) + limit - 1) / limit
	if pages < 1 {
		pages = 1
	}
	if hasMore {
		fmt.Fprintf(&sb, "--- page %d of %d | next: %s ---\n", page, pages, nextWorklogCommand(filter, flags, page+1, limit, show))
	} else {
		fmt.Fprintf(&sb, "--- page %d of %d ---\n", page, pages)
	}
	return sb.String(), nil
}

func renderSummary(records []worklogRecord, filter worklogFilter, author, authorDisplayName string, flags worklogFlags, now time.Time) (string, error) {
	var total int64
	for _, r := range records {
		total += r.TimeSpentSeconds
	}
	var groups []worklogSummaryGroup
	if flags.GroupBy != "" {
		groups = groupWorklogs(records, filter.DateField, flags.GroupBy)
	}
	s := worklogSummaryRecord{
		Author:            author,
		AuthorDisplayName: authorDisplayName,
		DateField:         filter.DateField,
		EntryCount:        len(records),
		TimeSpent:         formatWorklogSummaryDuration(total),
		TimeSpentSeconds:  total,
		GroupBy:           flags.GroupBy,
	}
	if !filter.Since.IsZero() {
		s.Since = filter.Since.UTC().Format(time.RFC3339)
	}
	if !filter.Until.IsZero() {
		s.Until = filter.Until.UTC().Format(time.RFC3339)
	}
	if flags.JSON {
		if flags.GroupBy != "" {
			s.Groups = &groups
		}
		b, err := json.Marshal(s)
		return string(b) + "\n", err
	}
	if authorDisplayName == "" {
		authorDisplayName = author
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Worklog summary for %s\n", authorDisplayName)
	fmt.Fprintf(&sb, "Period:   %s\n", worklogSummaryPeriod(filter, flags, now))
	fmt.Fprintf(&sb, "Based on: %s\n", worklogDateFieldLabel(filter.DateField))
	if flags.GroupBy == "" {
		fmt.Fprintf(&sb, "Entries:  %d\nTotal:    %s\n", len(records), formatWorklogSummaryDuration(total))
		return sb.String(), nil
	}
	sb.WriteByte('\n')
	sb.WriteString(renderWorklogSummaryTable(groups, flags.GroupBy, len(records), total))
	return sb.String(), nil
}

func formatWorklogSummaryDuration(seconds int64) string {
	if seconds <= 0 {
		return "0h"
	}
	const secondsPerWorkWeek = 5 * 8 * 60 * 60
	weeks := seconds / secondsPerWorkWeek
	if weeks == 0 {
		return jira.FormatSeconds(seconds)
	}
	remainder := seconds % secondsPerWorkWeek
	if remainder == 0 {
		return fmt.Sprintf("%dw", weeks)
	}
	return fmt.Sprintf("%dw%s", weeks, jira.FormatSeconds(remainder))
}

func worklogSummaryPeriod(filter worklogFilter, flags worklogFlags, now time.Time) string {
	date := func(t time.Time) string { return t.UTC().Format("2006-01-02") }
	switch {
	case !filter.Since.IsZero() && !filter.Until.IsZero():
		return date(filter.Since) + " → " + date(filter.Until)
	case !filter.Since.IsZero():
		period := date(filter.Since) + " → " + date(now)
		if rolling := rollingPeriodLabel(flags.Since); rolling != "" {
			period += " (" + rolling + ")"
		}
		return period
	case !filter.Until.IsZero():
		return "through " + date(filter.Until)
	default:
		return "all visible worklogs"
	}
}

func rollingPeriodLabel(value string) string {
	if len(value) < 2 {
		return ""
	}
	n, err := strconv.Atoi(value[:len(value)-1])
	if err != nil || n <= 0 {
		return ""
	}
	switch value[len(value)-1] {
	case 'w':
		return fmt.Sprintf("rolling %d days", n*7)
	case 'd':
		return fmt.Sprintf("rolling %d days", n)
	case 'h':
		return fmt.Sprintf("rolling %d hours", n)
	default:
		return ""
	}
}

func worklogDateFieldLabel(field string) string {
	switch field {
	case "created":
		return "entry time"
	case "updated":
		return "update time"
	default:
		return "work date"
	}
}

func renderWorklogSummaryTable(groups []worklogSummaryGroup, groupBy string, totalEntries int, totalSeconds int64) string {
	label := worklogGroupLabel(groupBy)
	labelWidth := max(10, len(label), len("Total"))
	entriesWidth := len("Entries")
	timeWidth := len("Time")
	for _, group := range groups {
		labelWidth = max(labelWidth, len(group.Group))
		entriesWidth = max(entriesWidth, len(strconv.Itoa(group.EntryCount)))
		timeWidth = max(timeWidth, len(group.TimeSpent))
	}
	totalTime := formatWorklogSummaryDuration(totalSeconds)
	entriesWidth = max(entriesWidth, len(strconv.Itoa(totalEntries)))
	timeWidth = max(timeWidth, len(totalTime))

	var sb strings.Builder
	fmt.Fprintf(&sb, "%-*s  %*s  %*s\n", labelWidth, label, entriesWidth, "Entries", timeWidth, "Time")
	for _, group := range groups {
		fmt.Fprintf(&sb, "%-*s  %*d  %*s\n", labelWidth, group.Group, entriesWidth, group.EntryCount, timeWidth, group.TimeSpent)
	}
	sb.WriteString(strings.Repeat("─", labelWidth+entriesWidth+timeWidth+4))
	sb.WriteByte('\n')
	fmt.Fprintf(&sb, "%-*s  %*d  %*s\n", labelWidth, "Total", entriesWidth, totalEntries, timeWidth, totalTime)
	return sb.String()
}

func worklogGroupLabel(groupBy string) string {
	switch groupBy {
	case "day":
		return "Day"
	case "week":
		return "Week"
	case "project":
		return "Project"
	default:
		return "Issue"
	}
}

func groupWorklogs(records []worklogRecord, field, groupBy string) []worklogSummaryGroup {
	groups := make(map[string]*worklogSummaryGroup)
	for _, r := range records {
		t, _ := worklogTime(r, field)
		key := ""
		switch groupBy {
		case "day":
			key = t.UTC().Format("2006-01-02")
		case "week":
			u := t.UTC()
			monday := u.AddDate(0, 0, -int((int(u.Weekday())+6)%7))
			key = monday.Format("2006-01-02")
		case "issue":
			key = r.IssueKey
		case "project":
			key = r.IssueKey
			if i := strings.LastIndex(key, "-"); i > 0 {
				key = key[:i]
			}
		}
		g := groups[key]
		if g == nil {
			g = &worklogSummaryGroup{Group: key}
			groups[key] = g
		}
		g.EntryCount++
		g.TimeSpentSeconds += r.TimeSpentSeconds
	}
	out := make([]worklogSummaryGroup, 0, len(groups))
	for _, g := range groups {
		g.TimeSpent = formatWorklogSummaryDuration(g.TimeSpentSeconds)
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Group < out[j].Group })
	return out
}

func filterScope(filter worklogFilter) string {
	if filter.Issue != "" {
		return filter.Issue
	}
	if filter.Project != "" {
		return "project " + filter.Project
	}
	return "all issues"
}

func nextWorklogCommand(filter worklogFilter, flags worklogFlags, page, limit int, show bool) string {
	args := []string{"jiracli"}
	if show {
		args = append(args, "show", "worklogs", filter.Issue)
	} else {
		args = append(args, "worklog", "list")
	}
	add := func(name, value string) {
		if value != "" {
			args = append(args, name, strconv.Quote(value))
		}
	}
	add("--profile", flags.Profile)
	if flags.Author != "" {
		add("--author", flags.Author)
	}
	if !show {
		add("--issue", flags.Issue)
		add("--project", flags.Project)
		if flags.AllIssues {
			args = append(args, "--all-issues")
		}
	}
	add("--since", flags.Since)
	add("--until", flags.Until)
	add("--date-field", filter.DateField)
	add("--sort", flags.Sort)
	args = append(args, "--limit", strconv.Itoa(limit), "--page", strconv.Itoa(page))
	return strings.Join(args, " ")
}
func authorDisplay(u jira.WorklogUser) string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Name
}
