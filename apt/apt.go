package apt

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
)

// RepoConfig defines a source APT repository to harvest packages from.
// It supports both:
// 1. Flat Repositories: Just a URL (Suite is empty).
// 2. Standard Repositories: URL + Suite + Component + Architectures (e.g., deb http://archive.ubuntu.com/ubuntu focal main).
type RepoConfig struct {
	URL           string
	Suite         string
	Component     string
	Architectures []string
}

// ArchiveInfo holds metadata about the repository itself.
// These fields are written to the 'Release' file and help APT clients identify
// the repository (e.g., for pinning or trust).
type ArchiveInfo struct {
	Origin        string
	Label         string
	Suite         string
	Codename      string
	Architectures string
	Components    string
	Description   string
}

// CachedAsset represents the metadata of a .deb file stored in the local cache.
// This avoids downloading and re-parsing large .deb files if we have seen them before.
type CachedAsset struct {
	ContentHash string // Payload Hash (debian-binary + control + data)
	FileHash    string // SHA256 of the .deb file
	Size        int64
	Control     string
	URL         string
}

// Package represents the metadata for a single .deb package version.
// It combines data from the package's internal 'control' file with
// repository-level metadata like the download URL and file hashes.
type Package struct {
	Name         string
	Version      string
	Architecture string
	// Control is the raw text block from the package's control file.
	// It contains fields like Package, Version, Depends, Description.
	Control string

	// Filename is the relative path or absolute URL to the .deb file.
	// In a repository index, this tells APT where to download the file.
	Filename string
	Size     int64
	FileHash string // SHA256

	// contentHash is a custom hash of the package payload (debian-binary + control.tar + data.tar).
	// It ignores ar archive headers (timestamps, UID/GID) to ensure reproducible builds
	// produce the same hash. It is NOT serialized to the Packages file.
	contentHash string
}

// ContentHash returns the payload-based hash of the package.
// If not already cached, it downloads/reads the file and computes it.
// This is used to verify that a package's logical content hasn't changed,
// enforcing immutability even if the file is rebuilt.
func (p *Package) ContentHash() (string, error) {
	if p.contentHash != "" {
		return p.contentHash, nil
	}

	var r io.Reader
	if strings.HasPrefix(p.Filename, "http") || strings.HasPrefix(p.Filename, "https") {
		resp, err := http.Get(p.Filename)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("failed to fetch %s: status %d", p.Filename, resp.StatusCode)
		}
		r = resp.Body
	} else {
		f, err := os.Open(p.Filename)
		if err != nil {
			return "", err
		}
		defer f.Close()
		r = f
	}

	// We need a seekable file for CalculateHashes (ar parsing)
	tmp, err := os.CreateTemp("", "hash-check-*.deb")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())

	if _, err := io.Copy(tmp, r); err != nil {
		return "", err
	}
	tmp.Close()

	_, ch, err := CalculateHashes(tmp.Name())
	if err != nil {
		return "", err
	}
	p.contentHash = ch
	return ch, nil
}

// PackageIndex is an in-memory database of packages.
// It serves as the staging area for generating the 'Packages' file.
// It enforces uniqueness based on "Name|Version|Architecture".
type PackageIndex struct {
	packages map[string]*Package // Key: Name|Version|Architecture

	PackagesContent         []byte
	PackagesGzContent       []byte
	ReleaseContent          []byte
	InReleaseContent        []byte
	PublicKeyContent        []byte
	PublicKeyContentArmored []byte
}

func NewPackageIndex() *PackageIndex {
	return &PackageIndex{packages: make(map[string]*Package)}
}

// Add inserts a package into the index.
// It returns an error if a package with the same Name, Version, and Architecture already exists.
func (idx *PackageIndex) Add(p *Package) error {
	if p.Name == "" || p.Version == "" || p.Architecture == "" {
		p.Name, p.Version, p.Architecture = parseControlMetadata(p.Control)
	}
	id := fmt.Sprintf("%s|%s|%s", p.Name, p.Version, p.Architecture)
	if p.Name != "" {
		if _, exists := idx.packages[id]; exists {
			return fmt.Errorf("duplicate package: %s", id)
		}
		idx.packages[id] = p
	}
	return nil
}

// Append merges another index into this one.
// Useful for aggregating packages from multiple sources (e.g., multiple GitHub repos + upstream Ubuntu).
func (idx *PackageIndex) Append(other *PackageIndex) error {
	for id, p := range other.packages {
		if _, exists := idx.packages[id]; exists {
			return fmt.Errorf("duplicate package: %s", id)
		}
		idx.packages[id] = p
	}
	return nil
}

// FetchPackageIndexFrom downloads and parses the 'Packages' index from a remote APT repository.
// It handles the logic for constructing URLs for both flat and hierarchical repository layouts.
func FetchPackageIndexFrom(r RepoConfig, cache map[string]CachedAsset) (*PackageIndex, error) {
	idx := NewPackageIndex()
	fmt.Printf("Harvesting %s...\n", r.URL)
	baseURL := r.URL
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}

	var urls []string
	if r.Suite == "" {
		// Flat repository
		urls = append(urls, baseURL+"Packages.gz")
	} else {
		// Hierarchical repository
		if len(r.Architectures) == 0 {
			return nil, fmt.Errorf("architectures required for suite %s", r.Suite)
		}
		for _, arch := range r.Architectures {
			// Standard layout: dists/<suite>/<component>/binary-<arch>/Packages.gz
			u := fmt.Sprintf("%sdists/%s/%s/binary-%s/Packages.gz", baseURL, r.Suite, r.Component, arch)
			urls = append(urls, u)
		}
	}

	for _, u := range urls {
		if err := processRemotePackages(u, baseURL, idx, cache); err != nil {
			fmt.Printf("    Warning: Failed to process %s: %v\n", u, err)
		}
	}
	return idx, nil
}

// fetchPackageIndexFromDebs creates an index from a list of raw .deb URLs.
func fetchPackageIndexFromDebs(urls []string, cache map[string]CachedAsset) (*PackageIndex, error) {
	idx := NewPackageIndex()
	for _, url := range urls {
		fmt.Printf("  Processing %s\n", filepath.Base(url))
		pkg, err := fetchPackageFrom(url, cache)
		if err != nil {
			fmt.Printf("    Error: %v\n", err)
			continue
		}
		if err := idx.Add(pkg); err != nil {
			fmt.Printf("    Error adding package: %v\n", err)
			continue
		}
	}
	return idx, nil
}

// processRemotePackages parses a 'Packages' text file (or gzipped stream).
// It extracts stanzas, rewrites relative filenames to absolute URLs (if needed),
// and adds them to the index.
func processRemotePackages(url, baseURL string, idx *PackageIndex, cache map[string]CachedAsset) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}

	var r io.Reader = resp.Body
	if strings.HasSuffix(url, ".gz") {
		gzr, err := gzip.NewReader(r)
		if err != nil {
			return err
		}
		defer gzr.Close()
		r = gzr
	}

	scanner := bufio.NewScanner(r)
	// Increase buffer for long lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var currentStanza strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			if currentStanza.Len() > 0 {
				p := parseStanza(currentStanza.String())
				// Rewrite relative filename to absolute URL
				if !strings.HasPrefix(p.Filename, "http") {
					p.Filename = baseURL + p.Filename
				}
				if cached, ok := cache[p.Filename]; ok {
					p.contentHash = cached.ContentHash
				}
				if err := idx.Add(p); err != nil {
					return err
				}
				currentStanza.Reset()
			}
			continue
		}
		currentStanza.WriteString(line + "\n")
	}
	// Flush last
	if currentStanza.Len() > 0 {
		p := parseStanza(currentStanza.String())
		if !strings.HasPrefix(p.Filename, "http") {
			p.Filename = baseURL + p.Filename
		}
		if cached, ok := cache[p.Filename]; ok {
			p.contentHash = cached.ContentHash
		}
		if err := idx.Add(p); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// fetchPackageFrom downloads a raw .deb file, extracts its metadata, and creates a Package object.
// This is used when we are indexing loose .deb files (e.g. from GitHub Releases) rather than
// reading an existing Packages index.
func fetchPackageFrom(url string, cache map[string]CachedAsset) (*Package, error) {
	if cached, ok := cache[url]; ok {
		p := &Package{
			Filename:    url,
			Control:     cached.Control,
			FileHash:    cached.FileHash,
			Size:        cached.Size,
			contentHash: cached.ContentHash,
		}
		p.Name, p.Version, p.Architecture = parseControlMetadata(p.Control)
		return p, nil
	}

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	tmp, err := os.CreateTemp("", "pkg-*.deb")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())

	size, err := io.Copy(tmp, resp.Body)
	if err != nil {
		return nil, err
	}
	tmp.Close()

	fileHash, contentHash, err := CalculateHashes(tmp.Name())
	if err != nil {
		return nil, err
	}

	control, err := extractControl(tmp.Name())

	cache[url] = CachedAsset{FileHash: fileHash, ContentHash: contentHash, Size: size, Control: control, URL: url}

	p := &Package{
		Filename:    url,
		Control:     control,
		FileHash:    fileHash,
		Size:        size,
		contentHash: contentHash,
	}
	p.Name, p.Version, p.Architecture = parseControlMetadata(p.Control)
	return p, nil
}

func generateStanzaString(control, filename, sha string, size int64) string {
	var b strings.Builder
	b.WriteString(control)
	if !strings.HasSuffix(control, "\n") {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Filename: %s\nSize: %d\nSHA256: %s\n\n", filename, size, sha)
	return b.String()
}

// CalculateHashes computes two SHA256 hashes for a .deb file:
// 1. FileHash: Standard SHA256 of the entire file (for integrity).
// 2. ContentHash: SHA256 of the payload members (debian-binary, control.tar, data.tar).
// The ContentHash is used for immutability checks, ignoring archive creation timestamps.
func CalculateHashes(path string) (fileHash string, contentHash string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	// File Hash
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", "", err
	}
	fileHash = hex.EncodeToString(h.Sum(nil))

	// Content Hash (Payload)
	f.Seek(0, 0)
	ch := sha256.New()

	// Iterate AR archive
	// AR header is 8 bytes "!<arch>\n"
	magic := make([]byte, 8)
	f.Read(magic)

	for {
		header := make([]byte, 60)
		if _, err := io.ReadFull(f, header); err != nil {
			break // EOF
		}
		// name := strings.TrimSpace(string(header[0:16]))
		sizeStr := strings.TrimSpace(string(header[48:58]))
		size, _ := strconv.ParseInt(sizeStr, 10, 64)

		// We hash the body of every entry (debian-binary, control, data)
		io.CopyN(ch, f, size)

		if size%2 != 0 {
			f.Seek(1, io.SeekCurrent)
		} // Pad
	}
	contentHash = hex.EncodeToString(ch.Sum(nil))
	return
}

func parseControlMetadata(control string) (string, string, string) {
	var p, v, a string
	scanner := bufio.NewScanner(strings.NewReader(control))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Package: ") {
			p = strings.TrimSpace(strings.TrimPrefix(line, "Package: "))
		} else if strings.HasPrefix(line, "Version: ") {
			v = strings.TrimSpace(strings.TrimPrefix(line, "Version: "))
		} else if strings.HasPrefix(line, "Architecture: ") {
			a = strings.TrimSpace(strings.TrimPrefix(line, "Architecture: "))
		}
	}
	return p, v, a
}

func parseStanza(stanza string) *Package {
	p := &Package{}
	var controlLines []string
	scanner := bufio.NewScanner(strings.NewReader(stanza))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Filename: ") {
			p.Filename = strings.TrimSpace(strings.TrimPrefix(line, "Filename: "))
		} else if strings.HasPrefix(line, "Size: ") {
			fmt.Sscanf(strings.TrimPrefix(line, "Size: "), "%d", &p.Size)
		} else if strings.HasPrefix(line, "SHA256: ") {
			p.FileHash = strings.TrimSpace(strings.TrimPrefix(line, "SHA256: "))
		} else {
			controlLines = append(controlLines, line)
		}
	}
	p.Control = strings.Join(controlLines, "\n") + "\n"
	p.Name, p.Version, p.Architecture = parseControlMetadata(p.Control)
	return p
}

func extractControl(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	magic := make([]byte, 8)
	if _, err := f.Read(magic); err != nil || string(magic) != "!<arch>\n" {
		return "", fmt.Errorf("not a debian archive")
	}

	for {
		header := make([]byte, 60)
		if _, err := io.ReadFull(f, header); err != nil {
			if err == io.EOF {
				break
			}
			return "", err
		}

		name := strings.TrimSpace(string(header[0:16]))
		sizeStr := strings.TrimSpace(string(header[48:58]))
		size, _ := strconv.ParseInt(sizeStr, 10, 64)

		if strings.HasPrefix(name, "control.tar") {
			limited := io.LimitReader(f, size)
			var tr *tar.Reader

			if strings.HasSuffix(name, ".gz") || strings.HasSuffix(name, ".gz/") {
				gzr, err := gzip.NewReader(limited)
				if err != nil {
					return "", err
				}
				defer gzr.Close()
				tr = tar.NewReader(gzr)
			} else {
				return "", fmt.Errorf("unsupported compression: %s (need .gz)", name)
			}

			for {
				th, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					return "", err
				}
				if filepath.Base(th.Name) == "control" {
					var buf bytes.Buffer
					io.Copy(&buf, tr)
					return buf.String(), nil
				}
			}
		}

		if size%2 != 0 {
			size++
		}
		f.Seek(size, io.SeekCurrent)
	}
	return "", fmt.Errorf("control file missing")
}

// ComputeIndices generates the standard APT repository metadata files in memory.
// 1. Packages: The text index of all packages.
// 2. Packages.gz: Compressed index.
// 3. Release: Metadata about the repository and hashes of the indices.
// 4. InRelease: GPG-signed version of the Release file.
func (idx *PackageIndex) ComputeIndices(i ArchiveInfo, gpgKey string) error {
	// 1. Generate Packages
	var pkgBuf bytes.Buffer
	for _, p := range idx.packages {
		fmt.Fprint(&pkgBuf, generateStanzaString(p.Control, p.Filename, p.FileHash, p.Size))
	}
	idx.PackagesContent = pkgBuf.Bytes()

	// 2. Generate Packages.gz
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	gw.Write(idx.PackagesContent)
	gw.Close()
	idx.PackagesGzContent = gzBuf.Bytes()

	// 3. Generate Release
	var relBuf bytes.Buffer
	fmt.Fprintf(&relBuf, "Origin: %s\nLabel: %s\nSuite: %s\nCodename: %s\nDate: %s\nArchitectures: %s\nComponents: %s\nDescription: %s\nSHA256:\n",
		i.Origin, i.Label, i.Suite, i.Codename, time.Now().UTC().Format(time.RFC1123Z), i.Architectures, i.Components, i.Description)

	// Hash Packages
	hPkg := sha256.Sum256(idx.PackagesContent)
	fmt.Fprintf(&relBuf, " %x %d %s\n", hPkg, len(idx.PackagesContent), "Packages")

	// Hash Packages.gz
	hGz := sha256.Sum256(idx.PackagesGzContent)
	fmt.Fprintf(&relBuf, " %x %d %s\n", hGz, len(idx.PackagesGzContent), "Packages.gz")

	idx.ReleaseContent = relBuf.Bytes()

	// 4. Sign (InRelease)
	if gpgKey != "" {
		signed, err := signBytes(idx.ReleaseContent, gpgKey)
		if err != nil {
			return fmt.Errorf("signing failed: %w", err)
		}
		idx.InReleaseContent = signed
		pubKey, err := extractPublicKey(gpgKey, false)
		if err != nil {
			return fmt.Errorf("failed to extract public key: %w", err)
		}
		idx.PublicKeyContent = pubKey

		pubKeyArmored, err := extractPublicKey(gpgKey, true)
		if err != nil {
			return fmt.Errorf("failed to extract armored public key: %w", err)
		}
		idx.PublicKeyContentArmored = pubKeyArmored

	}
	return nil
}

// IndexAll is the high-level aggregator.
// It fetches indices from standard repos and individual .deb files from URLs,
// merges them into a master index, and computes the final repository metadata.
func IndexAll(repos []RepoConfig, debURLs []string, cache map[string]CachedAsset, info ArchiveInfo, gpgKey string) (*PackageIndex, error) {
	masterIndex := NewPackageIndex()

	// 1. Harvest Standard Repositories
	for _, r := range repos {
		idx, err := FetchPackageIndexFrom(r, cache)
		if err != nil {
			fmt.Printf("Error harvesting %s: %v\n", r.URL, err)
			continue
		}
		if err := masterIndex.Append(idx); err != nil {
			return nil, fmt.Errorf("failed to append repository %s: %w", r.URL, err)
		}
	}

	// 2. Process Artifact URLs
	if len(debURLs) > 0 {
		idx, err := fetchPackageIndexFromDebs(debURLs, cache)
		if err != nil {
			fmt.Printf("Error processing artifacts: %v\n", err)
		} else {
			if err := masterIndex.Append(idx); err != nil {
				return nil, fmt.Errorf("failed to append artifacts: %w", err)
			}
		}
	}

	// 3. Compute Indices
	if err := masterIndex.ComputeIndices(info, gpgKey); err != nil {
		return nil, fmt.Errorf("failed to compute indices: %w", err)
	}

	return masterIndex, nil
}

// ConflictFree checks if a local .deb file is safe to upload.
// It verifies that if the version already exists in the master index, the content is identical.
// This enforces the "Immutability Principle": you cannot overwrite a version with different code.
func ConflictFree(path string, masterIndex *PackageIndex) (*Package, bool, error) {
	fileHash, contentHash, err := CalculateHashes(path)
	if err != nil {
		return nil, false, fmt.Errorf("invalid file: %w", err)
	}

	control, err := extractControl(path)
	if err != nil {
		return nil, false, fmt.Errorf("no control: %w", err)
	}

	p, v, a := parseControlMetadata(control)
	id := fmt.Sprintf("%s|%s|%s", p, v, a)

	stat, _ := os.Stat(path)
	pkg := &Package{
		Name:         p,
		Version:      v,
		Architecture: a,
		Control:      control,
		Filename:     filepath.Base(path),
		Size:         stat.Size(),
		FileHash:     fileHash,
		contentHash:  contentHash,
	}

	// Validate against Master Index (Config)
	if masterPkg, exists := masterIndex.packages[id]; exists {
		masterContentHash, err := masterPkg.ContentHash()
		if err != nil {
			return pkg, false, fmt.Errorf("could not verify master package %s: %w", id, err)
		} else if masterContentHash != contentHash {
			return pkg, false, fmt.Errorf("version conflict for %s %s (%s). Master hash differs", p, v, a)
		}
	}

	return pkg, true, nil
}

// SaveTo writes the generated index files (Packages, Release, etc.) to a local directory.
func (idx *PackageIndex) SaveTo(outputDir string) error {
	if len(idx.PackagesContent) == 0 {
		return fmt.Errorf("indices not computed")
	}
	os.WriteFile(filepath.Join(outputDir, "Packages"), idx.PackagesContent, 0644)
	os.WriteFile(filepath.Join(outputDir, "Packages.gz"), idx.PackagesGzContent, 0644)
	os.WriteFile(filepath.Join(outputDir, "Release"), idx.ReleaseContent, 0644)
	if len(idx.InReleaseContent) > 0 {
		os.WriteFile(filepath.Join(outputDir, "InRelease"), idx.InReleaseContent, 0644)
	}
	if len(idx.PublicKeyContent) > 0 {
		os.WriteFile(filepath.Join(outputDir, "public.gpg"), idx.PublicKeyContent, 0644)
	}
	if len(idx.PublicKeyContentArmored) > 0 {
		os.WriteFile(filepath.Join(outputDir, "public.asc"), idx.PublicKeyContentArmored, 0644)
	}
	return nil
}

func signBytes(input []byte, key string) ([]byte, error) {
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(key))
	if err != nil {
		return nil, err
	}
	var signer *openpgp.Entity
	for _, e := range entities {
		if e.PrivateKey != nil {
			signer = e
			break
		}
	}
	if signer == nil {
		return nil, fmt.Errorf("no private key")
	}

	var out bytes.Buffer
	w, err := clearsign.Encode(&out, signer.PrivateKey, nil)
	if err != nil {
		return nil, err
	}
	w.Write(input)
	w.Close()
	return out.Bytes(), nil
}

func extractPublicKey(key string, armored bool) ([]byte, error) {
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(key))
	if err != nil {
		return nil, err
	}
	var signer *openpgp.Entity
	for _, e := range entities {
		if e.PrivateKey != nil {
			signer = e
			break
		}
	}
	if signer == nil {
		return nil, fmt.Errorf("no private key found")
	}

	var buf bytes.Buffer
	if armored {
		w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
		if err != nil {
			return nil, err
		}
		if err := signer.Serialize(w); err != nil {
			return nil, err
		}
		w.Close()
	} else {
		if err := signer.Serialize(&buf); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}
