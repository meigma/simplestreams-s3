// Package testfixture creates bounded split-VM inputs for behavioral tests.
package testfixture

import (
	"archive/tar"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/ulikunitz/xz"
)

const (
	fixtureFileMode     = 0o600
	fixtureHeaderSize   = 72
	fixtureQCOW2Version = 3
	fixtureClusterBits  = 16
	fixtureVirtualSize  = 1 << 30
)

// VMOptions customizes the metadata.yaml identity used by WriteSplitVM.
type VMOptions struct {
	// Architecture is the top-level Incus architecture value.
	Architecture string
	// PropertyArchitecture is the properties.architecture value.
	PropertyArchitecture string
	// CreationDate is the positive Unix image creation timestamp.
	CreationDate int64
	// OS is the operating-system identity.
	OS string
	// Release is the operating-system release identity.
	Release string
	// Variant is the image variant identity.
	Variant string
	// Description is the required Incus image description.
	Description string
}

// DefaultVMOptions returns one valid arm64 Alpine cloud image identity.
func DefaultVMOptions() VMOptions {
	return VMOptions{
		Architecture:         "aarch64",
		PropertyArchitecture: "arm64",
		CreationDate:         time.Date(2026, 7, 18, 13, 2, 0, 0, time.UTC).Unix(),
		OS:                   "alpinelinux",
		Release:              "3.22",
		Variant:              "cloud",
		Description:          "Alpine 3.22 cloud arm64",
	}
}

// WriteSplitVM writes one valid compressed metadata archive and minimal QCOW2 disk.
func WriteSplitVM(t testing.TB, directory string, options VMOptions) (string, string) {
	t.Helper()
	metadataPath := filepath.Join(directory, "incus.tar.xz")
	diskPath := filepath.Join(directory, "disk.qcow2")
	writeMetadataArchive(t, metadataPath, options)
	writeQCOW2(t, diskPath)
	return metadataPath, diskPath
}

// writeMetadataArchive writes the exact root metadata.yaml fixture.
func writeMetadataArchive(t testing.TB, filename string, options VMOptions) {
	t.Helper()
	file, err := os.Create(filename)
	require.NoError(t, err)
	xzWriter, err := xz.NewWriter(file)
	require.NoError(t, err)
	tarWriter := tar.NewWriter(xzWriter)
	body := []byte(
		"architecture: " + options.Architecture + "\n" +
			"creation_date: " + strconv.FormatInt(options.CreationDate, 10) + "\n" +
			"properties:\n" +
			"  architecture: " + options.PropertyArchitecture + "\n" +
			"  description: " + options.Description + "\n" +
			"  os: " + options.OS + "\n" +
			"  release: \"" + options.Release + "\"\n" +
			"  variant: " + options.Variant + "\n",
	)
	require.NoError(t, tarWriter.WriteHeader(&tar.Header{
		Name: "metadata.yaml",
		Mode: fixtureFileMode,
		Size: int64(len(body)),
	}))
	_, err = tarWriter.Write(body)
	require.NoError(t, err)
	require.NoError(t, tarWriter.Close())
	require.NoError(t, xzWriter.Close())
	require.NoError(t, file.Close())
}

// writeQCOW2 writes the minimal fixed header fields validated by the application.
func writeQCOW2(t testing.TB, filename string) {
	t.Helper()
	header := make([]byte, 0, fixtureHeaderSize+len("fixture-disk-bytes"))
	header = append(header, make([]byte, fixtureHeaderSize)...)
	copy(header[:4], []byte{'Q', 'F', 'I', 0xfb})
	binary.BigEndian.PutUint32(header[4:8], fixtureQCOW2Version)
	binary.BigEndian.PutUint32(header[20:24], fixtureClusterBits)
	binary.BigEndian.PutUint64(header[24:32], fixtureVirtualSize)
	header = append(header, []byte("fixture-disk-bytes")...)
	require.NoError(t, os.WriteFile(filename, header, fixtureFileMode))
}
