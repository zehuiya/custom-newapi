package billing_setting

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/config"
)

const (
	BillingModeRatio      = "ratio"
	BillingModeTieredExpr = "tiered_expr"
)

// BillingSetting is managed by config.GlobalConfig.Register.
// DB keys: billing_setting.billing_mode, billing_setting.billing_expr
type BillingSetting struct {
	BillingMode map[string]string `json:"billing_mode"`
	BillingExpr map[string]string `json:"billing_expr"`
}

var billingSetting = BillingSetting{
	BillingMode: make(map[string]string),
	BillingExpr: make(map[string]string),
}

func init() {
	config.GlobalConfig.Register("billing_setting", &billingSetting)
}

// ---------------------------------------------------------------------------
// Read accessors (hot path, must be fast)
// ---------------------------------------------------------------------------

func GetBillingMode(model string) string {
	if mode, ok := billingSetting.BillingMode[model]; ok {
		return mode
	}
	return BillingModeRatio
}

func GetBillingExpr(model string) (string, bool) {
	expr, ok := billingSetting.BillingExpr[model]
	return expr, ok
}

// ---------------------------------------------------------------------------
// Smoke test (called externally for validation before save)
// ---------------------------------------------------------------------------

func SmokeTestExpr(exprStr string) error {
	return smokeTestExpr(exprStr)
}

func smokeTestExpr(exprStr string) error {
	vectors := []billingexpr.TokenParams{
		{P: 0, C: 0},
		{P: 1000, C: 1000},
		{P: 100000, C: 100000},
		{P: 1000000, C: 1000000},
	}
	requests := []billingexpr.RequestInput{
		{},
		{
			Headers: map[string]string{
				"anthropic-beta": "fast-mode-2026-02-01",
			},
			Body: []byte(`{"service_tier":"fast","stream_options":{"include_usage":true},"messages":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21]}`),
		},
	}

	for _, v := range vectors {
		for _, request := range requests {
			if err := validateSmokeTestResult(exprStr, v, request); err != nil {
				return err
			}
		}
	}

	// A time-based expression may hide an invalid branch until that price window
	// becomes active. Validate every Beijing minute when time functions are used.
	if strings.Contains(exprStr, "hour(") || strings.Contains(exprStr, "minute(") {
		beijing := time.FixedZone("Asia/Shanghai", 8*60*60)
		dayStart := time.Date(2026, time.January, 1, 0, 0, 0, 0, beijing)
		allDimensions := billingexpr.TokenParams{
			P: 1000, C: 1000, CR: 100, CC: 100, CC1h: 100,
			Img: 100, ImgO: 100, AI: 100, AO: 100,
		}
		for minuteOfDay := 0; minuteOfDay < 24*60; minuteOfDay++ {
			request := requestInputAt(dayStart.Add(time.Duration(minuteOfDay) * time.Minute))
			if err := validateSmokeTestResult(exprStr, allDimensions, request); err != nil {
				return fmt.Errorf("Beijing time %02d:%02d: %w", minuteOfDay/60, minuteOfDay%60, err)
			}
		}
	}
	return nil
}

func requestInputAt(at time.Time) billingexpr.RequestInput {
	return billingexpr.RequestInput{EvaluationTimeUnixMilli: at.UnixMilli()}
}

func validateSmokeTestResult(exprStr string, params billingexpr.TokenParams, request billingexpr.RequestInput) error {
	result, _, err := billingexpr.RunExprWithRequest(exprStr, params, request)
	if err != nil {
		return fmt.Errorf("vector {p=%g, c=%g}: run failed: %w", params.P, params.C, err)
	}
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return fmt.Errorf("vector {p=%g, c=%g}: result %f is not finite", params.P, params.C, result)
	}
	if result < 0 {
		return fmt.Errorf("vector {p=%g, c=%g}: result %f < 0", params.P, params.C, result)
	}
	return nil
}
