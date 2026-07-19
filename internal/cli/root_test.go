package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVersionCommandPrintsBuildMetadata proves the explicit command is stable and runnable.
func TestVersionCommandPrintsBuildMetadata(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	root := NewRootCommand(Options{
		Out: &stdout,
		Build: BuildInfo{
			Version: "0.2.0",
			Commit:  "abc1234",
			Date:    "2026-07-19T12:00:00Z",
		},
	})
	root.SetArgs([]string{"version"})

	require.NoError(t, root.ExecuteContext(context.Background()))
	assert.Equal(t, "simplestreams-s3 0.2.0 (abc1234) built 2026-07-19T12:00:00Z\n", stdout.String())
}

// TestRootExposesOnlyTheDesignedCommands proves the public command surface excludes template behavior.
func TestRootExposesOnlyTheDesignedCommands(t *testing.T) {
	t.Parallel()
	root := NewRootCommand(Options{})
	names := make([]string, 0, len(root.Commands()))
	for _, command := range root.Commands() {
		names = append(names, command.Name())
	}
	assert.ElementsMatch(t, []string{"proxy", "publish", "version"}, names)
}
