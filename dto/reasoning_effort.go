package dto

import (
	"fmt"
	"strings"
)

// RequestReasoningEffort keeps the original client parameter separate from
// provider defaults, parameter overrides, and model-name suffix conversion.
// Present with an empty Value represents an invalid/null/empty parameter; it
// must never match the separate "missing" option.
type RequestReasoningEffort struct {
	Present bool
	Value   string
}

type ReasoningEffortFilter struct {
	AllowedValues []string `json:"allowed_values"`
	AllowMissing  bool     `json:"allow_missing"`
}

func (filter *ReasoningEffortFilter) Validate() error {
	if filter == nil {
		return nil
	}
	if len(filter.AllowedValues) == 0 && !filter.AllowMissing {
		return fmt.Errorf("reasoning_effort_filter must allow at least one value or missing parameters")
	}
	seen := make(map[string]struct{}, len(filter.AllowedValues))
	for _, value := range filter.AllowedValues {
		if value == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("reasoning_effort_filter values must be non-empty strings without surrounding whitespace")
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("reasoning_effort_filter contains duplicate value %q", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func (filter *ReasoningEffortFilter) Matches(effort *RequestReasoningEffort) bool {
	if filter == nil {
		return true
	}
	if effort == nil {
		return false
	}
	if !effort.Present {
		return filter.AllowMissing
	}
	if effort.Value == "" {
		return false
	}
	for _, allowed := range filter.AllowedValues {
		if effort.Value == allowed {
			return true
		}
	}
	return false
}
