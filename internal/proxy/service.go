// Package proxy validates mirror paths and streams exact object reads through a port.
package proxy

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	simplestreams "github.com/meigma/go-simplestreams"

	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/object"
)

// Attributes contains transport-neutral response metadata for one object.
type Attributes struct {
	// Size is the exact representation length.
	Size object.ByteSize
	// ContentType is the stored media type when available.
	ContentType string
	// ETag is the opaque S3 entity tag when available.
	ETag string
	// LastModified is the object modification time when available.
	LastModified time.Time
	// ContentRange describes a returned byte range when present.
	ContentRange string
	// AcceptRanges declares supported byte-range units when present.
	AcceptRanges string
	// CacheControl is the stored cache-control policy when present.
	CacheControl string
	// ContentDisposition is the stored presentation directive when present.
	ContentDisposition string
	// ContentEncoding is the stored content encoding when present.
	ContentEncoding string
	// Expires is the stored HTTP expiry value when present.
	Expires string
}

// Request describes one transport-neutral conditional object read.
type Request struct {
	// Range is one syntactically valid HTTP bytes range or empty for the full object.
	Range string
	// IfMatch is the validated entity-tag condition when present.
	IfMatch string
	// IfNoneMatch is the validated entity-tag condition when present.
	IfNoneMatch string
	// IfModifiedSince is the validated modification-time condition when present.
	IfModifiedSince *time.Time
	// IfUnmodifiedSince is the validated modification-time condition when present.
	IfUnmodifiedSince *time.Time
}

// Object is a streaming object read and its response attributes.
type Object struct {
	// Attributes contains the response metadata.
	Attributes Attributes
	// Body is the exact upstream representation and must be closed.
	Body io.ReadCloser
}

// Reader is the consumer-owned object read port needed by the Phase 2 proxy.
type Reader interface {
	Head(context.Context, object.ObjectKey, Request) (Attributes, error)
	Get(context.Context, object.ObjectKey, Request) (Object, error)
}

// Service maps exact HTTP paths to authenticated object reads.
type Service struct {
	reader Reader
	prefix object.KeyPrefix
}

// NewService constructs a proxy over a validated mirror prefix.
func NewService(reader Reader, prefix object.KeyPrefix) *Service {
	return &Service{reader: reader, prefix: prefix}
}

// Head validates escapedPath and performs one exact object metadata read.
func (service *Service) Head(ctx context.Context, escapedPath string, request Request) (Attributes, error) {
	key, err := service.key(escapedPath)
	if err != nil {
		return Attributes{}, err
	}
	return service.reader.Head(ctx, key, request)
}

// Get validates escapedPath and performs one exact streaming object read.
func (service *Service) Get(ctx context.Context, escapedPath string, request Request) (Object, error) {
	key, err := service.key(escapedPath)
	if err != nil {
		return Object{}, err
	}
	return service.reader.Get(ctx, key, request)
}

// Probe checks the fixed catalog index key through the authenticated object reader.
func (service *Service) Probe(ctx context.Context) error {
	if service == nil || service.reader == nil {
		return failure.New(failure.KindInternal, "probe proxy readiness", "object reader is not configured")
	}
	key, err := object.NewObjectKey("streams/v1/index.json")
	if err != nil {
		return failure.Wrap(failure.KindInternal, "construct readiness index key", err)
	}
	_, err = service.reader.Head(ctx, service.prefix.Join(key), Request{})
	return err
}

// key validates an escaped HTTP path without cleaning or rewriting it.
func (service *Service) key(escapedPath string) (object.ObjectKey, error) {
	if service == nil || service.reader == nil {
		return "", failure.New(failure.KindInternal, "map proxy path", "object reader is not configured")
	}
	if err := rejectEncodedDelimiters(escapedPath); err != nil {
		return "", err
	}
	decoded, err := url.PathUnescape(escapedPath)
	if err != nil {
		return "", failure.Wrap(failure.KindInvalidInput, "decode proxy path", err)
	}
	if !strings.HasPrefix(decoded, "/") {
		return "", failure.New(failure.KindInvalidInput, "map proxy path", "path must begin with one slash")
	}
	decoded = strings.TrimPrefix(decoded, "/")
	if decoded == "" || strings.Contains(decoded, "\\") || strings.ContainsRune(decoded, '\x00') {
		return "", failure.New(
			failure.KindInvalidInput,
			"map proxy path",
			"path is empty or contains an unsafe delimiter",
		)
	}
	relativePath, err := simplestreams.ParseRelativePath(decoded)
	if err != nil {
		return "", failure.Wrap(failure.KindInvalidInput, "validate proxy path", err)
	}
	key, err := object.NewObjectKey(relativePath.String())
	if err != nil {
		return "", failure.Wrap(failure.KindInvalidInput, "validate object key", err)
	}
	return service.prefix.Join(key), nil
}

// rejectEncodedDelimiters rejects percent-encoded path structure before decoding once.
func rejectEncodedDelimiters(escapedPath string) error {
	for index := 0; index < len(escapedPath); index++ {
		if escapedPath[index] != '%' {
			continue
		}
		if index+2 >= len(escapedPath) {
			return failure.New(failure.KindInvalidInput, "map proxy path", "path contains an invalid percent escape")
		}
		decoded, err := url.PathUnescape(escapedPath[index : index+3])
		if err != nil {
			return failure.Wrap(failure.KindInvalidInput, "map proxy path", err)
		}
		switch decoded[0] {
		case '/', '\\', '.', '\x00':
			return failure.New(
				failure.KindInvalidInput,
				"map proxy path",
				fmt.Sprintf("path contains an encoded unsafe delimiter at byte %d", index),
			)
		}
		index += 2
	}
	return nil
}
