package middleware

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func reasoningTestBody(path, value string) string {
	field := `"reasoning_effort":` + value
	switch path {
	case "/v1/responses":
		field = `"reasoning":{"effort":` + value + `}`
	case "/v1/messages":
		field = `"output_config":{"effort":` + value + `}`
	}
	if value == "" {
		field = `"metadata":{}`
	}
	return fmt.Sprintf(`{"model":%q,"messages":[],%s}`, distributorEndpointTestModel, field)
}

func TestDistributeReasoningEffortSettings(t *testing.T) {
	db := setupDistributorEndpointTestDB(t)
	highSetting := `{"reasoning_effort_filter":{"allowed_values":["high"],"allow_missing":true}}`
	futureSetting := `{"reasoning_effort_filter":{"allowed_values":["max","future-v3"],"allow_missing":false}}`
	for _, path := range []string{"/v1/chat/completions", "/pg/chat/completions", "/v1/responses", "/v1/messages"} {
		t.Run(path, func(t *testing.T) {
			resetDistributorEndpointTestChannels(t, db)
			high := addDistributorEndpointTestChannel(t, db, highSetting, 100)
			future := addDistributorEndpointTestChannel(t, db, futureSetting, 50)
			for _, test := range []struct {
				value string
				want  int
			}{
				{"", high.Id}, {`"high"`, high.Id}, {`"max"`, future.Id}, {`"future-v3"`, future.Id},
				{`"none"`, 0}, {`"low"`, 0}, {`null`, 0}, {`""`, 0}, {`7`, 0},
			} {
				response, selected := requestDistributorEndpoint(path, 0, reasoningTestBody(path, test.value))
				if test.want == 0 {
					requireDistributorEndpointUnavailable(t, response, selected)
				} else {
					require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
					require.Equal(t, test.want, selected, test.value)
				}
			}
			response, selected := requestDistributorEndpoint(path, high.Id, reasoningTestBody(path, `"max"`))
			requireDistributorEndpointUnavailable(t, response, selected)
			response, selected = requestDistributorEndpoint(path, future.Id, reasoningTestBody(path, `"max"`))
			require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
			require.Equal(t, future.Id, selected)

			legacy := addDistributorEndpointTestChannel(t, db, "", 200)
			response, selected = requestDistributorEndpoint(path, 0, reasoningTestBody(path, `"brand-new-level"`))
			require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
			require.Equal(t, legacy.Id, selected, "unconfigured channels remain unrestricted")
		})
	}
}

func TestDistributeReasoningEffortRechecksAffinity(t *testing.T) {
	db := setupDistributorEndpointTestDB(t)
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
		t.Run(path, func(t *testing.T) {
			resetDistributorEndpointTestChannels(t, db)
			ruleName := strings.ReplaceAll(t.Name(), "/", "_")
			affinity := operation_setting.GetChannelAffinitySetting()
			previous := *affinity
			*affinity = operation_setting.ChannelAffinitySetting{
				Enabled: true, SwitchOnSuccess: true, MaxEntries: 100, DefaultTTLSeconds: 60,
				Rules: []operation_setting.ChannelAffinityRule{{
					Name: ruleName, ModelRegex: []string{"^" + distributorEndpointTestModel + "$"},
					KeySources:         []operation_setting.ChannelAffinityKeySource{{Type: "context_string", Key: "endpoint_test_affinity"}},
					SkipRetryOnFailure: true, IncludeUsingGroup: true, IncludeModelName: true, IncludeRuleName: true,
				}},
			}
			t.Cleanup(func() {
				_, err := service.ClearChannelAffinityCacheByRuleName(ruleName)
				*affinity = previous
				require.NoError(t, err)
			})
			high := addDistributorEndpointTestChannel(t, db, `{"reasoning_effort_filter":{"allowed_values":["high"]}}`, 100)
			low := addDistributorEndpointTestChannel(t, db, `{"reasoning_effort_filter":{"allowed_values":["low"]}}`, 50)
			response, selected := requestDistributorEndpoint(path, 0, reasoningTestBody(path, `"high"`))
			require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
			require.Equal(t, high.Id, selected)
			response, selected = requestDistributorEndpoint(path, 0, reasoningTestBody(path, `"low"`))
			require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
			require.Equal(t, low.Id, selected, "an affinity hit cannot override the new request's effort")
		})
	}
}
