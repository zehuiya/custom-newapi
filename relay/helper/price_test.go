package helper

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelPriceHelperTieredUsesPreloadedRequestInput(t *testing.T) {
	gin.SetMode(gin.TestMode)

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"tiered-test-model":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"tiered-test-model":"param(\"stream\") == true ? tier(\"stream\", p * 3) : tier(\"base\", p * 2)"}`,
	}))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/channel/test/1", nil)
	req.Body = nil
	req.ContentLength = 0
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req
	ctx.Set("group", "default")

	info := &relaycommon.RelayInfo{
		OriginModelName: "tiered-test-model",
		UserGroup:       "default",
		UsingGroup:      "default",
		StartTime:       time.UnixMilli(1_786_803_600_000),
		RequestHeaders:  map[string]string{"Content-Type": "application/json"},
		BillingRequestInput: &billingexpr.RequestInput{
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    []byte(`{"stream":true}`),
		},
	}

	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.Equal(t, 1500, priceData.QuotaToPreConsume)
	require.NotNil(t, info.TieredBillingSnapshot)
	require.Equal(t, "stream", info.TieredBillingSnapshot.EstimatedTier)
	require.Equal(t, billing_setting.BillingModeTieredExpr, info.TieredBillingSnapshot.BillingMode)
	require.Equal(t, common.QuotaPerUnit, info.TieredBillingSnapshot.QuotaPerUnit)
	require.Equal(t, info.StartTime.UnixMilli(), info.TieredBillingSnapshot.BillingTimeUnixMilli)
}

func TestModelPriceHelperTieredAlwaysFreezesBillingTime(t *testing.T) {
	gin.SetMode(gin.TestMode)

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"tiered-time-fallback":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"tiered-time-fallback":"tier(\"base\", p * 1)"}`,
	}))

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("group", "default")
	info := &relaycommon.RelayInfo{
		OriginModelName: "tiered-time-fallback",
		UserGroup:       "default",
		UsingGroup:      "default",
	}

	before := time.Now().UnixMilli()
	_, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	after := time.Now().UnixMilli()
	require.NoError(t, err)
	require.NotNil(t, info.TieredBillingSnapshot)
	require.GreaterOrEqual(t, info.TieredBillingSnapshot.BillingTimeUnixMilli, before)
	require.LessOrEqual(t, info.TieredBillingSnapshot.BillingTimeUnixMilli, after)
}

func TestModelPriceHelperKeepsConfiguredImageBasePriceAndAppliesRequestRatio(t *testing.T) {
	gin.SetMode(gin.TestMode)

	originalModelPrice := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(originalModelPrice))
	})
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"image-price-ratio-test":0.0064}`))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("group", "default")

	info := &relaycommon.RelayInfo{
		OriginModelName: "image-price-ratio-test",
		UserGroup:       "default",
		UsingGroup:      "default",
	}

	priceData, err := ModelPriceHelper(ctx, info, 1, &types.TokenCountMeta{ImagePriceRatio: 1.609375})
	require.NoError(t, err)
	require.True(t, priceData.UsePrice)
	require.Equal(t, 0.0064, priceData.ModelPrice)
	require.Equal(t, 1.609375, priceData.OtherRatios[types.OtherRatioImagePrice])
	require.Equal(t, int(0.0064*1.609375*common.QuotaPerUnit), priceData.QuotaToPreConsume)
}

func TestModelPriceHelperDoesNotPersistNeutralImageRatio(t *testing.T) {
	gin.SetMode(gin.TestMode)

	originalModelPrice := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(originalModelPrice))
	})
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"flat-image-price-test":0.25}`))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("group", "default")

	info := &relaycommon.RelayInfo{
		OriginModelName: "flat-image-price-test",
		UserGroup:       "default",
		UsingGroup:      "default",
	}

	priceData, err := ModelPriceHelper(ctx, info, 1, &types.TokenCountMeta{ImagePriceRatio: 1})
	require.NoError(t, err)
	require.True(t, priceData.UsePrice)
	require.Equal(t, 0.25, priceData.ModelPrice)
	require.NotContains(t, priceData.OtherRatios, types.OtherRatioImagePrice)
	require.Equal(t, int(0.25*common.QuotaPerUnit), priceData.QuotaToPreConsume)
}
