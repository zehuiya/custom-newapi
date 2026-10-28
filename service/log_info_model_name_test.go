package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGenerateTextOtherInfoResponseModelDisplay(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "default keeps mapping"
		if enabled {
			name = "restore hides mapping"
		}
		t.Run(name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			info := &relaycommon.RelayInfo{
				OriginModelName: "client-A",
				StartTime:       time.Now(), FirstResponseTime: time.Now(),
				ChannelMeta: &relaycommon.ChannelMeta{
					IsModelMapped: true, UpstreamModelName: "upstream-C",
					ChannelSetting: dto.ChannelSettings{RestoreResponseModel: enabled},
				},
			}
			other := GenerateTextOtherInfo(c, info, 1, 1, 1, 0, 0, -1, -1)
			if enabled {
				require.NotContains(t, other, "is_model_mapped")
				require.NotContains(t, other, "upstream_model_name")
			} else {
				require.Equal(t, true, other["is_model_mapped"])
				require.Equal(t, "upstream-C", other["upstream_model_name"])
			}
			require.True(t, info.IsModelMapped, "display option must retain the routing mapping")
			require.Equal(t, "upstream-C", info.UpstreamModelName)
			require.Equal(t, "client-A", info.OriginModelName)
			require.Equal(t, "/v1/messages", other["request_path"])
		})
	}
}
