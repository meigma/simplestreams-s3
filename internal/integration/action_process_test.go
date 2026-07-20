//go:build integration

package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/simplestreams-s3/internal/testfixture"
)

const actionCLIVersion = "0.1.0"

// TestGitHubActionPublishesAndRepeats proves the packaged action against a real released CLI and MinIO.
func TestGitHubActionPublishesAndRepeats(t *testing.T) {
	requireActionRuntime(t)
	scenario := newMinIOScenario(t)
	metadataPath, diskPath := testfixture.WriteSplitVM(
		t,
		t.TempDir(),
		testfixture.DefaultVMOptions(),
	)
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	toolCache := t.TempDir()
	runnerTemp := t.TempDir()

	first := runAction(t, repositoryRoot, toolCache, runnerTemp, metadataPath, diskPath)
	assert.Contains(t, first.log, "Installed simplestreams-s3 0.1.0")
	assert.Equal(t, actionCLIVersion, first.outputs["cli-version"])
	assert.Equal(t, "alpinelinux:3.22:cloud:arm64", first.outputs["product"])
	assert.Equal(t, "202607181302", first.outputs["image-version"])
	assert.FileExists(t, first.outputs["cli-path"])
	firstKeys, err := scenario.objectKeys()
	require.NoError(t, err)
	require.Len(t, firstKeys, minIOInitialObjectCount)

	second := runAction(t, repositoryRoot, toolCache, runnerTemp, metadataPath, diskPath)
	assert.Contains(t, second.log, "Using cached simplestreams-s3 0.1.0")
	assert.Equal(t, first.outputs, second.outputs)
	secondKeys, err := scenario.objectKeys()
	require.NoError(t, err)
	assert.Equal(t, firstKeys, secondKeys)
}

// actionResult captures observable runner output from one local action invocation.
type actionResult struct {
	log     string
	outputs map[string]string
}

// runAction invokes the packaged JavaScript action with GitHub runner command files.
func runAction(
	t *testing.T,
	repositoryRoot string,
	toolCache string,
	runnerTemp string,
	metadataPath string,
	diskPath string,
) actionResult {
	t.Helper()
	commandDirectory := t.TempDir()
	outputPath := filepath.Join(commandDirectory, "output")
	pathPath := filepath.Join(commandDirectory, "path")
	require.NoError(t, os.WriteFile(outputPath, nil, 0o600))
	require.NoError(t, os.WriteFile(pathPath, nil, 0o600))

	command := exec.CommandContext(
		t.Context(),
		"node",
		filepath.Join(repositoryRoot, "action", "dist", "index.js"),
	)
	command.Dir = repositoryRoot
	command.Env = append(os.Environ(),
		"RUNNER_TOOL_CACHE="+toolCache,
		"RUNNER_TEMP="+runnerTemp,
		"GITHUB_OUTPUT="+outputPath,
		"GITHUB_PATH="+pathPath,
		"INPUT_VERSION=v"+actionCLIVersion,
		"INPUT_METADATA-PATH="+metadataPath,
		"INPUT_DISK-PATH="+diskPath,
		"INPUT_S3-BUCKET="+minIOBucket,
		"INPUT_S3-REGION="+minIORegion,
		"INPUT_ALIASES=alpinelinux/stable\nalpinelinux/latest",
	)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	commandOutput, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	return actionResult{
		log:     string(output),
		outputs: parseActionOutputs(t, string(commandOutput)),
	}
}

// parseActionOutputs decodes the multiline GitHub output command-file protocol.
func parseActionOutputs(t *testing.T, contents string) map[string]string {
	t.Helper()
	lines := strings.Split(strings.ReplaceAll(contents, "\r\n", "\n"), "\n")
	outputs := make(map[string]string)
	for lineIndex := 0; lineIndex < len(lines); lineIndex++ {
		name, delimiter, found := strings.Cut(lines[lineIndex], "<<")
		if !found || name == "" || delimiter == "" {
			continue
		}
		valueStart := lineIndex + 1
		for lineIndex++; lineIndex < len(lines); lineIndex++ {
			if lines[lineIndex] != delimiter {
				continue
			}
			outputs[name] = strings.Join(lines[valueStart:lineIndex], "\n")
			break
		}
	}
	require.Contains(t, outputs, "cli-version")
	require.Contains(t, outputs, "cli-path")
	require.Contains(t, outputs, "product")
	require.Contains(t, outputs, "image-version")
	return outputs
}

// requireActionRuntime skips unsupported hosts and requires the action's Node.js baseline.
func requireActionRuntime(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("released action CLI does not support %s", runtime.GOOS)
	}
	output, err := exec.CommandContext(t.Context(), "node", "--version").Output()
	if err != nil {
		t.Skip("Node.js is not available")
	}
	majorText, _, found := strings.Cut(strings.TrimPrefix(strings.TrimSpace(string(output)), "v"), ".")
	require.True(t, found, "unexpected Node.js version %q", output)
	major, err := strconv.Atoi(majorText)
	require.NoError(t, err)
	if major < 24 {
		t.Skipf("the action requires Node.js 24 or newer, found %d", major)
	}
}
