// Package image validates and models the V1 split Incus virtual-machine input.
package image

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash/crc64"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/ulikunitz/xz"
	"go.yaml.in/yaml/v3"

	simplestreams "github.com/meigma/go-simplestreams"

	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/object"
)

const (
	maxArchiveBytes  = int64(64 << 20)
	maxMetadataBytes = int64(1 << 20)
	crc64NVMEPoly    = 0x9a6c_9329_ac4b_c9b5
	qcow2HeaderSize  = 72
)

// Architecture is a supported Incus and Simple Streams architecture identity.
type Architecture string

const (
	// ArchitectureAMD64 is the 64-bit x86 architecture.
	ArchitectureAMD64 Architecture = "amd64"
	// ArchitectureARM64 is the 64-bit Arm architecture.
	ArchitectureARM64 Architecture = "arm64"
)

// OperatingSystem is the non-empty operating-system catalog identity.
type OperatingSystem string

// Release is the non-empty operating-system release identity.
type Release string

// Variant is the non-empty image variant identity.
type Variant string

// ArtifactKind is a closed V1 artifact class.
type ArtifactKind string

const (
	// ArtifactMetadata is the Incus metadata tarball.
	ArtifactMetadata ArtifactKind = "incus.tar.xz"
	// ArtifactDisk is the QCOW2 virtual-machine disk.
	ArtifactDisk ArtifactKind = "disk-kvm.img"
)

// Artifact is one validated, reopenable local input and its content identities.
type Artifact struct {
	kind   ArtifactKind
	path   string
	size   object.ByteSize
	sha256 object.SHA256Digest
	crc64  object.CRC64NVME
}

// VMImage structurally contains exactly the two artifacts supported by V1.
type VMImage struct {
	metadata     Artifact
	disk         Artifact
	architecture Architecture
	os           OperatingSystem
	release      Release
	variant      Variant
	created      time.Time
}

// metadataDocument is the subset of Incus metadata.yaml owned by the V1 contract.
type metadataDocument struct {
	Architecture string             `yaml:"architecture"`
	CreationDate int64              `yaml:"creation_date"`
	Properties   metadataProperties `yaml:"properties"`
}

// metadataProperties is the required identity subset of Incus image properties.
type metadataProperties struct {
	Architecture string `yaml:"architecture"`
	Description  string `yaml:"description"`
	OS           string `yaml:"os"`
	Release      string `yaml:"release"`
	Variant      string `yaml:"variant"`
}

// archiveScan tracks bounded expansion and the sole accepted metadata document.
type archiveScan struct {
	metadata []byte
	expanded int64
}

// Inspect validates both input files and calculates their streaming identities.
func Inspect(metadataPath string, diskPath string) (VMImage, error) {
	metadata, err := inspectMetadataArchive(metadataPath)
	if err != nil {
		return VMImage{}, err
	}
	if qcowErr := inspectQCOW2(diskPath); qcowErr != nil {
		return VMImage{}, qcowErr
	}

	metadataArtifact, err := inspectArtifact(ArtifactMetadata, metadataPath)
	if err != nil {
		return VMImage{}, err
	}
	diskArtifact, err := inspectArtifact(ArtifactDisk, diskPath)
	if err != nil {
		return VMImage{}, err
	}

	architecture, err := parseArchitecture(metadata.Architecture)
	if err != nil {
		return VMImage{}, err
	}
	propertyArchitecture, err := parseArchitecture(metadata.Properties.Architecture)
	if err != nil {
		return VMImage{}, err
	}
	if architecture != propertyArchitecture {
		return VMImage{}, failure.New(
			failure.KindInvalidInput,
			"inspect metadata",
			"metadata architecture fields do not identify the same architecture",
		)
	}
	if metadata.CreationDate <= 0 {
		return VMImage{}, failure.New(failure.KindInvalidInput, "inspect metadata", "creation_date must be positive")
	}

	osName, err := newIdentity[OperatingSystem]("properties.os", metadata.Properties.OS)
	if err != nil {
		return VMImage{}, err
	}
	release, err := newIdentity[Release]("properties.release", metadata.Properties.Release)
	if err != nil {
		return VMImage{}, err
	}
	variant, err := newIdentity[Variant]("properties.variant", metadata.Properties.Variant)
	if err != nil {
		return VMImage{}, err
	}
	if strings.TrimSpace(metadata.Properties.Description) == "" {
		return VMImage{}, failure.New(
			failure.KindInvalidInput,
			"inspect metadata",
			"properties.description is required",
		)
	}

	return VMImage{
		metadata:     metadataArtifact,
		disk:         diskArtifact,
		architecture: architecture,
		os:           osName,
		release:      release,
		variant:      variant,
		created:      time.Unix(metadata.CreationDate, 0).UTC(),
	}, nil
}

// Metadata returns the validated Incus metadata artifact.
func (image VMImage) Metadata() Artifact { return image.metadata }

// Disk returns the validated QCOW2 disk artifact.
func (image VMImage) Disk() Artifact { return image.disk }

// Architecture returns the normalized Simple Streams architecture.
func (image VMImage) Architecture() Architecture { return image.architecture }

// OperatingSystem returns the catalog operating-system identity.
func (image VMImage) OperatingSystem() OperatingSystem { return image.os }

// Release returns the catalog release identity.
func (image VMImage) Release() Release { return image.release }

// Variant returns the catalog variant identity.
func (image VMImage) Variant() Variant { return image.variant }

// Created returns the UTC image creation time.
func (image VMImage) Created() time.Time { return image.created }

// Fingerprint calculates the Incus metadata-first combined SHA-256 identity.
func (image VMImage) Fingerprint() (object.SHA256Digest, error) {
	metadataFile, err := image.metadata.Open()
	if err != nil {
		return object.SHA256Digest{}, err
	}
	defer metadataFile.Close()
	diskFile, err := image.disk.Open()
	if err != nil {
		return object.SHA256Digest{}, err
	}
	defer diskFile.Close()

	digest, err := simplestreams.SHA256Concat(metadataFile, diskFile)
	if err != nil {
		return object.SHA256Digest{}, failure.Wrap(failure.KindIntegrity, "calculate image fingerprint", err)
	}
	parsed, err := object.ParseSHA256Digest(digest)
	if err != nil {
		return object.SHA256Digest{}, failure.Wrap(failure.KindInternal, "parse image fingerprint", err)
	}
	return parsed, nil
}

// Kind returns the closed artifact class.
func (artifact Artifact) Kind() ArtifactKind { return artifact.kind }

// Size returns the validated local byte size.
func (artifact Artifact) Size() object.ByteSize { return artifact.size }

// SHA256 returns the artifact content identity.
func (artifact Artifact) SHA256() object.SHA256Digest { return artifact.sha256 }

// CRC64NVME returns the full-object S3 checksum.
func (artifact Artifact) CRC64NVME() object.CRC64NVME { return artifact.crc64 }

// Open returns a fresh reader for the validated local path.
func (artifact Artifact) Open() (*os.File, error) {
	file, err := os.Open(artifact.path)
	if err != nil {
		return nil, failure.Wrap(failure.KindInvalidInput, "open artifact", err)
	}
	return file, nil
}

// String returns the Simple Streams architecture value.
func (architecture Architecture) String() string { return string(architecture) }

// String returns the catalog operating-system value.
func (name OperatingSystem) String() string { return string(name) }

// String returns the catalog release value.
func (release Release) String() string { return string(release) }

// String returns the catalog variant value.
func (variant Variant) String() string { return string(variant) }

// String returns the protocol file type for kind.
func (kind ArtifactKind) String() string { return string(kind) }

// newIdentity validates one non-empty colon- and slash-free catalog identity.
func newIdentity[T ~string](field string, value string) (T, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed != value || strings.ContainsAny(trimmed, ":/\\") {
		return "", failure.New(failure.KindInvalidInput, "inspect metadata", field+" is invalid")
	}
	return T(trimmed), nil
}

// parseArchitecture normalizes one supported Incus architecture name.
func parseArchitecture(value string) (Architecture, error) {
	switch strings.TrimSpace(value) {
	case "amd64", "x86_64":
		return ArchitectureAMD64, nil
	case "arm64", "aarch64":
		return ArchitectureARM64, nil
	default:
		return "", failure.New(failure.KindUnsupportedImage, "inspect metadata", "unsupported architecture")
	}
}

// inspectMetadataArchive extracts and validates the bounded metadata document.
func inspectMetadataArchive(filename string) (metadataDocument, error) {
	file, err := os.Open(filename)
	if err != nil {
		return metadataDocument{}, failure.Wrap(failure.KindInvalidInput, "open metadata archive", err)
	}
	defer file.Close()

	xzReader, err := xz.NewReader(file)
	if err != nil {
		return metadataDocument{}, failure.Wrap(failure.KindInvalidInput, "decode metadata archive", err)
	}
	metadataBytes, err := readMetadataArchive(tar.NewReader(xzReader))
	if err != nil {
		return metadataDocument{}, err
	}

	var document metadataDocument
	if decodeErr := yaml.Unmarshal(metadataBytes, &document); decodeErr != nil {
		return metadataDocument{}, failure.Wrap(failure.KindInvalidInput, "decode metadata.yaml", decodeErr)
	}
	return document, nil
}

// readMetadataArchive finds the sole root metadata document while bounding expansion.
func readMetadataArchive(tarReader *tar.Reader) ([]byte, error) {
	scan := archiveScan{}
	for {
		header, nextErr := tarReader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nil, failure.Wrap(failure.KindInvalidInput, "read metadata archive", nextErr)
		}
		if memberErr := validateArchiveMember(header); memberErr != nil {
			return nil, memberErr
		}
		if consumeErr := scan.consume(tarReader, header); consumeErr != nil {
			return nil, consumeErr
		}
	}
	if scan.metadata == nil {
		return nil, failure.New(failure.KindInvalidInput, "inspect metadata archive", "metadata.yaml is missing")
	}
	return scan.metadata, nil
}

// consume validates bounds and drains one regular archive member.
func (scan *archiveScan) consume(tarReader *tar.Reader, header *tar.Header) error {
	if header.Typeflag != tar.TypeReg {
		return nil
	}
	scan.expanded += header.Size
	if scan.expanded > maxArchiveBytes {
		return failure.New(failure.KindInvalidInput, "inspect metadata archive", "expanded archive exceeds 64 MiB")
	}
	if path.Base(header.Name) != "metadata.yaml" {
		if _, err := io.CopyN(io.Discard, tarReader, header.Size); err != nil {
			return failure.Wrap(failure.KindInvalidInput, "read metadata archive member", err)
		}
		return nil
	}
	if header.Name != "metadata.yaml" || scan.metadata != nil {
		return failure.New(
			failure.KindInvalidInput,
			"inspect metadata archive",
			"metadata.yaml must appear exactly once at archive root",
		)
	}
	if header.Size > maxMetadataBytes {
		return failure.New(failure.KindInvalidInput, "inspect metadata archive", "metadata.yaml exceeds 1 MiB")
	}
	metadata, err := io.ReadAll(io.LimitReader(tarReader, header.Size))
	if err != nil {
		return failure.Wrap(failure.KindInvalidInput, "read metadata.yaml", err)
	}
	scan.metadata = metadata
	return nil
}

// validateArchiveMember rejects unsafe paths, links, devices, and V1-excluded payloads.
func validateArchiveMember(header *tar.Header) error {
	name := header.Name
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
		return failure.New(
			failure.KindInvalidInput,
			"inspect metadata archive",
			"archive contains an unsafe member path",
		)
	}
	checkName := name
	if header.Typeflag == tar.TypeDir {
		checkName = strings.TrimSuffix(checkName, "/")
	}
	for segment := range strings.SplitSeq(checkName, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return failure.New(
				failure.KindInvalidInput,
				"inspect metadata archive",
				"archive contains an unsafe member path",
			)
		}
	}
	switch header.Typeflag {
	case tar.TypeReg, tar.TypeDir:
	default:
		return failure.New(
			failure.KindInvalidInput,
			"inspect metadata archive",
			"archive contains a non-regular member",
		)
	}

	base := path.Base(name)
	if base == "rootfs.squashfs" || strings.HasPrefix(base, "rootfs.tar.") || strings.HasPrefix(base, "lxd.tar.") {
		return failure.New(
			failure.KindUnsupportedImage,
			"inspect metadata archive",
			"unified or container image payload is unsupported",
		)
	}
	return nil
}

// inspectQCOW2 validates the fixed QCOW2 header fields needed to reject other disk formats.
func inspectQCOW2(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return failure.Wrap(failure.KindInvalidInput, "open QCOW2 disk", err)
	}
	defer file.Close()

	header := make([]byte, qcow2HeaderSize)
	if _, err := io.ReadFull(file, header); err != nil {
		return failure.Wrap(failure.KindUnsupportedImage, "read QCOW2 header", err)
	}
	if string(header[:4]) != "QFI\xfb" {
		return failure.New(
			failure.KindUnsupportedImage,
			"inspect QCOW2 disk",
			"disk does not have a QCOW2 magic header",
		)
	}
	version := binary.BigEndian.Uint32(header[4:8])
	clusterBits := binary.BigEndian.Uint32(header[20:24])
	virtualSize := binary.BigEndian.Uint64(header[24:32])
	if (version < 1 || version > 3) || clusterBits < 9 || clusterBits > 21 || virtualSize == 0 {
		return failure.New(failure.KindUnsupportedImage, "inspect QCOW2 disk", "disk has an invalid QCOW2 header")
	}
	return nil
}

// inspectArtifact calculates SHA-256 and CRC-64/NVME in one bounded streaming pass.
func inspectArtifact(kind ArtifactKind, filename string) (Artifact, error) {
	file, err := os.Open(filename)
	if err != nil {
		return Artifact{}, failure.Wrap(failure.KindInvalidInput, "open artifact", err)
	}
	defer file.Close()

	shaHasher := sha256.New()
	crcHasher := crc64.New(crc64.MakeTable(crc64NVMEPoly))
	written, err := io.Copy(io.MultiWriter(shaHasher, crcHasher), file)
	if err != nil {
		return Artifact{}, failure.Wrap(failure.KindInvalidInput, "read artifact", err)
	}
	digest, err := object.NewSHA256Digest(shaHasher.Sum(nil))
	if err != nil {
		return Artifact{}, failure.Wrap(failure.KindInternal, "record artifact digest", err)
	}
	size, err := object.NewByteSize(written)
	if err != nil {
		return Artifact{}, failure.Wrap(failure.KindInternal, "record artifact size", err)
	}
	return Artifact{
		kind:   kind,
		path:   filename,
		size:   size,
		sha256: digest,
		crc64:  object.NewCRC64NVME(crcHasher.Sum64()),
	}, nil
}
