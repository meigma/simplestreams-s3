//go:build integration

package integration_test

import "testing"

// TestPublishAndProxyProcess proves the complete Phase 2 procedure against one disposable MinIO instance.
func TestPublishAndProxyProcess(t *testing.T) {
	scenario := newMinIOScenario(t)
	fixture := scenario.publishValidVM()
	scenario.requireCompleteMirror()
	scenario.requireProxyContract()
	scenario.requirePhaseTwoRefusal(fixture)
	scenario.requireCreateOnlyCollision()
}
