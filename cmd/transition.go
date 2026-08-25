package cmd

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cgrossde/jiracli/internal/jira"
)

// TransitionFlags holds parsed flag values for the transition write command.
type TransitionFlags struct {
	Profile string
	Comment string
	Fields  []string // raw "key=value" tokens from --field
	Yes     bool
}

// NewTransitionCmd builds the "transition" write command.
func NewTransitionCmd() *cobra.Command {
	var flags TransitionFlags
	c := &cobra.Command{
		Use:   "status <KEY> [KEY...] <name-or-id>",
		Short: "Move one or more issues through a workflow transition (dry-run by default; use --yes to apply)",
		Long: `Move one or more issues through a workflow transition (dry-run by default; use --yes to apply).

The last argument is always the transition name or numeric ID. All preceding
arguments are treated as issue keys. With multiple keys the preview lists every
planned operation; execution is sequential and per-key errors are collected
rather than aborting the whole batch.

Transition screens may require additional fields (e.g. resolution, fixVersions).
Pass them with --field. If the transition is rejected because of missing fields,
jiracli will tell you exactly which fields are required and what values are allowed.

See also:
  jiracli show transitions <KEY>    list valid target states for this issue`,
		Example: `  jiracli edit status ACME-123 Done
  jiracli edit status ACME-123 Closed --field resolution="Won't Fix" --field fixVersions=Future --yes
  jiracli edit status ACME-123 ACME-124 ACME-125 "In Review" --yes
  jiracli edit status ACME-123 Done --comment "shipped" --yes`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			keys := args[:len(args)-1]
			nameOrID := args[len(args)-1]
			if len(keys) == 1 {
				result, err := Transition(cmd.Context(), flags, keys[0], nameOrID)
				if err != nil {
					return err
				}
				fmt.Fprint(cmd.OutOrStdout(), result)
				return nil
			}
			result, err := TransitionBulk(cmd.Context(), flags, keys, nameOrID)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), result)
			return nil
		},
	}
	c.Flags().StringVar(&flags.Profile, "profile", "", "Profile name")
	c.Flags().StringVar(&flags.Comment, "comment", "", "Comment to post atomically with the transition")
	c.Flags().StringArrayVar(&flags.Fields, "field", nil, "Transition screen field as key=value (repeatable; e.g. --field resolution=\"Won't Fix\")")
	c.Flags().BoolVar(&flags.Yes, "yes", false, "Apply without confirmation")
	return c
}

// parseTransitionFields parses the --field tokens into a map suitable for the
// "fields" block of the transition POST body. Only the "=" operator is
// accepted. Well-known list fields (fixVersions, versions) accumulate into
// slices; object fields (resolution, assignee, reporter, priority) are coerced
// to {"name": value}; unknown fields fall back to a raw string.
func parseTransitionFields(tokens []string) (map[string]any, error) {
	out := map[string]any{}
	for _, tok := range tokens {
		spec, err := jira.ParseFieldSpec(tok)
		if err != nil {
			return nil, err
		}
		if spec.Op != jira.OpReplace {
			return nil, fmt.Errorf("transition fields only support = operator (got %q — use --field %s=%s)", tok, spec.Name, spec.Raw)
		}
		name := strings.ToLower(spec.Name)
		switch name {
		case "fixversions", "versions":
			existing, _ := out[spec.Name].([]map[string]any)
			out[spec.Name] = append(existing, map[string]any{"name": spec.Raw})
		case "resolution", "assignee", "reporter", "priority":
			// Jira expects object fields as {"name": value}.
			out[spec.Name] = map[string]any{"name": spec.Raw}
		default:
			out[spec.Name] = spec.Raw
		}
	}
	return out, nil
}

// buildTransitionFieldsError converts a *jira.TransitionError into a
// corrective human-readable message. It cross-references the error's field
// map against the transition's schema to suggest --field flags and show
// allowed values.
func buildTransitionFieldsError(te *jira.TransitionError, t jira.TransitionRaw, key string, providedFields map[string]any) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("transition %q on %s was rejected by Jira:\n", t.Name, key))

	// Surface Jira's own per-field messages first.
	if len(te.FieldErrors) > 0 {
		// Sort for deterministic output.
		fieldIDs := make([]string, 0, len(te.FieldErrors))
		for id := range te.FieldErrors {
			fieldIDs = append(fieldIDs, id)
		}
		sort.Strings(fieldIDs)
		for _, id := range fieldIDs {
			sb.WriteString(fmt.Sprintf("  ✗ %s: %s\n", id, te.FieldErrors[id]))
		}
	}
	for _, msg := range te.ErrorMessages {
		sb.WriteString(fmt.Sprintf("  ✗ %s\n", msg))
	}

	// Collect all required fields on this transition screen.
	type requiredField struct {
		id            string
		meta          jira.TransitionFieldMeta
		alreadyFailed bool // already listed above in FieldErrors
	}
	var required []requiredField
	for id, meta := range t.Fields {
		if !meta.Required {
			continue
		}
		required = append(required, requiredField{
			id:            id,
			meta:          meta,
			alreadyFailed: te.FieldErrors[id] != "",
		})
	}
	sort.Slice(required, func(i, j int) bool { return required[i].id < required[j].id })

	// Find which required fields are missing from the provided set.
	var missing []requiredField
	for _, r := range required {
		if _, ok := providedFields[r.id]; !ok {
			missing = append(missing, r)
		}
	}

	if len(missing) > 0 {
		sb.WriteString("\nRequired transition screen fields not yet provided:\n")
		for _, r := range missing {
			label := r.meta.Name
			if label == "" {
				label = r.id
			}
			names := r.meta.AllowedNames()
			if len(names) > 0 {
				sb.WriteString(fmt.Sprintf("  --field %s=<value>   (%s)\n", r.id, label))
				sb.WriteString(fmt.Sprintf("    Allowed: %s\n", strings.Join(names, ", ")))
			} else {
				sb.WriteString(fmt.Sprintf("  --field %s=<value>   (%s)\n", r.id, label))
			}
		}
	} else if len(t.Fields) > 0 {
		// All required fields were supplied but something still failed.
		// Show all screen fields as a reference.
		sb.WriteString("\nAll required fields were provided. Check the values above.\n")
		sb.WriteString("Transition screen fields:\n")
		type screenField struct {
			id   string
			meta jira.TransitionFieldMeta
		}
		var all []screenField
		for id, meta := range t.Fields {
			all = append(all, screenField{id, meta})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].id < all[j].id })
		for _, f := range all {
			req := " "
			if f.meta.Required {
				req = "*"
			}
			label := f.meta.Name
			if label == "" {
				label = f.id
			}
			names := f.meta.AllowedNames()
			if len(names) > 0 {
				sb.WriteString(fmt.Sprintf("  %s --field %s=<value>   (%s)\n", req, f.id, label))
				sb.WriteString(fmt.Sprintf("    Allowed: %s\n", strings.Join(names, ", ")))
			} else {
				sb.WriteString(fmt.Sprintf("  %s --field %s=<value>   (%s)\n", req, f.id, label))
			}
		}
	}

	// Suggest the corrected command.
	var fieldFlags []string
	for _, r := range missing {
		names := r.meta.AllowedNames()
		placeholder := "<value>"
		if len(names) > 0 {
			placeholder = fmt.Sprintf("%q", names[0])
		}
		fieldFlags = append(fieldFlags, fmt.Sprintf("--field %s=%s", r.id, placeholder))
	}
	if len(fieldFlags) > 0 {
		sb.WriteString(fmt.Sprintf("\nRun: jiracli edit status %s %q %s --yes\n",
			key, t.Name, strings.Join(fieldFlags, " ")))
	}

	return strings.TrimRight(sb.String(), "\n")
}

// resolveTransition finds a transition by name (case-insensitive) or numeric
// ID from the provided list. Returns nil when nothing matches.
func resolveTransition(transitions []jira.TransitionRaw, nameOrID string) *jira.TransitionRaw {
	if _, numErr := strconv.Atoi(nameOrID); numErr == nil {
		for i := range transitions {
			if transitions[i].ID == nameOrID {
				return &transitions[i]
			}
		}
		return nil
	}
	lower := strings.ToLower(nameOrID)
	var matches []int
	for i := range transitions {
		if strings.ToLower(transitions[i].Name) == lower {
			matches = append(matches, i)
		}
	}
	if len(matches) == 1 {
		return &transitions[matches[0]]
	}
	return nil
}

// Transition is the Layer 1 implementation for the transition write command.
func Transition(ctx context.Context, flags TransitionFlags, key, nameOrID string) (string, error) {
	entry, err := resolveEntry(flags.Profile)
	if err != nil {
		return "", err
	}
	client := jira.New(entry)

	// Parse --field tokens up-front so we fail fast on bad syntax.
	fieldsBlock, err := parseTransitionFields(flags.Fields)
	if err != nil {
		return "", err
	}

	// Fetch current status for display.
	raw, err := client.GetIssue(ctx, key, "status", false)
	if err != nil {
		return "", err
	}
	currentStatus := raw.Fields.Status.Name

	// Fetch available transitions (with field schema via expand=transitions.fields).
	transitions, err := client.GetTransitions(ctx, key)
	if err != nil {
		return "", fmt.Errorf("fetch transitions for %s: %w", key, err)
	}

	t := resolveTransition(transitions, nameOrID)
	if t == nil {
		var lines []string
		lines = append(lines, fmt.Sprintf("no transition %q on %s (current: %s)", nameOrID, key, currentStatus))
		lines = append(lines, "Available transitions:")
		for _, tr := range transitions {
			lines = append(lines, fmt.Sprintf("  %-6s  %s", tr.ID, tr.Name))
		}
		lines = append(lines, fmt.Sprintf("Run: jiracli edit status %s <id>", key))
		return "", fmt.Errorf("%s", strings.Join(lines, "\n"))
	}

	targetStatus := t.To.Name

	// Build the POST body for preview and execution.
	postBody := map[string]any{
		"transition": map[string]string{"id": t.ID},
	}
	if len(fieldsBlock) > 0 {
		postBody["fields"] = fieldsBlock
	}
	if flags.Comment != "" {
		postBody["update"] = map[string]any{
			"comment": []map[string]any{
				{"add": map[string]string{"body": flags.Comment}},
			},
		}
	}

	// Build validation rows.
	validation := []jira.ValidationRow{
		{Status: "✓", Message: fmt.Sprintf("transition %s %q is available on %s", t.ID, t.Name, key)},
		{Status: "✓", Message: fmt.Sprintf("%s → %s", currentStatus, targetStatus)},
	}
	for fieldID := range fieldsBlock {
		meta, hasMeta := t.Fields[fieldID]
		label := fieldID
		if hasMeta && meta.Name != "" {
			label = meta.Name
		}
		validation = append(validation, jira.ValidationRow{
			Status:  "✓",
			Message: fmt.Sprintf("field: %s = %v", label, fieldsBlock[fieldID]),
		})
	}

	// Warn about required fields not yet provided (pre-flight, before sending).
	missing := t.MissingRequiredFields(fieldsBlock)
	sort.Strings(missing)
	for _, id := range missing {
		meta := t.Fields[id]
		label := id
		if meta.Name != "" {
			label = meta.Name
		}
		names := meta.AllowedNames()
		msg := fmt.Sprintf("required field %q (%s) not provided", id, label)
		if len(names) > 0 {
			msg += fmt.Sprintf(" — allowed: %s", strings.Join(names, ", "))
		}
		validation = append(validation, jira.ValidationRow{Status: "⚠", Message: msg})
	}

	p := jira.Preview{
		Method:      "POST",
		Path:        "/issue/" + key + "/transitions",
		Body:        postBody,
		Description: fmt.Sprintf("%s: %s → %s", key, currentStatus, targetStatus),
		Validation:  validation,
	}

	// Inline preview → confirm → execute lifecycle so we can intercept the
	// *jira.TransitionError and surface corrective field suggestions.
	preview := p.Render(entry.URL)

	doApply := flags.Yes
	if !doApply {
		apply, ttyAvailable := promptApply()
		if !ttyAvailable {
			return preview + "\nApply with: re-run with --yes\n", nil
		}
		if !apply {
			return preview + "\naborted\n", nil
		}
		doApply = true
	}

	execErr := client.DoTransitionWithFields(ctx, key, t.ID, fieldsBlock, flags.Comment)
	if execErr != nil {
		var te *jira.TransitionError
		if errors.As(execErr, &te) {
			msg := buildTransitionFieldsError(te, *t, key, fieldsBlock)
			return "", fmt.Errorf("%s", msg)
		}
		return "", fmt.Errorf("apply failed: %w", execErr)
	}

	out := fmt.Sprintf("✓ transitioned %s: %s → %s\n  → jiracli show %s\n", key, currentStatus, targetStatus, key)
	if flags.Yes {
		return out, nil
	}
	return preview + "\n" + out, nil
}

// TransitionBulk moves multiple issues through the same transition.
//
// Dry-run: resolves the transition for every key (sequential API calls), prints
// a combined preview table, then either prompts once (TTY) or exits with
// "Apply with: --yes". On --yes: executes sequentially, collects per-key
// errors, and reports them all at the end rather than aborting on first failure.
func TransitionBulk(ctx context.Context, flags TransitionFlags, keys []string, nameOrID string) (string, error) {
	entry, err := resolveEntry(flags.Profile)
	if err != nil {
		return "", err
	}
	client := jira.New(entry)

	// Parse --field tokens up-front.
	fieldsBlock, err := parseTransitionFields(flags.Fields)
	if err != nil {
		return "", err
	}

	// Phase 1: resolve transition for every key.
	type resolved struct {
		key           string
		currentStatus string
		targetStatus  string
		transitionID  string
		transition    jira.TransitionRaw
	}
	var rows []resolved
	var lookupErrs []string
	for _, key := range keys {
		raw, err := client.GetIssue(ctx, key, "status", false)
		if err != nil {
			lookupErrs = append(lookupErrs, fmt.Sprintf("  %s: fetch failed: %v", key, err))
			continue
		}
		currentStatus := raw.Fields.Status.Name
		transitions, err := client.GetTransitions(ctx, key)
		if err != nil {
			lookupErrs = append(lookupErrs, fmt.Sprintf("  %s: fetch transitions failed: %v", key, err))
			continue
		}
		t := resolveTransition(transitions, nameOrID)
		if t == nil {
			lookupErrs = append(lookupErrs, fmt.Sprintf("  %s: no transition %q available (current: %s)", key, nameOrID, currentStatus))
			continue
		}
		rows = append(rows, resolved{
			key:           key,
			currentStatus: currentStatus,
			targetStatus:  t.To.Name,
			transitionID:  t.ID,
			transition:    *t,
		})
	}
	if len(lookupErrs) > 0 && len(rows) == 0 {
		return "", fmt.Errorf("all keys failed resolution:\n%s", strings.Join(lookupErrs, "\n"))
	}

	// Phase 2: build preview.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("transition %d issue(s) → %s\n\n", len(rows), nameOrID))
	for _, r := range rows {
		sb.WriteString(fmt.Sprintf("  %-16s  %s → %s\n", r.key, r.currentStatus, r.targetStatus))
	}
	if len(fieldsBlock) > 0 {
		sb.WriteString("\nFields:\n")
		fieldIDs := make([]string, 0, len(fieldsBlock))
		for id := range fieldsBlock {
			fieldIDs = append(fieldIDs, id)
		}
		sort.Strings(fieldIDs)
		for _, id := range fieldIDs {
			sb.WriteString(fmt.Sprintf("  %s = %v\n", id, fieldsBlock[id]))
		}
	}
	if len(lookupErrs) > 0 {
		sb.WriteString("\nSkipped (resolution failed):\n")
		for _, e := range lookupErrs {
			sb.WriteString(e + "\n")
		}
	}
	preview := sb.String()

	// Phase 3: apply or return preview.
	doApply := flags.Yes
	if !doApply {
		apply, ttyAvailable := promptApply()
		if !ttyAvailable {
			return preview + "\nApply with: re-run with --yes\n", nil
		}
		if !apply {
			return preview + "\naborted\n", nil
		}
		doApply = true
	}
	_ = doApply

	// Phase 4: execute sequentially, collect errors.
	var applyErrs []string
	var okKeys []string
	for _, r := range rows {
		execErr := client.DoTransitionWithFields(ctx, r.key, r.transitionID, fieldsBlock, flags.Comment)
		if execErr != nil {
			var te *jira.TransitionError
			if errors.As(execErr, &te) {
				msg := buildTransitionFieldsError(te, r.transition, r.key, fieldsBlock)
				applyErrs = append(applyErrs, "  "+strings.ReplaceAll(msg, "\n", "\n  "))
			} else {
				applyErrs = append(applyErrs, fmt.Sprintf("  %s: %v", r.key, execErr))
			}
		} else {
			okKeys = append(okKeys, r.key)
		}
	}

	var out strings.Builder
	out.WriteString(preview + "\n")
	for _, key := range okKeys {
		out.WriteString(fmt.Sprintf("✓ transitioned %s\n", key))
	}
	if len(applyErrs) > 0 {
		out.WriteString("\nFailed:\n")
		for _, e := range applyErrs {
			out.WriteString(e + "\n")
		}
		return out.String(), fmt.Errorf("%d of %d transitions failed", len(applyErrs), len(rows))
	}
	return out.String(), nil
}
