package apt

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
)

// Helper to create a mock .deb file with minimal valid structure
func createMockDeb(t *testing.T, controlContent string) string {
	f, err := os.CreateTemp("", "test*.deb")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// AR Header
	f.WriteString("!<arch>\n")

	writeEntry := func(name string, data []byte) {
		// Header: name(16) timestamp(12) owner(6) group(6) mode(8) size(10) end(2)
		// Note: AR format requires padding name to 16 bytes, and size is decimal.
		header := fmt.Sprintf("%-16s%-12s%-6s%-6s%-8s%-10d`\n", name, "0", "0", "0", "100644", len(data))
		f.WriteString(header)
		f.Write(data)
		if len(data)%2 != 0 {
			f.WriteString("\n")
		}
	}

	// 1. debian-binary
	writeEntry("debian-binary", []byte("2.0\n"))

	// 2. control.tar.gz
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	hdr := &tar.Header{
		Name: "control",
		Mode: 0644,
		Size: int64(len(controlContent)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(controlContent)); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gw.Close()
	writeEntry("control.tar.gz", buf.Bytes())

	// 3. data.tar.gz
	writeEntry("data.tar.gz", []byte("dummy data"))

	return f.Name()
}

func TestPackageIndex_Add(t *testing.T) {
	idx := NewPackageIndex()
	p := &Package{
		Name:         "test-pkg",
		Version:      "1.0.0",
		Architecture: "amd64",
		Control:      "Package: test-pkg\nVersion: 1.0.0\nArchitecture: amd64\n",
	}

	if err := idx.Add(p); err != nil {
		t.Errorf("Add failed: %v", err)
	}
	if len(idx.packages) != 1 {
		t.Errorf("Expected 1 package, got %d", len(idx.packages))
	}

	// Duplicate add
	if err := idx.Add(p); err == nil {
		t.Error("Expected error on duplicate add, got nil")
	}
}

func TestPackageIndex_Append(t *testing.T) {
	idx1 := NewPackageIndex()
	idx1.Add(&Package{Name: "p1", Version: "1.0", Architecture: "all", Control: "Package: p1\nVersion: 1.0\nArchitecture: all\n"})

	idx2 := NewPackageIndex()
	idx2.Add(&Package{Name: "p2", Version: "1.0", Architecture: "all", Control: "Package: p2\nVersion: 1.0\nArchitecture: all\n"})

	if err := idx1.Append(idx2); err != nil {
		t.Errorf("Append failed: %v", err)
	}
	if len(idx1.packages) != 2 {
		t.Errorf("Expected 2 packages, got %d", len(idx1.packages))
	}

	// Duplicate append
	idx3 := NewPackageIndex()
	idx3.Add(&Package{Name: "p1", Version: "1.0", Architecture: "all", Control: "Package: p1\nVersion: 1.0\nArchitecture: all\n"})
	if err := idx1.Append(idx3); err == nil {
		t.Error("Expected error on duplicate append, got nil")
	}
}

func TestCalculateHashes_And_ExtractControl(t *testing.T) {
	control := "Package: test\nVersion: 1.0\nArchitecture: amd64\n"
	path := createMockDeb(t, control)
	defer os.Remove(path)

	fileHash, contentHash, err := CalculateHashes(path)
	if err != nil {
		t.Fatalf("CalculateHashes failed: %v", err)
	}
	if fileHash == "" || contentHash == "" {
		t.Error("Hashes are empty")
	}

	extractedControl, err := extractControl(path)
	if err != nil {
		t.Fatalf("extractControl failed: %v", err)
	}
	if extractedControl != control {
		t.Errorf("Control mismatch. Got %q, want %q", extractedControl, control)
	}
}

func TestFetchPackageIndexFrom(t *testing.T) {
	// Mock server serving Packages.gz
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "Packages.gz") {
			gw := gzip.NewWriter(w)
			fmt.Fprint(gw, "Package: remote-pkg\nVersion: 1.0\nArchitecture: amd64\nFilename: pool/main/r/remote-pkg.deb\nSHA256: dummyhash\nSize: 100\n\n")
			gw.Close()
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	repo := RepoConfig{
		URL:           ts.URL,
		Suite:         "stable",
		Component:     "main",
		Architectures: []string{"amd64"},
	}

	cache := make(map[string]CachedAsset)
	idx, err := FetchPackageIndexFrom(repo, cache)
	if err != nil {
		t.Fatalf("FetchPackageIndexFrom failed: %v", err)
	}

	if len(idx.packages) != 1 {
		t.Errorf("Expected 1 package, got %d", len(idx.packages))
	}

	// Verify URL rewriting
	for _, p := range idx.packages {
		expectedURL := ts.URL + "/pool/main/r/remote-pkg.deb"
		if p.Filename != expectedURL {
			t.Errorf("Expected filename %s, got %s", expectedURL, p.Filename)
		}
	}
}

func TestFetchPackageIndexFromDebs(t *testing.T) {
	control := "Package: deb-pkg\nVersion: 1.0\nArchitecture: amd64\n"
	debPath := createMockDeb(t, control)
	defer os.Remove(debPath)
	debContent, _ := os.ReadFile(debPath)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(debContent)
	}))
	defer ts.Close()

	urls := []string{ts.URL + "/test.deb"}
	cache := make(map[string]CachedAsset)

	idx, err := fetchPackageIndexFromDebs(urls, cache)
	if err != nil {
		t.Fatalf("fetchPackageIndexFromDebs failed: %v", err)
	}
	if len(idx.packages) != 1 {
		t.Errorf("Expected 1 package, got %d", len(idx.packages))
	}

	// Check cache population
	if _, ok := cache[urls[0]]; !ok {
		t.Error("Cache not populated")
	}
}

func TestComputeIndices(t *testing.T) {
	idx := NewPackageIndex()
	idx.Add(&Package{
		Name: "p1", Version: "1.0", Architecture: "all",
		Control:  "Package: p1\nVersion: 1.0\nArchitecture: all\n",
		Filename: "http://example.com/p1.deb",
		Size:     100,
		FileHash: "abc",
	})

	info := ArchiveInfo{
		Origin:   "Test",
		Label:    "TestRepo",
		Codename: "stable",
	}

	// Test without GPG
	if err := idx.ComputeIndices(info, ""); err != nil {
		t.Fatalf("ComputeIndices failed: %v", err)
	}
	if len(idx.PackagesContent) == 0 || len(idx.ReleaseContent) == 0 {
		t.Error("Generated content is empty")
	}
	if len(idx.InReleaseContent) != 0 {
		t.Error("InRelease should be empty without key")
	}

	// Test with GPG
	entity, err := openpgp.NewEntity("Test User", "test", "test@example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	var keyBuf bytes.Buffer
	w, _ := armor.Encode(&keyBuf, openpgp.PrivateKeyType, nil)
	entity.SerializePrivate(w, nil)
	w.Close()
	privKey := keyBuf.String()

	if err := idx.ComputeIndices(info, privKey); err != nil {
		t.Fatalf("ComputeIndices with key failed: %v", err)
	}
	if len(idx.InReleaseContent) == 0 {
		t.Error("InRelease should not be empty with key")
	}
	if len(idx.PublicKeyContent) == 0 {
		t.Error("PublicKeyContent should not be empty with key")
	}
}

func TestConflictFree(t *testing.T) {
	control := "Package: conflict-test\nVersion: 1.0\nArchitecture: amd64\n"
	path := createMockDeb(t, control)
	defer os.Remove(path)

	masterIdx := NewPackageIndex()

	// Case 1: New package (not in master)
	pkg, ok, err := ConflictFree(path, masterIdx)
	if err != nil {
		t.Errorf("ConflictFree failed for new pkg: %v", err)
	}
	if !ok {
		t.Error("Expected ok=true for new pkg")
	}
	if pkg == nil {
		t.Error("Expected pkg to be returned")
	}

	// Case 2: Existing package, same content
	masterIdx.Add(pkg)
	pkg2, ok, err := ConflictFree(path, masterIdx)
	if err != nil {
		t.Errorf("ConflictFree failed for existing pkg: %v", err)
	}
	if !ok {
		t.Error("Expected ok=true for existing pkg with same content")
	}
	if pkg2 == nil {
		t.Error("Expected pkg to be returned")
	}

	// Case 3: Existing package, different content
	// Manually corrupt the content hash in master index to simulate conflict
	pkg.contentHash = "corrupted"

	_, ok, err = ConflictFree(path, masterIdx)
	if err == nil {
		t.Error("Expected error for conflict")
	}
	if ok {
		t.Error("Expected ok=false for conflict")
	}
}

func TestParseControlMetadata(t *testing.T) {
	c := "Package: foo\nVersion: 1.0\nArchitecture: amd64\n"
	p, v, a := parseControlMetadata(c)
	if p != "foo" || v != "1.0" || a != "amd64" {
		t.Errorf("Parse failed: %s %s %s", p, v, a)
	}
}

func TestPackageIndex_SaveTo(t *testing.T) {
	idx := NewPackageIndex()
	idx.PackagesContent = []byte("pkg")
	idx.PackagesGzContent = []byte("gz")
	idx.ReleaseContent = []byte("rel")
	idx.PublicKeyContent = []byte("pub")

	tmpDir := t.TempDir()
	if err := idx.SaveTo(tmpDir); err != nil {
		t.Fatalf("SaveTo failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "Packages")); os.IsNotExist(err) {
		t.Error("Packages not created")
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "Packages.gz")); os.IsNotExist(err) {
		t.Error("Packages.gz not created")
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "Release")); os.IsNotExist(err) {
		t.Error("Release not created")
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "public.key")); os.IsNotExist(err) {
		t.Error("public.key not created")
	}
}

func TestGenerateStanzaString(t *testing.T) {
	control := "Package: foo\nVersion: 1.0\n"
	s := generateStanzaString(control, "http://url", "hash", 123)
	expected := "Package: foo\nVersion: 1.0\nFilename: http://url\nSize: 123\nSHA256: hash\n\n"
	if s != expected {
		t.Errorf("Stanza mismatch.\nGot:\n%q\nWant:\n%q", s, expected)
	}
}
