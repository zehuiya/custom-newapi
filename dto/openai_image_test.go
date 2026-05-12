package dto

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageRequestQwenImage2512UsesFlatPrice(t *testing.T) {
	t.Parallel()

	request := ImageRequest{
		Model:   "qwen-image-2512",
		Quality: "high",
		Size:    "512x512",
	}

	require.Equal(t, 0.0, request.GetTokenCountMeta().ImagePriceRatio)
}

func TestImageRequestDallePriceRatios(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request ImageRequest
		want    float64
	}{
		{
			name: "dall-e-2 512",
			request: ImageRequest{
				Model: "dall-e-2",
				Size:  "512x512",
			},
			want: 0.45,
		},
		{
			name: "dall-e-3 hd square",
			request: ImageRequest{
				Model:   "dall-e-3",
				Quality: "hd",
				Size:    "1024x1024",
			},
			want: 2,
		},
		{
			name: "dall-e-3 hd portrait",
			request: ImageRequest{
				Model:   "dall-e-3",
				Quality: "hd",
				Size:    "1024x1792",
			},
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.request.GetTokenCountMeta().ImagePriceRatio)
		})
	}
}
