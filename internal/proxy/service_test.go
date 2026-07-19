package proxy

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/object"
)

// recordingReader captures exact object identities selected by the proxy service.
type recordingReader struct {
	key object.ObjectKey
}

// Head records the exact key and returns fixed attributes.
func (reader *recordingReader) Head(_ context.Context, key object.ObjectKey) (Attributes, error) {
	reader.key = key
	size, _ := object.NewByteSize(7)
	return Attributes{Size: size}, nil
}

// Get records the exact key and returns a fixed streaming object.
func (reader *recordingReader) Get(_ context.Context, key object.ObjectKey) (Object, error) {
	reader.key = key
	size, _ := object.NewByteSize(7)
	return Object{Attributes: Attributes{Size: size}, Body: io.NopCloser(strings.NewReader("catalog"))}, nil
}

// TestServiceMapsOneExactValidatedPath proves prefixing occurs without cleaning or query semantics.
func TestServiceMapsOneExactValidatedPath(t *testing.T) {
	t.Parallel()
	prefix, err := object.ParseKeyPrefix("private/incus")
	require.NoError(t, err)
	reader := &recordingReader{}
	service := NewService(reader, prefix)

	result, err := service.Get(context.Background(), "/streams/v1/index.json")
	require.NoError(t, err)
	require.NoError(t, result.Body.Close())
	assert.Equal(t, "private/incus/streams/v1/index.json", reader.key.String())
}

// TestServiceRejectsUnsafePathsRatherThanCleaningThem proves every structural ambiguity fails closed.
func TestServiceRejectsUnsafePathsRatherThanCleaningThem(t *testing.T) {
	t.Parallel()
	service := NewService(&recordingReader{}, "")
	tests := []string{
		"",
		"/",
		"streams/v1/index.json",
		"//streams/v1/index.json",
		"/streams/../index.json",
		`/streams\v1/index.json`,
		"/streams/%2e%2e/index.json",
		"/streams%2Fv1/index.json",
		"/streams/%00/index.json",
		"/streams/%zz/index.json",
	}
	for _, escapedPath := range tests {
		t.Run(escapedPath, func(t *testing.T) {
			t.Parallel()
			_, err := service.Head(context.Background(), escapedPath)
			require.Error(t, err)
			assert.Equal(t, failure.KindInvalidInput, failure.KindOf(err))
		})
	}
}
