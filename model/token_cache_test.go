package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestIsTokenCacheComplete(t *testing.T) {
	tests := []struct {
		name     string
		token    *Token
		complete bool
	}{
		{name: "nil", token: nil, complete: false},
		{name: "quota field only", token: &Token{RemainQuota: 100}, complete: false},
		{name: "missing user", token: &Token{Id: 1, Status: common.TokenStatusEnabled}, complete: false},
		{name: "missing status", token: &Token{Id: 1, UserId: 2}, complete: false},
		{name: "enabled", token: &Token{Id: 1, UserId: 2, Status: common.TokenStatusEnabled}, complete: true},
		{name: "disabled", token: &Token{Id: 1, UserId: 2, Status: common.TokenStatusDisabled}, complete: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.complete, isTokenCacheComplete(test.token))
		})
	}
}
