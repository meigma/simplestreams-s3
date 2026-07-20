//go:build integration

package integration_test

import (
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/simplestreams-s3/internal/testfixture"
)

const (
	actionWorkflowTestEnvironment = "SIMPLESTREAMS_S3_ACTION_WORKFLOW_TEST"
	actionFixtureDirEnvironment   = "SIMPLESTREAMS_S3_ACTION_FIXTURE_DIR"
	actionEndpointEnvironment     = "SIMPLESTREAMS_S3_TEST_S3_ENDPOINT"
)

// TestPrepareGitHubActionWorkflow creates the external MinIO bucket and action fixture.
func TestPrepareGitHubActionWorkflow(t *testing.T) {
	requireActionWorkflowTest(t)
	writeActionWorkflowFixture(t)

	client := newMinIOClient(t, requireActionWorkflowEnvironment(t, actionEndpointEnvironment))
	_, err := client.CreateBucket(t.Context(), &s3.CreateBucketInput{
		Bucket: aws.String(minIOBucket),
	})
	require.NoError(t, err)
}

// TestPrepareGitHubActionAWSWorkflow creates the fixture for a real AWS workflow run.
func TestPrepareGitHubActionAWSWorkflow(t *testing.T) {
	requireActionWorkflowTest(t)
	writeActionWorkflowFixture(t)
}

// TestVerifyGitHubActionWorkflow verifies idempotent action publication in external MinIO.
func TestVerifyGitHubActionWorkflow(t *testing.T) {
	requireActionWorkflowTest(t)
	client := newMinIOClient(t, requireActionWorkflowEnvironment(t, actionEndpointEnvironment))
	scenario := &minIOScenario{t: t, s3: client}
	keys, err := scenario.objectKeys()
	require.NoError(t, err)
	require.Len(t, keys, minIOInitialObjectCount)
	assert.True(t, strings.HasPrefix(keys[0], "images/"))
	assert.True(t, strings.HasPrefix(keys[1], "images/"))
	assert.True(t, strings.HasPrefix(keys[2], "streams/v1/images-"))
	assert.Equal(t, "streams/v1/index.json", keys[3])
}

// requireActionWorkflowTest skips helpers outside the explicit workflow proof.
func requireActionWorkflowTest(t *testing.T) {
	t.Helper()
	if os.Getenv(actionWorkflowTestEnvironment) != "1" {
		t.Skip("GitHub Action workflow proof is not enabled")
	}
}

// requireActionWorkflowEnvironment returns one required workflow environment value.
func requireActionWorkflowEnvironment(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	require.NotEmpty(t, value, "%s must be set", name)
	return value
}

// writeActionWorkflowFixture writes the shared split-VM action fixture.
func writeActionWorkflowFixture(t *testing.T) {
	t.Helper()
	fixtureDir := requireActionWorkflowEnvironment(t, actionFixtureDirEnvironment)
	require.NoError(t, os.MkdirAll(fixtureDir, 0o700))
	testfixture.WriteSplitVM(t, fixtureDir, testfixture.DefaultVMOptions())
}
