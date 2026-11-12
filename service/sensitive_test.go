package service

import (
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/require"
)

func TestPromptSensitiveFilterDoesNotBlockPlaceholderByDefault(t *testing.T) {
	prompt := "def test_sensitive_headers_are_never_indexed():"
	require.True(t, setting.ShouldCheckPromptSensitive())
	blocked, _ := CheckSensitiveText(prompt)
	require.False(t, blocked)

	oldWords := setting.SensitiveWords
	t.Cleanup(func() { setting.SensitiveWords = oldWords })
	setting.SensitiveWordsFromString("configured_block_word")
	blocked, words := CheckSensitiveText(prompt + " configured_block_word")
	require.True(t, blocked)
	require.Equal(t, []string{"configured_block_word"}, words)
}
