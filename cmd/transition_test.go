package cmd

import (
	"strings"
	"testing"

	"github.com/cgrossde/jiracli/internal/jira"
)

// ---------------------------------------------------------------------------
// parseTransitionFields
// ---------------------------------------------------------------------------

func TestParseTransitionFields_basic(t *testing.T) {
	fields, err := parseTransitionFields([]string{"resolution=Fixed", "fixVersions=2.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	res, ok := fields["resolution"].(map[string]any)
	if !ok || res["name"] != "Fixed" {
		t.Errorf("resolution: want {name:Fixed}, got %v", fields["resolution"])
	}
	fv, ok := fields["fixVersions"].([]map[string]any)
	if !ok || len(fv) != 1 || fv[0]["name"] != "2.0" {
		t.Errorf("fixVersions: want [{name:2.0}], got %v", fields["fixVersions"])
	}
}

func TestParseTransitionFields_multipleFixVersions(t *testing.T) {
	fields, err := parseTransitionFields([]string{"fixVersions=1.0", "fixVersions=2.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fv, ok := fields["fixVersions"].([]map[string]any)
	if !ok || len(fv) != 2 {
		t.Errorf("expected 2 fixVersions entries, got %v", fields["fixVersions"])
	}
}

func TestParseTransitionFields_rejectsAddOperator(t *testing.T) {
	_, err := parseTransitionFields([]string{"labels+=foo"})
	if err == nil {
		t.Fatal("expected error for += operator, got nil")
	}
	if !strings.Contains(err.Error(), "only support =") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestParseTransitionFields_rejectsInvalidSpec(t *testing.T) {
	_, err := parseTransitionFields([]string{"noequalssign"})
	if err == nil {
		t.Fatal("expected error for missing operator, got nil")
	}
}

func TestParseTransitionFields_empty(t *testing.T) {
	fields, err := parseTransitionFields(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 0 {
		t.Errorf("expected empty map, got %v", fields)
	}
}

func TestParseTransitionFields_unknownFieldIsRawString(t *testing.T) {
	fields, err := parseTransitionFields([]string{"customfield_12345=some value"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fields["customfield_12345"] != "some value" {
		t.Errorf("want raw string, got %v", fields["customfield_12345"])
	}
}

// ---------------------------------------------------------------------------
// buildTransitionFieldsError
// ---------------------------------------------------------------------------

func makeTransitionRaw(fields map[string]jira.TransitionFieldMeta) jira.TransitionRaw {
	return jira.TransitionRaw{
		ID:     "31",
		Name:   "Closed",
		Fields: fields,
	}
}

func TestBuildTransitionFieldsError_surfacesJiraMessages(t *testing.T) {
	te := &jira.TransitionError{
		FieldErrors:   map[string]string{"resolution": "The selected resolution cannot be chosen during this action"},
		ErrorMessages: []string{"Workflow validation failed"},
	}
	tr := makeTransitionRaw(map[string]jira.TransitionFieldMeta{
		"resolution": {
			Required: true,
			Name:     "Resolution",
			AllowedValues: []struct {
				ID    string `json:"id"`
				Name  string `json:"name"`
				Value string `json:"value"`
			}{
				{Name: "Fixed"},
				{Name: "Won't Fix"},
				{Name: "Duplicate"},
			},
		},
	})
	msg := buildTransitionFieldsError(te, tr, "MOBTC-123", map[string]any{})

	if !strings.Contains(msg, "resolution: The selected resolution cannot be chosen") {
		t.Errorf("expected Jira field error, got:\n%s", msg)
	}
	if !strings.Contains(msg, "Workflow validation failed") {
		t.Errorf("expected top-level error message, got:\n%s", msg)
	}
}

func TestBuildTransitionFieldsError_suggestsMissingRequiredField(t *testing.T) {
	te := &jira.TransitionError{
		FieldErrors: map[string]string{"resolution": "Field required"},
	}
	tr := makeTransitionRaw(map[string]jira.TransitionFieldMeta{
		"resolution": {
			Required: true,
			Name:     "Resolution",
			AllowedValues: []struct {
				ID    string `json:"id"`
				Name  string `json:"name"`
				Value string `json:"value"`
			}{
				{Name: "Fixed"},
				{Name: "Won't Fix"},
			},
		},
		"fixVersions": {
			Required: true,
			Name:     "Fix Version/s",
		},
	})
	msg := buildTransitionFieldsError(te, tr, "MOBTC-123", map[string]any{})

	if !strings.Contains(msg, "--field resolution=") {
		t.Errorf("expected --field resolution suggestion, got:\n%s", msg)
	}
	if !strings.Contains(msg, "--field fixVersions=") {
		t.Errorf("expected --field fixVersions suggestion, got:\n%s", msg)
	}
	if !strings.Contains(msg, "Fixed") {
		t.Errorf("expected allowedValues in suggestion, got:\n%s", msg)
	}
	if !strings.Contains(msg, "jiracli edit status MOBTC-123") {
		t.Errorf("expected corrective run command, got:\n%s", msg)
	}
}

func TestBuildTransitionFieldsError_doesNotSuggestAlreadyProvidedFields(t *testing.T) {
	te := &jira.TransitionError{
		FieldErrors: map[string]string{"resolution": "Bad value"},
	}
	tr := makeTransitionRaw(map[string]jira.TransitionFieldMeta{
		"resolution": {Required: true, Name: "Resolution"},
		"fixVersions": {
			Required: true,
			Name:     "Fix Version/s",
			AllowedValues: []struct {
				ID    string `json:"id"`
				Name  string `json:"name"`
				Value string `json:"value"`
			}{
				{Name: "Future"},
			},
		},
	})
	// fixVersions already provided — should not appear in "not yet provided"
	provided := map[string]any{"fixVersions": []map[string]any{{"name": "Future"}}}
	msg := buildTransitionFieldsError(te, tr, "MOBTC-123", provided)

	if strings.Contains(msg, "--field fixVersions=") {
		t.Errorf("fixVersions was already provided but appeared in suggestions:\n%s", msg)
	}
}

func TestBuildTransitionFieldsError_allFieldsProvided(t *testing.T) {
	te := &jira.TransitionError{
		FieldErrors: map[string]string{"resolution": "Bad value for resolution"},
	}
	tr := makeTransitionRaw(map[string]jira.TransitionFieldMeta{
		"resolution": {Required: true, Name: "Resolution"},
	})
	provided := map[string]any{"resolution": map[string]any{"name": "Bogus"}}
	msg := buildTransitionFieldsError(te, tr, "MOBTC-123", provided)

	if !strings.Contains(msg, "All required fields were provided") {
		t.Errorf("expected 'all required fields' note, got:\n%s", msg)
	}
}

func TestBuildTransitionFieldsError_noFieldsMetaFallback(t *testing.T) {
	// Jira instance didn't return field schema (e.g. old version, no expand support)
	te := &jira.TransitionError{
		FieldErrors: map[string]string{"resolution": "Field required"},
	}
	tr := makeTransitionRaw(nil) // no fields schema
	msg := buildTransitionFieldsError(te, tr, "MOBTC-123", map[string]any{})

	// Should at least surface the Jira error
	if !strings.Contains(msg, "resolution: Field required") {
		t.Errorf("expected Jira field error surfaced, got:\n%s", msg)
	}
}

// ---------------------------------------------------------------------------
// TransitionFieldMeta.AllowedNames
// ---------------------------------------------------------------------------

func TestAllowedNames_deduplicatesAndPreferName(t *testing.T) {
	meta := jira.TransitionFieldMeta{}
	meta.AllowedValues = []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Value string `json:"value"`
	}{
		{Name: "Fixed"},
		{Name: "Fixed"},     // duplicate
		{Value: "Wont Fix"}, // value fallback
		{Name: ""},          // empty — should be skipped
	}
	names := meta.AllowedNames()
	if len(names) != 2 {
		t.Errorf("expected 2 names, got %v", names)
	}
}
