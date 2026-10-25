package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReasoningEffortSelectionWithCacheAndDatabase(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
	oldDB, oldCache, oldGroupCol := DB, common.MemoryCacheEnabled, commonGroupCol
	oldGroups, oldChannels := group2model2channels, channelsIDM
	t.Cleanup(func() {
		DB, common.MemoryCacheEnabled, commonGroupCol = oldDB, oldCache, oldGroupCol
		group2model2channels, channelsIDM = oldGroups, oldChannels
	})
	DB, commonGroupCol = db, "`group`"
	high := `{"reasoning_effort_filter":{"allowed_values":["high"],"allow_missing":true}}`
	max := `{"reasoning_effort_filter":{"allowed_values":["max","future-v3"],"allow_missing":false}}`
	channels := []Channel{
		{Id: 1, Key: "test", Group: "default", Models: "effort-model,single-model", Status: common.ChannelStatusEnabled, Priority: int64Ptr(100), Setting: &high},
		{Id: 2, Key: "test", Group: "default", Models: "effort-model", Status: common.ChannelStatusEnabled, Priority: int64Ptr(50), Setting: &max},
	}
	require.NoError(t, db.Create(&channels).Error)
	for i := range channels {
		require.NoError(t, channels[i].AddAbilities(db))
	}
	for _, useCache := range []bool{false, true} {
		common.MemoryCacheEnabled = useCache
		InitChannelCache()
		for _, path := range []string{"/v1/chat/completions", "/pg/chat/completions", "/v1/responses", "/v1/messages"} {
			for _, test := range []struct {
				effort dto.RequestReasoningEffort
				want   int
			}{
				{dto.RequestReasoningEffort{}, 1},
				{dto.RequestReasoningEffort{Present: true, Value: "high"}, 1},
				{dto.RequestReasoningEffort{Present: true, Value: "max"}, 2},
				{dto.RequestReasoningEffort{Present: true, Value: "future-v3"}, 2},
				{dto.RequestReasoningEffort{Present: true, Value: "none"}, 0},
				{dto.RequestReasoningEffort{Present: true}, 0},
			} {
				constraint := &ChannelSelectionConstraint{RequestPath: path, ReasoningEffort: &test.effort}
				channel, err := GetRandomSatisfiedChannelWithSelectionConstraint("default", "effort-model", 0, constraint)
				require.NoError(t, err)
				if test.want == 0 {
					require.Nil(t, channel)
				} else {
					require.NotNil(t, channel)
					require.Equal(t, test.want, channel.Id, "cache=%v, %s, %+v", useCache, path, test.effort)
				}
			}
			channel, err := GetRandomSatisfiedChannelWithSelectionConstraint("default", "single-model", 0, &ChannelSelectionConstraint{RequestPath: path, ReasoningEffort: &dto.RequestReasoningEffort{Present: true, Value: "max"}})
			require.NoError(t, err)
			require.Nil(t, channel, "single candidate must also match")
		}
	}
}

func TestValidateSettingsRejectsInvalidReasoningEffortFilter(t *testing.T) {
	for _, raw := range []string{
		`{"reasoning_effort_filter":{}}`,
		`{"reasoning_effort_filter":{"allowed_values":["high","high"]}}`,
		`{"reasoning_effort_filter":{"allowed_values":[""]}}`,
		`{"reasoning_effort_filter":{"allowed_values":[" high"]}}`,
	} {
		channel := Channel{Setting: &raw}
		require.ErrorContains(t, channel.ValidateSettings(), "reasoning_effort_filter")
	}
	for _, raw := range []string{
		`{}`, `{"reasoning_effort_filter":null}`,
		`{"reasoning_effort_filter":{"allowed_values":[],"allow_missing":true}}`,
		`{"reasoning_effort_filter":{"allowed_values":["high","future-v3"]}}`,
	} {
		channel := Channel{Setting: &raw}
		require.NoError(t, channel.ValidateSettings())
	}
}
