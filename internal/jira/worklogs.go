package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// WorklogUser is the user reference embedded in a Jira worklog.
type WorklogUser struct {
	Name         string `json:"name"`
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress,omitempty"`
	AccountID    string `json:"accountId,omitempty"`
}

// Worklog is a single Jira issue worklog. Timestamp strings remain in Jira's
// transport representation so callers can apply their own time semantics.
type Worklog struct {
	ID               string      `json:"id"`
	IssueID          string      `json:"issueId"`
	Author           WorklogUser `json:"author"`
	UpdateAuthor     WorklogUser `json:"updateAuthor"`
	Comment          string      `json:"comment"`
	Started          string      `json:"started"`
	Created          string      `json:"created"`
	Updated          string      `json:"updated"`
	TimeSpent        string      `json:"timeSpent"`
	TimeSpentSeconds int64       `json:"timeSpentSeconds"`
}

// WorklogsResponse is the paginated response from /issue/<KEY>/worklog.
type WorklogsResponse struct {
	StartAt    int       `json:"startAt"`
	MaxResults int       `json:"maxResults"`
	Total      int       `json:"total"`
	Worklogs   []Worklog `json:"worklogs"`
}

// GetIssueWorklogs fetches every visible worklog for an issue. Jira versions
// differ: some honor pagination and others return every entry regardless of
// maxResults. Duplicate IDs and non-progressing pages are rejected rather than
// returning incomplete data.
func (c *Client) GetIssueWorklogs(ctx context.Context, key string) ([]Worklog, error) {
	const pageSize = 100
	startAt := 0
	seen := make(map[string]struct{})
	worklogs := make([]Worklog, 0)
	total := -1

	for {
		q := url.Values{}
		q.Set("startAt", strconv.Itoa(startAt))
		q.Set("maxResults", strconv.Itoa(pageSize))
		body, status, err := c.Get(ctx, "/issue/"+key+"/worklog", q)
		if err != nil {
			return nil, err
		}
		if status != 200 {
			return nil, fmt.Errorf("get worklogs %s: %w", key, MapStatus("", status, body))
		}
		var resp WorklogsResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("parse worklogs: %w", err)
		}
		if total < 0 {
			total = resp.Total
		}
		if resp.Total > total {
			total = resp.Total
		}
		if total == 0 {
			return []Worklog{}, nil
		}

		added := 0
		for _, worklog := range resp.Worklogs {
			if _, ok := seen[worklog.ID]; ok {
				continue
			}
			seen[worklog.ID] = struct{}{}
			worklogs = append(worklogs, worklog)
			added++
		}
		if len(worklogs) >= total {
			return worklogs, nil
		}
		if added == 0 || len(resp.Worklogs) == 0 {
			return nil, fmt.Errorf("incomplete worklog response for %s (%d of %d) — retry or inspect the issue in Jira", key, len(worklogs), total)
		}
		startAt += len(resp.Worklogs)
	}
}
