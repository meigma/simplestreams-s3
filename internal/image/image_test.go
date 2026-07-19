package image

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	simplestreams "github.com/meigma/go-simplestreams"

	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/testfixture"
)

// TestInspectAcceptsOneValidSplitVM proves normalization, identities, and combined fingerprint behavior.
func TestInspectAcceptsOneValidSplitVM(t *testing.T) {
	t.Parallel()
	metadataPath, diskPath := testfixture.WriteSplitVM(t, t.TempDir(), testfixture.DefaultVMOptions())

	vm, err := Inspect(metadataPath, diskPath)
	require.NoError(t, err)
	assert.Equal(t, ArchitectureARM64, vm.Architecture())
	assert.Equal(t, "alpinelinux", vm.OperatingSystem().String())
	assert.Equal(t, "3.22", vm.Release().String())
	assert.Equal(t, "cloud", vm.Variant().String())
	assert.Positive(t, vm.Metadata().Size().Int64())
	assert.Positive(t, vm.Disk().Size().Int64())

	metadataFile, err := os.Open(metadataPath)
	require.NoError(t, err)
	defer metadataFile.Close()
	diskFile, err := os.Open(diskPath)
	require.NoError(t, err)
	defer diskFile.Close()
	want, err := simplestreams.SHA256Concat(metadataFile, diskFile)
	require.NoError(t, err)
	got, err := vm.Fingerprint()
	require.NoError(t, err)
	assert.Equal(t, want, got.String())
}

// TestInspectRejectsInvalidVMInputs proves V1 fails unsupported and malformed inputs before storage.
func TestInspectRejectsInvalidVMInputs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		mutate     func(testfixture.VMOptions) testfixture.VMOptions
		mutateDisk bool
		kind       failure.Kind
	}{
		{
			name: "unsupported architecture",
			mutate: func(options testfixture.VMOptions) testfixture.VMOptions {
				options.Architecture = "riscv64"
				options.PropertyArchitecture = "riscv64"
				return options
			},
			kind: failure.KindUnsupportedImage,
		},
		{
			name: "mismatched architectures",
			mutate: func(options testfixture.VMOptions) testfixture.VMOptions {
				options.PropertyArchitecture = "amd64"
				return options
			},
			kind: failure.KindInvalidInput,
		},
		{
			name: "missing description",
			mutate: func(options testfixture.VMOptions) testfixture.VMOptions {
				options.Description = ""
				return options
			},
			kind: failure.KindInvalidInput,
		},
		{
			name: "non QCOW2 disk",
			mutate: func(options testfixture.VMOptions) testfixture.VMOptions {
				return options
			},
			mutateDisk: true,
			kind:       failure.KindUnsupportedImage,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := test.mutate(testfixture.DefaultVMOptions())
			metadataPath, diskPath := testfixture.WriteSplitVM(t, t.TempDir(), options)
			if test.mutateDisk {
				require.NoError(t, os.WriteFile(diskPath, []byte("not a disk"), 0o600))
			}
			_, err := Inspect(metadataPath, diskPath)
			require.Error(t, err)
			assert.Equal(t, test.kind, failure.KindOf(err))
		})
	}
}
