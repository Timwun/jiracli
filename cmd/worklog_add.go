package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cgrossde/jiracli/internal/jira"
)

// WorklogAddFlags holds parsed flag values for the worklog creation command.
type WorklogAddFlags struct {
	Profile        string
	Comment        string
	Started        string
	AdjustEstimate string
	Yes            bool
}

// NewWorklogAddCmd builds the "add worklog" command.
func NewWorklogAddCmd() *cobra.Command {
	var flags WorklogAddFlags
	c := &cobra.Command{
		Use:   "worklog <KEY> <time-spent>",
		Short: "Log work on an issue (dry-run by default; use --yes to apply)",
		Example: `  jiracli add worklog ACME-123 2h
  jiracli add worklog ACME-123 2h --started 2026-09-23T09:00:00Z --comment "Implementation" --yes
  jiracli add worklog ACME-123 30m --adjust-estimate leave`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := WorklogAdd(cmd.Context(), flags, args[0], args[1])
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), result)
			return err
		},
	}
	c.Flags().StringVar(&flags.Profile, "profile", "", "Profile name")
	c.Flags().StringVar(&flags.Comment, "comment", "", "Jira Wiki Markup comment")
	c.Flags().StringVar(&flags.Started, "started", "", "Work-start instant in RFC3339 format")
	c.Flags().StringVar(&flags.AdjustEstimate, "adjust-estimate", "auto", "Remaining-estimate policy: auto or leave")
	c.Flags().BoolVar(&flags.Yes, "yes", false, "Apply without confirmation")
	return c
}

// WorklogAdd is the Layer 1 implementation for adding a worklog.
func WorklogAdd(ctx context.Context, flags WorklogAddFlags, key, timeSpent string) (string, error) {
	entry, err := resolveEntry(flags.Profile)
	if err != nil {
		return "", err
	}
	return worklogAddWithClient(ctx, jira.New(entry), entry.URL, flags, key, timeSpent)
}

func worklogAddWithClient(ctx context.Context, client *jira.Client, baseURL string, flags WorklogAddFlags, key, timeSpent string) (string, error) {
	adjustEstimate := flags.AdjustEstimate
	if adjustEstimate == "" {
		adjustEstimate = "auto"
	}
	if adjustEstimate != "auto" && adjustEstimate != "leave" {
		return "", fmt.Errorf("invalid --adjust-estimate %s — choose auto or leave", adjustEstimate)
	}
	if strings.TrimSpace(timeSpent) == "" {
		return "", fmt.Errorf("time-spent is required — provide Jira duration syntax such as 2h or 30m")
	}

	body := map[string]any{
		"timeSpent": timeSpent,
	}
	if flags.Comment != "" {
		body["comment"] = flags.Comment
	}
	if flags.Started != "" {
		started, err := time.Parse(time.RFC3339, flags.Started)
		if err != nil {
			return "", fmt.Errorf("invalid --started %q — use RFC3339, for example 2026-09-23T09:00:00Z", flags.Started)
		}
		body["started"] = started.Format("2006-01-02T15:04:05.000-0700")
	}

	if _, err := client.GetIssue(ctx, key, "key", false); err != nil {
		return "", fmt.Errorf("validate issue %s: %w", key, err)
	}

	description := fmt.Sprintf("+ log %s on %s; ", timeSpent, key)
	if adjustEstimate == "auto" {
		description += "Jira will automatically reduce the remaining estimate"
	} else {
		description += "the remaining estimate will be left unchanged"
	}
	p := jira.Preview{
		Method:      "POST",
		Path:        "/issue/" + key + "/worklog",
		Query:       url.Values{"adjustEstimate": []string{adjustEstimate}},
		Body:        body,
		Description: description,
		Validation:  []jira.ValidationRow{{Status: "✓", Message: key + " is accessible"}},
	}

	var decodeErr error
	result, err := HandleWrite(ctx, client, baseURL, p, flags.Yes, func(resp []byte) string {
		var worklog jira.Worklog
		if err := json.Unmarshal(resp, &worklog); err != nil {
			decodeErr = fmt.Errorf("parse worklog response: %w", err)
			return ""
		}
		duration := worklog.TimeSpent
		if duration == "" {
			duration = timeSpent
		}
		id := ""
		if worklog.ID != "" {
			id = fmt.Sprintf(" (worklog id %s)", worklog.ID)
		}
		return fmt.Sprintf("✓ logged %s on %s%s\n  → jiracli show worklogs %s\n", duration, key, id, key)
	})
	if err != nil {
		return "", err
	}
	if decodeErr != nil {
		return "", decodeErr
	}
	return result, nil
}
