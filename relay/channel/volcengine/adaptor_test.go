package volcengine

import (
	"slices"
	"testing"

	channelconstant "github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
)

func TestGetRequestURL(t *testing.T) {
	tests := []struct {
		name        string
		channelType int
		baseURL     string
		relayMode   int
		want        string
	}{
		{
			name:        "regular chat remains on api v3",
			channelType: channelconstant.ChannelTypeVolcEngine,
			baseURL:     "https://ark.cn-beijing.volces.com",
			relayMode:   constant.RelayModeChatCompletions,
			want:        "https://ark.cn-beijing.volces.com/api/v3/chat/completions",
		},
		{
			name:        "agent plan chat",
			channelType: channelconstant.ChannelTypeVolcEngineAgentPlan,
			baseURL:     "https://ark.cn-beijing.volces.com",
			relayMode:   constant.RelayModeChatCompletions,
			want:        "https://ark.cn-beijing.volces.com/api/plan/v3/chat/completions",
		},
		{
			name:        "agent plan embeddings",
			channelType: channelconstant.ChannelTypeVolcEngineAgentPlan,
			baseURL:     "https://ark.cn-beijing.volces.com/",
			relayMode:   constant.RelayModeEmbeddings,
			want:        "https://ark.cn-beijing.volces.com/api/plan/v3/embeddings",
		},
		{
			name:        "agent plan responses with prefixed base",
			channelType: channelconstant.ChannelTypeVolcEngineAgentPlan,
			baseURL:     "https://ark.cn-beijing.volces.com/api/plan/v3",
			relayMode:   constant.RelayModeResponses,
			want:        "https://ark.cn-beijing.volces.com/api/plan/v3/responses",
		},
		{
			name:        "agent plan image generation",
			channelType: channelconstant.ChannelTypeVolcEngineAgentPlan,
			baseURL:     "https://ark.cn-beijing.volces.com",
			relayMode:   constant.RelayModeImagesGenerations,
			want:        "https://ark.cn-beijing.volces.com/api/plan/v3/images/generations",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				RelayMode:   tt.relayMode,
				RelayFormat: types.RelayFormatOpenAI,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelType:    tt.channelType,
					ChannelBaseUrl: tt.baseURL,
				},
			}
			adaptor := &Adaptor{}
			adaptor.Init(info)
			got, err := adaptor.GetRequestURL(info)
			if err != nil {
				t.Fatalf("GetRequestURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("GetRequestURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAgentPlanModelList(t *testing.T) {
	adaptor := &Adaptor{}
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelType: channelconstant.ChannelTypeVolcEngineAgentPlan,
	}})

	for _, model := range []string{
		"doubao-seed-2.0-mini",
		"doubao-embedding-vision",
		"doubao-seedance-1.5-pro",
		"doubao-seedance-2.0-mini",
	} {
		if !slices.Contains(adaptor.GetModelList(), model) {
			t.Errorf("Agent Plan model list does not contain %q", model)
		}
	}
	if adaptor.GetChannelName() != AgentPlanChannelName {
		t.Errorf("GetChannelName() = %q, want %q", adaptor.GetChannelName(), AgentPlanChannelName)
	}
}
