// Command generate creates the disposable Phase 1 Incus Simple Streams mirror.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	simplestreams "github.com/meigma/go-simplestreams"
	"github.com/meigma/go-simplestreams/schema/incus"
)

// config contains the explicit inputs for the disposable catalog generator.
type config struct {
	metadataPath string
	diskPath     string
	outputPath   string
	os           string
	release      string
	variant      string
	architecture string
	creationDate int64
}

// artifact describes one source artifact and its Simple Streams identity.
type artifact struct {
	sourcePath string
	path       simplestreams.RelativePath
	size       int64
	sha256     string
}

// result records the observable identities needed by the compatibility proof.
type result struct {
	Product        string `json:"product"`
	Alias          string `json:"alias"`
	Version        string `json:"version"`
	Architecture   string `json:"architecture"`
	Fingerprint    string `json:"fingerprint"`
	MetadataPath   string `json:"metadata_path"`
	MetadataSHA256 string `json:"metadata_sha256"`
	DiskPath       string `json:"disk_path"`
	DiskSHA256     string `json:"disk_sha256"`
	ProductPath    string `json:"product_path"`
}

// catalog contains the rendered documents and artifact identities for one VM.
type catalog struct {
	productFile *simplestreams.ProductFile
	index       *simplestreams.Index
	productBody []byte
	indexBody   []byte
	metadata    artifact
	disk        artifact
	result      result
}

// main parses the spike flags, generates the mirror, and prints proof identities.
func main() {
	cfg := parseFlags()
	generated, err := generate(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(generated); err != nil {
		fmt.Fprintf(os.Stderr, "encode result: %v\n", err)
		os.Exit(1)
	}
}

// parseFlags binds the explicit inputs used by this one-image experiment.
func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.metadataPath, "metadata", "", "path to the Incus metadata tarball")
	flag.StringVar(&cfg.diskPath, "disk", "", "path to the QCOW2 disk")
	flag.StringVar(&cfg.outputPath, "output", "mirror", "output mirror root")
	flag.StringVar(&cfg.os, "os", "alpine", "Simple Streams operating system")
	flag.StringVar(&cfg.release, "release", "3.22", "Simple Streams release")
	flag.StringVar(&cfg.variant, "variant", "cloud", "Simple Streams variant")
	flag.StringVar(&cfg.architecture, "architecture", "arm64", "Simple Streams architecture")
	flag.Int64Var(&cfg.creationDate, "creation-date", 0, "positive Unix creation timestamp")
	flag.Parse()
	return cfg
}

// generate builds, validates, renders, and writes the disposable mirror.
func generate(cfg config) (result, error) {
	built, err := buildCatalog(cfg)
	if err != nil {
		return result{}, err
	}
	if err := writeCatalog(cfg.outputPath, built); err != nil {
		return result{}, err
	}
	return built.result, nil
}

// buildCatalog constructs the exact V1 two-item VM catalog in memory.
func buildCatalog(cfg config) (*catalog, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	metadata, err := inspectArtifact(cfg.metadataPath, "incus.tar.xz")
	if err != nil {
		return nil, fmt.Errorf("inspect metadata: %w", err)
	}
	disk, err := inspectArtifact(cfg.diskPath, "qcow2")
	if err != nil {
		return nil, fmt.Errorf("inspect disk: %w", err)
	}

	metadata.path, err = simplestreams.ParseRelativePath("images/" + metadata.sha256 + ".incus.tar.xz")
	if err != nil {
		return nil, fmt.Errorf("metadata path: %w", err)
	}
	disk.path, err = simplestreams.ParseRelativePath("images/" + disk.sha256 + ".qcow2")
	if err != nil {
		return nil, fmt.Errorf("disk path: %w", err)
	}

	fingerprint, err := combinedSHA256(cfg.metadataPath, cfg.diskPath)
	if err != nil {
		return nil, err
	}

	created := time.Unix(cfg.creationDate, 0).UTC()
	versionName := created.Format("200601021504")
	productName := strings.Join([]string{cfg.os, cfg.release, cfg.variant, cfg.architecture}, ":")
	alias := strings.Join([]string{cfg.os, cfg.release, cfg.variant}, "/")
	updated := created.Format(time.RFC1123Z)

	productFile := simplestreams.NewProductFile(incus.ContentIDImages)
	productFile.DataType = incus.DataTypeImageDownloads
	productFile.Updated = updated
	product := productFile.SetProduct(productName, nil)
	product.SetMetadata("aliases", alias)
	product.SetMetadata("arch", cfg.architecture)
	product.SetMetadata("os", cfg.os)
	product.SetMetadata("release", cfg.release)
	product.SetMetadata("release_title", cfg.release)
	product.SetMetadata("variant", cfg.variant)
	product.SetMetadata("requirements", map[string]any{})

	version := product.SetVersion(versionName, nil)
	metadataItem := version.SetItem("incus.tar.xz", &simplestreams.Item{
		FileType: "incus.tar.xz",
		Path:     metadata.path,
		Size:     sizePointer(metadata.size),
		SHA256:   metadata.sha256,
	})
	metadataItem.SetMetadata("combined_disk-kvm-img_sha256", fingerprint)
	version.SetItem("disk-kvm.img", &simplestreams.Item{
		FileType: "disk-kvm.img",
		Path:     disk.path,
		Size:     sizePointer(disk.size),
		SHA256:   disk.sha256,
	})

	if err := incus.ValidateRuntimeProductFile(productFile); err != nil {
		return nil, fmt.Errorf("validate Incus product document: %w", err)
	}
	productBody, err := simplestreams.MarshalJSONDocument(productFile)
	if err != nil {
		return nil, fmt.Errorf("render product document: %w", err)
	}
	productDigest := sha256.Sum256(productBody)
	productPath, err := simplestreams.ParseRelativePath(
		"streams/v1/images-" + hex.EncodeToString(productDigest[:]) + ".json",
	)
	if err != nil {
		return nil, fmt.Errorf("product document path: %w", err)
	}

	index, err := simplestreams.BuildIndex([]simplestreams.BuildIndexEntry{{
		ContentID: incus.ContentIDImages,
		Path:      productPath,
		Format:    simplestreams.ProductsFormat,
		DataType:  incus.DataTypeImageDownloads,
		Updated:   updated,
		Products:  []string{productName},
	}}, updated)
	if err != nil {
		return nil, fmt.Errorf("build index: %w", err)
	}
	indexBody, err := simplestreams.MarshalJSONDocument(index)
	if err != nil {
		return nil, fmt.Errorf("render index: %w", err)
	}

	return &catalog{
		productFile: productFile,
		index:       index,
		productBody: productBody,
		indexBody:   indexBody,
		metadata:    metadata,
		disk:        disk,
		result: result{
			Product:        productName,
			Alias:          alias,
			Version:        versionName,
			Architecture:   cfg.architecture,
			Fingerprint:    fingerprint,
			MetadataPath:   metadata.path.String(),
			MetadataSHA256: metadata.sha256,
			DiskPath:       disk.path.String(),
			DiskSHA256:     disk.sha256,
			ProductPath:    productPath.String(),
		},
	}, nil
}

// validateConfig rejects missing and unsupported spike inputs before reading files.
func validateConfig(cfg config) error {
	if cfg.metadataPath == "" || cfg.diskPath == "" || cfg.outputPath == "" {
		return errors.New("metadata, disk, and output paths are required")
	}
	for name, value := range map[string]string{
		"os": cfg.os, "release": cfg.release, "variant": cfg.variant,
	} {
		if value == "" || strings.ContainsAny(value, ":/,\\") {
			return fmt.Errorf("invalid %s %q", name, value)
		}
	}
	if cfg.architecture != "amd64" && cfg.architecture != "arm64" {
		return fmt.Errorf("unsupported architecture %q", cfg.architecture)
	}
	if cfg.creationDate <= 0 {
		return errors.New("creation-date must be positive")
	}
	return nil
}

// inspectArtifact calculates one artifact's size and SHA-256 digest in one pass.
func inspectArtifact(path string, kind string) (artifact, error) {
	file, err := os.Open(path)
	if err != nil {
		return artifact{}, err
	}
	defer file.Close()

	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return artifact{}, fmt.Errorf("hash %s: %w", kind, err)
	}
	return artifact{
		sourcePath: path,
		size:       size,
		sha256:     hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

// combinedSHA256 calculates the Incus fingerprint in metadata-then-disk order.
func combinedSHA256(metadataPath string, diskPath string) (string, error) {
	metadata, err := os.Open(metadataPath)
	if err != nil {
		return "", fmt.Errorf("open metadata for fingerprint: %w", err)
	}
	defer metadata.Close()
	disk, err := os.Open(diskPath)
	if err != nil {
		return "", fmt.Errorf("open disk for fingerprint: %w", err)
	}
	defer disk.Close()

	fingerprint, err := simplestreams.SHA256Concat(metadata, disk)
	if err != nil {
		return "", fmt.Errorf("calculate fingerprint: %w", err)
	}
	return fingerprint, nil
}

// writeCatalog writes the validated documents and content-addressed artifacts.
func writeCatalog(root string, built *catalog) error {
	paths := []string{
		filepath.Join(root, "streams", "v1"),
		filepath.Join(root, "images"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", path, err)
		}
	}

	if err := os.WriteFile(
		filepath.Join(root, simplestreams.DefaultIndexPath.String()),
		built.indexBody,
		0o644,
	); err != nil {
		return fmt.Errorf("write index: %w", err)
	}
	if err := os.WriteFile(filepath.Join(root, built.result.ProductPath), built.productBody, 0o644); err != nil {
		return fmt.Errorf("write product document: %w", err)
	}
	if err := copyFile(built.metadata.sourcePath, filepath.Join(root, built.metadata.path.String())); err != nil {
		return fmt.Errorf("copy metadata: %w", err)
	}
	if err := copyFile(built.disk.sourcePath, filepath.Join(root, built.disk.path.String())); err != nil {
		return fmt.Errorf("copy disk: %w", err)
	}
	return nil
}

// copyFile streams one artifact into the disposable mirror.
func copyFile(sourcePath string, destinationPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.Create(destinationPath)
	if err != nil {
		return err
	}
	defer destination.Close()
	if _, err := io.Copy(destination, source); err != nil {
		return err
	}
	return destination.Close()
}

// sizePointer returns a stable pointer for a rendered artifact size.
func sizePointer(size int64) *int64 {
	return &size
}
