package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"
)

func TestApplyTaskOtherRatiosToFixedPrice(t *testing.T) {
	priceData := &types.PriceData{
		UsePrice:    true,
		Quota:       10000,
		OtherRatios: map[string]float64{"audio": 0.5},
	}

	applyTaskOtherRatios(priceData, "doubao-seedance-1.5-pro")

	if priceData.Quota != 5000 {
		t.Fatalf("fixed-price quota = %d, want 5000", priceData.Quota)
	}
}

func TestApplyTaskOtherRatiosToSeedanceMiniVideoInput(t *testing.T) {
	priceData := &types.PriceData{
		Quota:       23000,
		OtherRatios: map[string]float64{"video_input": 14.0 / 23.0},
	}

	applyTaskOtherRatios(priceData, "doubao-seedance-2-0-mini-260615")

	if priceData.Quota != 14000 {
		t.Fatalf("mini video-input quota = %d, want 14000", priceData.Quota)
	}
}

func TestApplyTaskOtherRatiosKeepsTaskPricePatchCompatibility(t *testing.T) {
	originalPatches := constant.TaskPricePatches
	constant.TaskPricePatches = []string{"doubao-seedance-1.5-pro"}
	t.Cleanup(func() {
		constant.TaskPricePatches = originalPatches
	})

	priceData := &types.PriceData{
		UsePrice:    true,
		Quota:       10000,
		OtherRatios: map[string]float64{"audio": 0.5},
	}

	applyTaskOtherRatios(priceData, "doubao-seedance-1.5-pro")

	if priceData.Quota != 10000 {
		t.Fatalf("TASK_PRICE_PATCH quota = %d, want 10000", priceData.Quota)
	}
}
