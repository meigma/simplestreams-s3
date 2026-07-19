package object

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseKeyPrefixRejectsCleaningCandidates proves prefixes are rejected rather than normalized.
func TestParseKeyPrefixRejectsCleaningCandidates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
	}{
		{name: "leading slash", value: "/mirror"},
		{name: "trailing slash", value: "mirror/"},
		{name: "empty segment", value: "mirror//images"},
		{name: "dot segment", value: "mirror/./images"},
		{name: "parent segment", value: "mirror/../images"},
		{name: "backslash", value: `mirror\images`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseKeyPrefix(test.value)
			require.Error(t, err)
		})
	}
}

// TestStrongObjectValuesRoundTrip proves validated identities retain exact values.
func TestStrongObjectValuesRoundTrip(t *testing.T) {
	t.Parallel()
	bucket, err := NewBucketName("private-images")
	require.NoError(t, err)
	prefix, err := ParseKeyPrefix("incus/mirror")
	require.NoError(t, err)
	key, err := NewObjectKey("streams/v1/index.json")
	require.NoError(t, err)
	digest, size, err := DigestReader(strings.NewReader("catalog"))
	require.NoError(t, err)

	assert.Equal(t, "private-images", bucket.String())
	assert.Equal(t, "incus/mirror/streams/v1/index.json", prefix.Join(key).String())
	assert.EqualValues(t, len("catalog"), size.Int64())
	assert.Len(t, digest.String(), 64)
}
