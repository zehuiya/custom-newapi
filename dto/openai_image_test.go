package dto

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageRequestQwenImage2512PriceRatios(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request ImageRequest
		want    float64
	}{
		{
			name: "auto",
			request: ImageRequest{
				Model:   "qwen-image-2512",
				Quality: "auto",
				Size:    "auto",
			},
			want: 1,
		},
		{
			name: "low 512",
			request: ImageRequest{
				Model:   "qwen-image-2512",
				Quality: "low",
				Size:    "512x512",
			},
			want: 0.40625,
		},
		{
			name: "high 1024",
			request: ImageRequest{
				Model:   "qwen-image-2512",
				Quality: "high",
				Size:    "1024x1024",
			},
			want: 1.609375,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.request.GetTokenCountMeta().ImagePriceRatio)
		})
	}
}
