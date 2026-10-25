export const normalizeReasoningEfforts = (values) => [
  ...new Set(
    (Array.isArray(values) ? values : [])
      .filter((value) => typeof value === 'string')
      .map((value) => value.trim())
      .filter(Boolean),
  ),
];

export const readReasoningEffortFilter = (filter) => ({
  reasoning_effort_filter_enabled:
    filter !== null && typeof filter === 'object' && !Array.isArray(filter),
  reasoning_effort_allowed_values: normalizeReasoningEfforts(
    filter?.allowed_values,
  ),
  reasoning_effort_allow_missing: filter?.allow_missing === true,
});

export const buildReasoningEffortFilter = (values) =>
  values.reasoning_effort_filter_enabled === true
    ? {
        allowed_values: normalizeReasoningEfforts(
          values.reasoning_effort_allowed_values,
        ),
        allow_missing: values.reasoning_effort_allow_missing === true,
      }
    : undefined;
