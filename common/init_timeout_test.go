package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
)

func TestStreamingTimeoutDefaultAndOverride(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	for _, tc := range []struct {
		name  string
		value string
		want  int
	}{
		{"default", "", 1200},
		{"explicit", "1200", 1200},
		{"override", "42", 42},
		{"invalid", "invalid", 1200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("STREAMING_TIMEOUT", tc.value)
			initConstantEnv()
			require.Equal(t, tc.want, constant.StreamingTimeout)
		})
	}
}
