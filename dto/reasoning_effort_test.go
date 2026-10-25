package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestReasoningEffortFilterDefaultsAndExactMatching(t *testing.T) {
	for _, raw := range []string{`{}`, `{"reasoning_effort_filter":null}`} {
		var settings ChannelSettings
		require.NoError(t, common.UnmarshalJsonStr(raw, &settings))
		require.True(t, settings.ReasoningEffortFilter.Matches(nil))
		require.True(t, settings.ReasoningEffortFilter.Matches(&RequestReasoningEffort{Present: true, Value: "future-effort"}))
	}
	filter := &ReasoningEffortFilter{AllowedValues: []string{"high", "max", "future-effort"}, AllowMissing: true}
	require.NoError(t, filter.Validate())
	for _, value := range []string{"high", "max", "future-effort"} {
		require.True(t, filter.Matches(&RequestReasoningEffort{Present: true, Value: value}), value)
	}
	for _, value := range []string{"none", "low", "HIGH", " high", ""} {
		require.False(t, filter.Matches(&RequestReasoningEffort{Present: true, Value: value}), value)
	}
	require.True(t, filter.Matches(&RequestReasoningEffort{}))
	require.False(t, filter.Matches(nil), "missing metadata cannot bypass the rule")
	filter.AllowMissing = false
	require.False(t, filter.Matches(&RequestReasoningEffort{}))

	missingOnly := &ReasoningEffortFilter{AllowedValues: []string{}, AllowMissing: true}
	require.NoError(t, missingOnly.Validate())
	require.True(t, missingOnly.Matches(&RequestReasoningEffort{}))
	require.False(t, missingOnly.Matches(&RequestReasoningEffort{Present: true, Value: "none"}))
	encoded, err := common.Marshal(missingOnly)
	require.NoError(t, err)
	require.JSONEq(t, `{"allowed_values":[],"allow_missing":true}`, string(encoded))
}

func TestReasoningEffortFilterRejectsInvalidConfiguration(t *testing.T) {
	for _, filter := range []*ReasoningEffortFilter{
		{}, {AllowedValues: []string{""}}, {AllowedValues: []string{" "}},
		{AllowedValues: []string{" high"}}, {AllowedValues: []string{"high", "high"}},
	} {
		require.Error(t, filter.Validate(), "%+v", filter)
	}
}
