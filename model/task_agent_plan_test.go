package model

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func TestInitTaskPersistsSelectedAgentPlanMultiKey(t *testing.T) {
	tests := []struct {
		name       string
		isMultiKey bool
		wantKey    string
	}{
		{name: "multi key", isMultiKey: true, wantKey: "selected-key"},
		{name: "single key", isMultiKey: false, wantKey: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeVolcEngineAgentPlan,
				ChannelIsMultiKey: tt.isMultiKey,
				ApiKey:            "selected-key",
			}}
			task := InitTask(constant.TaskPlatform("60"), info)
			if task.PrivateData.Key != tt.wantKey {
				t.Fatalf("private key = %q, want %q", task.PrivateData.Key, tt.wantKey)
			}
		})
	}
}
