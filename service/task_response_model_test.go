package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestTaskBillingOtherRestoreResponseModel(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		task := &model.Task{
			Properties: model.Properties{
				OriginModelName:   "client-A",
				UpstreamModelName: "upstream-B",
			},
			PrivateData: model.TaskPrivateData{
				RestoreResponseModel: enabled,
				BillingContext: &model.TaskBillingContext{
					ModelPrice:      0.02,
					ModelRatio:      2,
					GroupRatio:      1,
					OriginModelName: "client-A",
					OtherRatios:     map[string]float64{"duration": 5},
				},
			},
		}
		other := taskBillingOther(task)
		if enabled {
			require.NotContains(t, other, "is_model_mapped")
			require.NotContains(t, other, "upstream_model_name")
		} else {
			require.Equal(t, true, other["is_model_mapped"])
			require.Equal(t, "upstream-B", other["upstream_model_name"])
		}
		require.Equal(t, 0.02, other["model_price"])
		require.Equal(t, float64(2), other["model_ratio"])
		require.Equal(t, float64(1), other["group_ratio"])
		require.Equal(t, float64(5), other["duration"])
		require.Equal(t, "client-A", taskModelName(task))
		require.Equal(t, "upstream-B", task.Properties.UpstreamModelName)
	}
}
