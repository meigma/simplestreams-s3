// Package object defines validated S3-neutral object identities and attributes.
package object

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// BucketName is a validated general-purpose S3 bucket name.
type BucketName string

// KeyPrefix is an optional validated object-key prefix without edge slashes.
type KeyPrefix string

// ObjectKey is a validated S3 key relative to one bucket.
//
//nolint:revive // ObjectKey deliberately distinguishes S3 keys from Simple Streams paths.
type ObjectKey string

// ByteSize is a non-negative object size in bytes.
type ByteSize int64

// SHA256Digest is a binary SHA-256 content identity.
type SHA256Digest [sha256.Size]byte

// CRC64NVME is an S3 full-object CRC-64/NVME checksum.
type CRC64NVME uint64

// NewBucketName validates a general-purpose S3 bucket name.
func NewBucketName(value string) (BucketName, error) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < 3 || len(trimmed) > 63 {
		return "", errors.New("bucket name must contain 3 to 63 characters")
	}
	if trimmed != value || strings.ContainsAny(trimmed, "/\\") {
		return "", errors.New("bucket name contains invalid whitespace or separators")
	}
	return BucketName(trimmed), nil
}

// ParseKeyPrefix validates an optional slash-separated mirror prefix.
func ParseKeyPrefix(value string) (KeyPrefix, error) {
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, "\\") {
		return "", errors.New("key prefix must not have edge slashes or backslashes")
	}
	for segment := range strings.SplitSeq(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("key prefix contains unsafe segment %q", segment)
		}
	}
	return KeyPrefix(value), nil
}

// NewObjectKey validates a non-empty slash-separated object key.
func NewObjectKey(value string) (ObjectKey, error) {
	if value == "" || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, "\\") {
		return "", errors.New("object key must be a non-empty relative slash-separated path")
	}
	for segment := range strings.SplitSeq(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("object key contains unsafe segment %q", segment)
		}
	}
	return ObjectKey(value), nil
}

// NewByteSize validates a non-negative byte size.
func NewByteSize(value int64) (ByteSize, error) {
	if value < 0 {
		return 0, errors.New("byte size must not be negative")
	}
	return ByteSize(value), nil
}

// ParseSHA256Digest validates a lowercase or uppercase hexadecimal digest.
func ParseSHA256Digest(value string) (SHA256Digest, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return SHA256Digest{}, errors.New("SHA-256 digest must contain 64 hexadecimal characters")
	}
	var digest SHA256Digest
	copy(digest[:], decoded)
	return digest, nil
}

// NewSHA256Digest validates and copies one binary SHA-256 value.
func NewSHA256Digest(value []byte) (SHA256Digest, error) {
	if len(value) != sha256.Size {
		return SHA256Digest{}, errors.New("binary SHA-256 digest must contain 32 bytes")
	}
	var digest SHA256Digest
	copy(digest[:], value)
	return digest, nil
}

// NewCRC64NVME constructs a checksum from a completed hash value.
func NewCRC64NVME(value uint64) CRC64NVME { return CRC64NVME(value) }

// DigestBytes calculates the SHA-256 identity of data.
func DigestBytes(data []byte) SHA256Digest {
	return SHA256Digest(sha256.Sum256(data))
}

// DigestReader streams r into a SHA-256 identity and returns the byte count.
func DigestReader(r io.Reader) (SHA256Digest, ByteSize, error) {
	hasher := sha256.New()
	written, err := io.Copy(hasher, r)
	if err != nil {
		return SHA256Digest{}, 0, fmt.Errorf("read digest source: %w", err)
	}
	var digest SHA256Digest
	copy(digest[:], hasher.Sum(nil))
	return digest, ByteSize(written), nil
}

// String returns the bucket name as configured.
func (name BucketName) String() string { return string(name) }

// String returns the prefix without edge slashes.
func (prefix KeyPrefix) String() string { return string(prefix) }

// Join prepends the prefix to key without cleaning either value.
func (prefix KeyPrefix) Join(key ObjectKey) ObjectKey {
	if prefix == "" {
		return key
	}
	return ObjectKey(string(prefix) + "/" + string(key))
}

// String returns the exact S3 key.
func (key ObjectKey) String() string { return string(key) }

// Int64 returns the size for I/O and SDK boundaries.
func (size ByteSize) Int64() int64 { return int64(size) }

// String returns the lowercase hexadecimal SHA-256 digest.
func (digest SHA256Digest) String() string { return hex.EncodeToString(digest[:]) }

// Bytes returns a copy of the binary SHA-256 digest.
func (digest SHA256Digest) Bytes() []byte { return append([]byte(nil), digest[:]...) }

// Uint64 returns the checksum for encoding at an adapter boundary.
func (checksum CRC64NVME) Uint64() uint64 { return uint64(checksum) }
