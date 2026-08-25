package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// TransitionFieldMeta describes one field on a transition screen, as returned
// when the caller passes expand=transitions.fields.
type TransitionFieldMeta struct {
	Required      bool     `json:"required"`
	Name          string   `json:"name"`
	AllowedValues []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Value string `json:"value"` // some fields use "value" instead of "name"
	} `json:"allowedValues"`
	Schema struct {
		Type   string `json:"type"`
		System string `json:"system"`
	} `json:"schema"`
}

// AllowedNames returns a deduplicated slice of display names for allowedValues.
// Fields like resolution use "name"; some others use "value". Both are checked.
func (f TransitionFieldMeta) AllowedNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, av := range f.AllowedValues {
		n := av.Name
		if n == "" {
			n = av.Value
		}
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// TransitionRaw is the shape of one transition item returned by the Jira
// /rest/api/2/issue/<KEY>/transitions endpoint.
type TransitionRaw struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	To   struct {
		Name           string `json:"name"`
		StatusCategory struct {
			Name string `json:"name"`
		} `json:"statusCategory"`
	} `json:"to"`
	// Fields is populated when expand=transitions.fields is requested.
	// Keys are field IDs (e.g. "resolution", "fixVersions").
	Fields map[string]TransitionFieldMeta `json:"fields"`
}

// RequiredFields returns the field IDs that are required on this transition
// screen and have not yet been supplied in the provided fields map.
func (t TransitionRaw) MissingRequiredFields(provided map[string]any) []string {
	var missing []string
	for id, meta := range t.Fields {
		if !meta.Required {
			continue
		}
		if _, ok := provided[id]; !ok {
			missing = append(missing, id)
		}
	}
	return missing
}

// TransitionsResponse wraps the transitions array from the API.
type TransitionsResponse struct {
	Transitions []TransitionRaw `json:"transitions"`
}

// TransitionError is returned by DoTransitionWithFields when the server
// rejects the transition with a 400. It carries the raw field-error map from
// Jira (field ID → human-readable message) so callers can build corrective
// suggestions.
type TransitionError struct {
	// FieldErrors maps field ID to Jira's own validation message.
	FieldErrors map[string]string
	// ErrorMessages contains top-level error messages (not field-specific).
	ErrorMessages []string
}

func (e *TransitionError) Error() string {
	var msgs []string
	msgs = append(msgs, e.ErrorMessages...)
	for k, v := range e.FieldErrors {
		msgs = append(msgs, k+": "+v)
	}
	if len(msgs) == 0 {
		return "transition rejected (HTTP 400)"
	}
	out := msgs[0]
	for _, m := range msgs[1:] {
		out += "; " + m
	}
	return out
}

// GetTransitions fetches the available workflow transitions for an issue.
// The expand=transitions.fields parameter is included so the server returns
// the fields schema for each transition (used by the write path in Phase 4).
func (c *Client) GetTransitions(ctx context.Context, key string) ([]TransitionRaw, error) {
	q := url.Values{}
	q.Set("expand", "transitions.fields")
	body, status, err := c.Get(ctx, "/issue/"+key+"/transitions", q)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("get transitions %s: %w", key, MapStatus("", status, body))
	}
	var resp TransitionsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse transitions: %w", err)
	}
	return resp.Transitions, nil
}

// DoTransitionWithFields POSTs a workflow transition on an issue, including
// any transition-screen fields in the "fields" block. comment is optional.
// On HTTP 400 the error is a *TransitionError carrying the parsed field-error
// map — callers can use errors.As to access it for corrective messaging.
func (c *Client) DoTransitionWithFields(ctx context.Context, key, transitionID string, fields map[string]any, comment string) error {
	body := map[string]any{
		"transition": map[string]string{"id": transitionID},
	}
	if len(fields) > 0 {
		body["fields"] = fields
	}
	if comment != "" {
		body["update"] = map[string]any{
			"comment": []map[string]any{
				{"add": map[string]string{"body": comment}},
			},
		}
	}
	raw, _ := json.Marshal(body)
	respBody, status, err := c.Post(ctx, "/issue/"+key+"/transitions", nil, bytes.NewReader(raw), "application/json")
	if err != nil {
		return err
	}
	if status == 204 || status == 200 {
		return nil
	}
	if status == 400 {
		var eb jiraErrorBody
		if jsonErr := json.Unmarshal(respBody, &eb); jsonErr == nil {
			return &TransitionError{
				FieldErrors:   eb.Errors,
				ErrorMessages: eb.ErrorMessages,
			}
		}
	}
	return fmt.Errorf("transition %s: %w", key, MapStatus("", status, respBody))
}

// DoTransition POSTs a workflow transition on an issue.
// comment is optional; if non-empty, it is posted atomically via the update block.
// Deprecated: use DoTransitionWithFields with a nil fields map instead.
func (c *Client) DoTransition(ctx context.Context, key, transitionID, comment string) error {
	return c.DoTransitionWithFields(ctx, key, transitionID, nil, comment)
}
