package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEndpointSelectionFiltersCacheAndDatabaseCandidates(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))

	oldDB, oldMemoryCacheEnabled, oldGroupCol := DB, common.MemoryCacheEnabled, commonGroupCol
	oldGroups, oldChannels := group2model2channels, channelsIDM
	t.Cleanup(func() {
		DB, common.MemoryCacheEnabled, commonGroupCol = oldDB, oldMemoryCacheEnabled, oldGroupCol
		group2model2channels, channelsIDM = oldGroups, oldChannels
	})
	DB = db
	commonGroupCol = "`group`"

	disabled := `{"responses_enabled":false,"messages_enabled":false,"chat_completions_enabled":false}`
	channels := []Channel{
		{Id: 1, Key: "test-key", Group: "default", Models: "endpoint-model,single-model", Status: common.ChannelStatusEnabled, Priority: int64Ptr(100), Weight: uintPtr(1000), Setting: &disabled},
		{Id: 2, Key: "test-key", Group: "default", Models: "endpoint-model", Status: common.ChannelStatusEnabled, Priority: int64Ptr(50), Weight: uintPtr(1), MaxOutputTokens: intPtr(100)},
	}
	require.NoError(t, db.Create(&channels).Error)
	for i := range channels {
		require.NoError(t, channels[i].AddAbilities(db))
	}

	for _, useCache := range []bool{true, false} {
		name := "database"
		if useCache {
			name = "cache"
		}
		t.Run(name, func(t *testing.T) {
			common.MemoryCacheEnabled = useCache
			require.NoError(t, db.Model(&Channel{}).Where("id = ?", 2).Update("setting", nil).Error)
			InitChannelCache()
			for _, path := range []string{"/v1/responses", "/v1/messages", "/v1/chat/completions", "/pg/chat/completions"} {
				constraint := &ChannelSelectionConstraint{RequestPath: path}
				channel, err := GetRandomSatisfiedChannelWithSelectionConstraint("default", "endpoint-model", 0, constraint)
				require.NoError(t, err, path)
				require.NotNil(t, channel, path)
				require.Equal(t, 2, channel.Id, "skip a disabled endpoint even at a higher priority: %s", path)

				channel, err = GetRandomSatisfiedChannelWithSelectionConstraint("default", "single-model", 0, constraint)
				require.NoError(t, err, path)
				require.Nil(t, channel, "a lone channel cannot bypass endpoint settings: %s", path)

				constraint.TokenLimit = &ChannelTokenLimit{MaxTokens: 101}
				channel, err = GetRandomSatisfiedChannelWithSelectionConstraint("default", "endpoint-model", 0, constraint)
				require.NoError(t, err, path)
				require.Nil(t, channel, "both endpoint and token constraints must be satisfied: %s", path)
			}

			channel, err := GetRandomSatisfiedChannelWithSelectionConstraint("default", "endpoint-model", 0, &ChannelSelectionConstraint{RequestPath: "/v1/embeddings"})
			require.NoError(t, err)
			require.NotNil(t, channel)
			require.Equal(t, 1, channel.Id, "unrelated endpoints keep their highest priority channel")

			require.NoError(t, db.Model(&Channel{}).Where("id = ?", 2).Update("setting", disabled).Error)
			InitChannelCache()
			for _, path := range []string{"/v1/responses", "/v1/messages", "/v1/chat/completions", "/pg/chat/completions"} {
				channel, err := GetRandomSatisfiedChannelWithSelectionConstraint("default", "endpoint-model", 0, &ChannelSelectionConstraint{RequestPath: path})
				require.NoError(t, err, path)
				require.Nil(t, channel, "all channels have disabled the requested endpoint: %s", path)
			}
		})
	}
}
