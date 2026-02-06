package deb

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ArchiveInfo holds metadata about the repository itself.
// These fields are written to the 'Release' file.
//
// Reference: https://wiki.debian.org/DebianRepository/Format#Release_file
type ArchiveInfo struct {
	// Origin identifies the repository origin (e.g., "Debian", "MyOrg").
	//
	// Reference: https://wiki.debian.org/DebianRepository/Format#Origin
	Origin string

	// Label is a short label for the repository.
	//
	// Reference: https://wiki.debian.org/DebianRepository/Format#Label
	Label string

	// Suite specifies the suite name (e.g., "stable", "testing").
	//
	// Reference: https://wiki.debian.org/DebianRepository/Format#Suite
	Suite string

	// Version is the version of the release (e.g., "12.0").
	//
	// Reference: https://wiki.debian.org/DebianRepository/Format#Version
	Version string

	// Codename specifies the release codename (e.g., "bookworm", "jammy").
	//
	// Reference: https://wiki.debian.org/DebianRepository/Format#Codename
	Codename string

	// Architectures is a space-separated list of architectures supported by this repository.
	//
	// Reference: https://wiki.debian.org/DebianRepository/Format#Architectures
	Architectures string

	// Components is a space-separated list of repository components (e.g., "main", "contrib").
	//
	// Reference: https://wiki.debian.org/DebianRepository/Format#Components
	Components string

	// Description provides a description of the repository.
	//
	// Reference: https://wiki.debian.org/DebianRepository/Format#Description
	Description string

	// ValidUntil specifies an expiration date for the Release file.
	// Format: RFC1123Z (e.g., "Sat, 01 Jan 2000 00:00:00 UTC").
	//
	// Reference: https://wiki.debian.org/DebianRepository/Format#Valid-Until
	ValidUntil string

	// NotAutomatic, if "yes", prevents the repository from being selected by default for upgrades.
	//
	// Reference: https://wiki.debian.org/DebianRepository/Format#NotAutomatic
	NotAutomatic string

	// ButAutomaticUpgrades, if "yes" (and NotAutomatic is "yes"), allows automatic upgrades for packages already installed.
	//
	// Reference: https://wiki.debian.org/DebianRepository/Format#ButAutomaticUpgrades
	ButAutomaticUpgrades string

	// AcquireByHash, if "yes", indicates support for acquiring indices by hash.
	//
	// Reference: https://wiki.debian.org/DebianRepository/Format#Acquire-By-Hash
	AcquireByHash string
}

// Repository represents a collection of packages
// that will be assembled into a flat APT repository.
//
// Reference: https://wiki.debian.org/DebianRepository/Format#Flat_Repository_Format
type Repository struct {
	// ArchiveInfo contains the metadata for the Release file.
	ArchiveInfo ArchiveInfo
	// Packages are in-memory package definitions (generated or pre-built) to be included.
	Packages []*Package
	// GPGKey is the ASCII-armored private key used to sign the Release file.
	GPGKey string
}

// Get finds a package in the repository by its name, version, and architecture.
// It returns the package and its index if found, otherwise (nil, -1).
func (r *Repository) Get(name, version, arch string) *Package {
	for _, pkg := range r.Packages {
		if pkg.Metadata.Package == name && pkg.Metadata.Version == version && pkg.Metadata.Architecture == arch {
			return pkg
		}
	}
	return nil
}

// Append adds a package to the repository.
// If there is no conflicting package, it appends the new package and returns (nil, nil).
// If the existing package is identical to the new one, it returns the existing package and a nil error.
// If the existing package is different, it returns the existing package and an error.
func (r *Repository) Append(pkg *Package) (*Package, error) {
	if existing := r.Get(pkg.Metadata.Package, pkg.Metadata.Version, pkg.Metadata.Architecture); existing != nil {
		if existing.Equal(pkg) {
			return existing, nil
		}
		// f1, _ := os.CreateTemp("", "existing-*.txt")
		// f1.WriteString(existing.String())
		// f1.Close()
		// f2, _ := os.CreateTemp("", "new-*.txt")
		// f2.WriteString(pkg.String())
		// f2.Close()
		// fmt.Printf("existing: %s\nnew: %s\n", f1.Name(), f2.Name())

		return existing, fmt.Errorf("package %s version %s for %s already exists", pkg.Metadata.Package, pkg.Metadata.Version, pkg.Metadata.Architecture)
	}
	r.Packages = append(r.Packages, pkg)
	return nil, nil
}

// AddOverwrite adds a package to the repository, replacing any existing package
// with the same name, version, and architecture.
func (r *Repository) AddOverwrite(pkg *Package) {
	name, version, arch := pkg.Metadata.Package, pkg.Metadata.Version, pkg.Metadata.Architecture
	for i, pkg := range r.Packages {
		if pkg.Metadata.Package == name && pkg.Metadata.Version == version && pkg.Metadata.Architecture == arch {
			r.Packages[i] = pkg
			return
		}
	}
	r.Packages = append(r.Packages, pkg)
}

// PackagesByUpstream returns all packages in the repository that match the given name,
// upstream version, and architecture.
// The returned list is sorted by version in descending order (most recent first).
func (r *Repository) PackagesByUpstream(name, upstreamVersion, arch string) []*Package {
	var matches []*Package
	for _, p := range r.Packages {
		if p.Metadata.Package == name && p.Metadata.Architecture == arch && p.UpstreamVersion() == upstreamVersion {
			matches = append(matches, p)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return compareVersions(matches[j].Metadata.Version, matches[i].Metadata.Version)
	})
	return matches
}

func splitVersion(v string) (string, string) {
	lastHyphen := strings.LastIndex(v, "-")
	if lastHyphen == -1 {
		return v, ""
	}
	return v[:lastHyphen], v[lastHyphen+1:]
}

func compareVersions(v1, v2 string) bool {
	_, r1 := splitVersion(v1)
	_, r2 := splitVersion(v2)

	i1, err1 := strconv.Atoi(r1)
	i2, err2 := strconv.Atoi(r2)

	if err1 == nil && err2 == nil {
		return i1 < i2
	}
	return r1 < r2
}

// repoPackage is an internal struct to hold metadata for the index.
// It maps to the fields in the 'Packages' file.
//
// Reference: https://wiki.debian.org/DebianRepository/Format#Packages_Indices
type repoPackage struct {
	// Package is the name of the binary package.
	Package string
	// Version is the version string of the package.
	Version string
	// Architecture is the architecture of the package.
	Architecture string
	// Control is the raw content of the package's control file.
	Control string
	// Filename is the path to the package file relative to the repository root.
	//
	// Reference: https://wiki.debian.org/DebianRepository/Format#Packages_Indices
	Filename string
	// Size is the size of the package file in bytes.
	//
	// Reference: https://wiki.debian.org/DebianRepository/Format#Packages_Indices
	Size int64
	// SHA256 is the SHA256 checksum of the package file.
	//
	// Reference: https://wiki.debian.org/DebianRepository/Format#Packages_Indices
	SHA256 string
}

// WriteTo generates the repository and writes it as a tar.gz to the provided writer.
func (r *Repository) WriteTo(w io.Writer) (int64, error) {
	cw := &countingWriter{w: w}
	gzw := gzip.NewWriter(cw)
	defer gzw.Close()
	tw := tar.NewWriter(gzw)

	var index []*repoPackage

	// Helper to add file to tar
	addFile := func(name string, content []byte) error {
		header := &tar.Header{
			Name:    name,
			Size:    int64(len(content)),
			Mode:    0644,
			ModTime: time.Now(),
		}
		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("writing header for %s: %w", name, err)
		}
		_, err := tw.Write(content)
		return err
	}

	// Process Packages
	for _, pkg := range r.Packages {
		var buf bytes.Buffer
		if _, err := pkg.WriteTo(&buf); err != nil {
			return cw.n, fmt.Errorf("building package: %w", err)
		}
		content := buf.Bytes()

		rp, err := parseDeb(content, "")
		if err != nil {
			return cw.n, fmt.Errorf("parsing package: %w", err)
		}

		rp.Filename = fmt.Sprintf("%s_%s_%s.deb", rp.Package, rp.Version, rp.Architecture)
		if err := addFile(rp.Filename, content); err != nil {
			return cw.n, err
		}

		index = append(index, rp)
	}

	// 4. Generate Indices
	packagesContent := generatePackagesFile(index)
	if err := addFile("Packages", packagesContent); err != nil {
		return cw.n, err
	}

	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	gw.Write(packagesContent)
	gw.Close()
	packagesGzContent := gzBuf.Bytes()
	if err := addFile("Packages.gz", packagesGzContent); err != nil {
		return cw.n, err
	}

	releaseContent := generateReleaseFile(r.ArchiveInfo, packagesContent, packagesGzContent)
	if err := addFile("Release", releaseContent); err != nil {
		return cw.n, err
	}

	if r.GPGKey != "" {
		inRelease, err := signBytes(releaseContent, r.GPGKey)
		if err != nil {
			return cw.n, fmt.Errorf("signing InRelease: %w", err)
		}
		if err := addFile("InRelease", inRelease); err != nil {
			return cw.n, err
		}

		pubKey, err := extractPublicKey(r.GPGKey, false)
		if err == nil {
			if err := addFile("public.gpg", pubKey); err != nil {
				return cw.n, err
			}
		}
		pubKeyAsc, err := extractPublicKey(r.GPGKey, true)
		if err == nil {
			if err := addFile("public.asc", pubKeyAsc); err != nil {
				return cw.n, err
			}
		}
	}

	if err := tw.Close(); err != nil {
		return cw.n, err
	}
	return cw.n, nil
}

// WriteToDir generates the repository and writes it to the provided directory path.
func (r *Repository) WriteToDir(path string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return err
	}

	var index []*repoPackage

	// Process Packages
	for _, pkg := range r.Packages {
		var buf bytes.Buffer
		if _, err := pkg.WriteTo(&buf); err != nil {
			return fmt.Errorf("building package: %w", err)
		}
		content := buf.Bytes()

		rp, err := parseDeb(content, "")
		if err != nil {
			return fmt.Errorf("parsing package: %w", err)
		}

		rp.Filename = fmt.Sprintf("%s_%s_%s.deb", rp.Package, rp.Version, rp.Architecture)
		if err := os.WriteFile(filepath.Join(path, rp.Filename), content, 0644); err != nil {
			return err
		}

		index = append(index, rp)
	}

	// Generate Indices
	packagesContent := generatePackagesFile(index)
	if err := os.WriteFile(filepath.Join(path, "Packages"), packagesContent, 0644); err != nil {
		return err
	}

	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	gw.Write(packagesContent)
	gw.Close()
	packagesGzContent := gzBuf.Bytes()
	if err := os.WriteFile(filepath.Join(path, "Packages.gz"), packagesGzContent, 0644); err != nil {
		return err
	}

	releaseContent := generateReleaseFile(r.ArchiveInfo, packagesContent, packagesGzContent)
	if err := os.WriteFile(filepath.Join(path, "Release"), releaseContent, 0644); err != nil {
		return err
	}

	if r.GPGKey != "" {
		inRelease, err := signBytes(releaseContent, r.GPGKey)
		if err != nil {
			return fmt.Errorf("signing InRelease: %w", err)
		}
		if err := os.WriteFile(filepath.Join(path, "InRelease"), inRelease, 0644); err != nil {
			return err
		}

		pubKey, err := extractPublicKey(r.GPGKey, false)
		if err == nil {
			os.WriteFile(filepath.Join(path, "public.gpg"), pubKey, 0644)
		}
		pubKeyAsc, err := extractPublicKey(r.GPGKey, true)
		if err == nil {
			os.WriteFile(filepath.Join(path, "public.asc"), pubKeyAsc, 0644)
		}
	}

	return nil
}

// NewRepository creates a Repository from a tar.gz stream.
func NewRepository(r io.Reader) (*Repository, error) {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	repo := &Repository{
		Packages: []*Package{},
	}

	var externalPkgs []*Package

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch {
		case header.Name == "Release" || header.Name == "./Release":
			buf := new(bytes.Buffer)
			if _, err := io.Copy(buf, tr); err != nil {
				return nil, err
			}
			if err := parseReleaseFile(buf.String(), &repo.ArchiveInfo); err != nil {
				return nil, fmt.Errorf("parsing Release: %w", err)
			}
		case strings.HasSuffix(header.Name, ".deb"):
			pkg, err := NewPackage(tr)
			if err != nil {
				return nil, fmt.Errorf("parsing %s: %w", header.Name, err)
			}
			repo.Packages = append(repo.Packages, pkg)
		case header.Name == "Packages" || header.Name == "./Packages":
			buf := new(bytes.Buffer)
			if _, err := io.Copy(buf, tr); err != nil {
				return nil, err
			}
			pkgs, err := parsePackagesIndex(buf.String())
			if err != nil {
				return nil, fmt.Errorf("parsing Packages: %w", err)
			}
			externalPkgs = pkgs
		}
	}

	// Merge external packages
	existing := make(map[string]bool)
	for _, p := range repo.Packages {
		key := fmt.Sprintf("%s_%s_%s", p.Metadata.Package, p.Metadata.Version, p.Metadata.Architecture)
		existing[key] = true
	}

	for _, p := range externalPkgs {
		key := fmt.Sprintf("%s_%s_%s", p.Metadata.Package, p.Metadata.Version, p.Metadata.Architecture)
		if !existing[key] {
			repo.Packages = append(repo.Packages, p)
		}
	}

	return repo, nil
}

// NewRepositoryFromDir creates a Repository from a directory.
func NewRepositoryFromDir(path string) (*Repository, error) {
	repo := &Repository{
		Packages: []*Package{},
	}
	var externalPkgs []*Package

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		fullPath := filepath.Join(path, name)

		if name == "Release" {
			content, err := os.ReadFile(fullPath)
			if err != nil {
				return nil, err
			}
			if err := parseReleaseFile(string(content), &repo.ArchiveInfo); err != nil {
				return nil, fmt.Errorf("parsing Release: %w", err)
			}
		} else if strings.HasSuffix(name, ".deb") {
			f, err := os.Open(fullPath)
			if err != nil {
				return nil, err
			}
			pkg, err := NewPackage(f)
			f.Close()
			if err != nil {
				return nil, fmt.Errorf("parsing %s: %w", name, err)
			}
			repo.Packages = append(repo.Packages, pkg)
		} else if name == "Packages" {
			content, err := os.ReadFile(fullPath)
			if err != nil {
				return nil, err
			}
			pkgs, err := parsePackagesIndex(string(content))
			if err != nil {
				return nil, fmt.Errorf("parsing Packages: %w", err)
			}
			externalPkgs = pkgs
		}
	}

	// Merge external packages
	existing := make(map[string]bool)
	for _, p := range repo.Packages {
		key := fmt.Sprintf("%s_%s_%s", p.Metadata.Package, p.Metadata.Version, p.Metadata.Architecture)
		existing[key] = true
	}

	for _, p := range externalPkgs {
		key := fmt.Sprintf("%s_%s_%s", p.Metadata.Package, p.Metadata.Version, p.Metadata.Architecture)
		if !existing[key] {
			repo.Packages = append(repo.Packages, p)
		}
	}

	return repo, nil
}

// StandardRepository represents a hierarchical APT repository (dists/..., pool/...).
// It aggregates multiple Repositories, each representing a specific Component and Architecture.
type StandardRepository struct {
	ArchiveInfo ArchiveInfo
	GPGKey      string
	// Parts is a list of Repositories. Each Repository must have a single Architecture
	// and Component set in its ArchiveInfo.
	Parts []*Repository
}

type releaseFileEntry struct {
	Path string
	Size int64
	Hash string
}

// WriteTo generates the hierarchical repository and writes it as a tarball.
func (r *StandardRepository) WriteTo(w io.Writer) (int64, error) {
	cw := &countingWriter{w: w}
	tw := tar.NewWriter(cw)

	// Track files written to pool to avoid duplicates
	// Key: pool path (e.g., "pool/main/p/pkg/file.deb")
	poolFiles := make(map[string]bool)

	// Track generated indices for the top-level Release file
	var releaseEntries []releaseFileEntry

	// Helper to add file to tar
	addFile := func(name string, content []byte) error {
		header := &tar.Header{
			Name:    name,
			Size:    int64(len(content)),
			Mode:    0644,
			ModTime: time.Now(),
		}
		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("writing header for %s: %w", name, err)
		}
		_, err := tw.Write(content)
		return err
	}

	for _, part := range r.Parts {
		comp := part.ArchiveInfo.Components
		arch := part.ArchiveInfo.Architectures
		if comp == "" || arch == "" {
			return cw.n, fmt.Errorf("part missing component or architecture")
		}

		var index []*repoPackage

		for _, pkg := range part.Packages {
			var buf bytes.Buffer
			if _, err := pkg.WriteTo(&buf); err != nil {
				return cw.n, fmt.Errorf("building package: %w", err)
			}
			content := buf.Bytes()

			rp, err := parseDeb(content, "")
			if err != nil {
				return cw.n, fmt.Errorf("parsing package: %w", err)
			}

			pkgName := rp.Package
			if pkgName == "" {
				pkgName = "unknown"
			}
			poolPath := fmt.Sprintf("pool/%s/%s/%s", comp, pkgName, fmt.Sprintf("%s_%s_%s.deb", rp.Package, rp.Version, rp.Architecture))

			if !poolFiles[poolPath] {
				if err := addFile(poolPath, content); err != nil {
					return cw.n, err
				}
				poolFiles[poolPath] = true
			}
			rp.Filename = poolPath
			index = append(index, rp)
		}

		// Generate Indices
		packagesContent := generatePackagesFile(index)

		// Path in tar: dists/<Codename>/<Component>/binary-<Arch>/Packages
		relDir := fmt.Sprintf("%s/binary-%s", comp, arch)
		packagesPath := fmt.Sprintf("dists/%s/%s/Packages", r.ArchiveInfo.Codename, relDir)

		if err := addFile(packagesPath, packagesContent); err != nil {
			return cw.n, err
		}

		hash := sha256.Sum256(packagesContent)
		releaseEntries = append(releaseEntries, releaseFileEntry{
			Path: fmt.Sprintf("%s/Packages", relDir),
			Size: int64(len(packagesContent)),
			Hash: hex.EncodeToString(hash[:]),
		})

		// Packages.gz
		var gzBuf bytes.Buffer
		gw := gzip.NewWriter(&gzBuf)
		gw.Write(packagesContent)
		gw.Close()
		packagesGzContent := gzBuf.Bytes()

		packagesGzPath := fmt.Sprintf("dists/%s/%s/Packages.gz", r.ArchiveInfo.Codename, relDir)
		if err := addFile(packagesGzPath, packagesGzContent); err != nil {
			return cw.n, err
		}

		hashGz := sha256.Sum256(packagesGzContent)
		releaseEntries = append(releaseEntries, releaseFileEntry{
			Path: fmt.Sprintf("%s/Packages.gz", relDir),
			Size: int64(len(packagesGzContent)),
			Hash: hex.EncodeToString(hashGz[:]),
		})
	}

	// Generate Top-Level Release
	releaseContent := generateHierarchicalRelease(r.ArchiveInfo, releaseEntries)
	releasePath := fmt.Sprintf("dists/%s/Release", r.ArchiveInfo.Codename)
	if err := addFile(releasePath, releaseContent); err != nil {
		return cw.n, err
	}

	if r.GPGKey != "" {
		inRelease, err := signBytes(releaseContent, r.GPGKey)
		if err != nil {
			return cw.n, fmt.Errorf("signing InRelease: %w", err)
		}
		if err := addFile(fmt.Sprintf("dists/%s/InRelease", r.ArchiveInfo.Codename), inRelease); err != nil {
			return cw.n, err
		}

		pubKey, err := extractPublicKey(r.GPGKey, false)
		if err == nil {
			addFile("public.gpg", pubKey)
		}
		pubKeyAsc, err := extractPublicKey(r.GPGKey, true)
		if err == nil {
			addFile("public.asc", pubKeyAsc)
		}
	}

	if err := tw.Close(); err != nil {
		return cw.n, err
	}
	return cw.n, nil
}
