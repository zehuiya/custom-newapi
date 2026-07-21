package common

import (
	"reflect"
	"testing"

	"github.com/QuantumNous/new-api/constant"
)

func TestAgentPlanUsesVolcEngineAPIType(t *testing.T) {
	apiType, ok := ChannelType2APIType(constant.ChannelTypeVolcEngineAgentPlan)
	if !ok {
		t.Fatal("Agent Plan channel type was not mapped")
	}
	if apiType != constant.APITypeVolcEngine {
		t.Fatalf("api type = %d, want %d", apiType, constant.APITypeVolcEngine)
	}
}

func TestAgentPlanEndpointTypes(t *testing.T) {
	tests := []struct {
		model string
		want  []constant.EndpointType
	}{
		{"doubao-seed-2.0-mini", []constant.EndpointType{constant.EndpointTypeOpenAI, constant.EndpointTypeOpenAIResponse}},
		{"doubao-embedding-vision", []constant.EndpointType{constant.EndpointTypeEmbeddings}},
		{"doubao-seedream-5.0-lite", []constant.EndpointType{constant.EndpointTypeImageGeneration}},
		{"doubao-seedance-1.5-pro", []constant.EndpointType{constant.EndpointTypeOpenAIVideo}},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := GetEndpointTypesByChannelType(constant.ChannelTypeVolcEngineAgentPlan, tt.model)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("endpoint types = %v, want %v", got, tt.want)
			}
		})
	}
}
