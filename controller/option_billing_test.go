package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateBillingExprOption(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{
			name:  "time pricing",
			value: `{"model-a":"(hour(\"Asia/Shanghai\") * 60 + minute(\"Asia/Shanghai\")) >= 600 && (hour(\"Asia/Shanghai\") * 60 + minute(\"Asia/Shanghai\")) < 720 ? tier(\"time_1000_1200\", p * 1 + c * 2) : tier(\"default\", p * 3 + c * 4)"}`,
		},
		{
			name:    "invalid json",
			value:   `{`,
			wantErr: true,
		},
		{
			name:    "invalid expression",
			value:   `{"model-a":"p *"}`,
			wantErr: true,
		},
		{
			name:    "negative result",
			value:   `{"model-a":"tier(\"bad\", p * -1)"}`,
			wantErr: true,
		},
		{
			name:    "negative result hidden in time window",
			value:   `{"model-a":"(hour(\"Asia/Shanghai\") * 60 + minute(\"Asia/Shanghai\")) >= 600 && (hour(\"Asia/Shanghai\") * 60 + minute(\"Asia/Shanghai\")) < 601 ? tier(\"bad\", p * -1) : tier(\"default\", p * 1)"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBillingExprOption(tt.value)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}
