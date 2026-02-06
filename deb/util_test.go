package deb

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/blakesmith/ar"
)

func TestCountingWriter(t *testing.T) {
	var buf bytes.Buffer
	cw := &countingWriter{w: &buf}

	data := []byte("hello")
	n, err := cw.Write(data)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes written, got %d", n)
	}
	if cw.n != 5 {
		t.Errorf("expected count 5, got %d", cw.n)
	}
	if buf.String() != "hello" {
		t.Errorf("buffer mismatch")
	}
}

func TestAddBufferToAr(t *testing.T) {
	var buf bytes.Buffer
	arW := ar.NewWriter(&buf)
	// Write global header first as required by AR format
	if err := arW.WriteGlobalHeader(); err != nil {
		t.Fatalf("WriteGlobalHeader failed: %v", err)
	}

	content := []byte("content")
	if err := addBufferToAr(arW, "test.txt", content); err != nil {
		t.Fatalf("addBufferToAr failed: %v", err)
	}

	// Verify
	arR := ar.NewReader(&buf)
	hdr, err := arR.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if hdr.Name != "test.txt" {
		t.Errorf("expected name test.txt, got %s", hdr.Name)
	}
	if hdr.Size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), hdr.Size)
	}
}

// Helper to create a mock .deb byte slice
func createMockDebBytes(t *testing.T, controlContent string) []byte {
	t.Helper()
	var buf bytes.Buffer
	arW := ar.NewWriter(&buf)
	arW.WriteGlobalHeader()

	// debian-binary
	addBufferToAr(arW, string(PkgDebianBinary), []byte("2.0\n"))

	// control.tar.gz
	var cBuf bytes.Buffer
	gw := gzip.NewWriter(&cBuf)
	tw := tar.NewWriter(gw)
	hdr := &tar.Header{
		Name: "control",
		Mode: 0644,
		Size: int64(len(controlContent)),
	}
	tw.WriteHeader(hdr)
	tw.Write([]byte(controlContent))
	tw.Close()
	gw.Close()
	addBufferToAr(arW, string(PkgControlTarGz), cBuf.Bytes())

	return buf.Bytes()
}

func TestParseDeb(t *testing.T) {
	control := "Package: test\nVersion: 1.0\nArchitecture: amd64\n"
	debBytes := createMockDebBytes(t, control)

	pkg, err := parseDeb(debBytes, "test.deb")
	if err != nil {
		t.Fatalf("parseDeb failed: %v", err)
	}

	if pkg.Package != "test" {
		t.Errorf("expected package test, got %s", pkg.Package)
	}
	if pkg.Version != "1.0" {
		t.Errorf("expected version 1.0, got %s", pkg.Version)
	}
	if pkg.Filename != "test.deb" {
		t.Errorf("expected filename test.deb, got %s", pkg.Filename)
	}
	if pkg.Size != int64(len(debBytes)) {
		t.Errorf("expected size %d, got %d", len(debBytes), pkg.Size)
	}

	hash := sha256.Sum256(debBytes)
	expectedHash := hex.EncodeToString(hash[:])
	if pkg.SHA256 != expectedHash {
		t.Errorf("hash mismatch")
	}
}

func TestExtractControlFromBytes(t *testing.T) {
	expected := "Package: foo\n"
	debBytes := createMockDebBytes(t, expected)

	got, err := extractControlFromBytes(debBytes)
	if err != nil {
		t.Fatalf("extractControlFromBytes failed: %v", err)
	}
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestParseControlFields(t *testing.T) {
	c := "Package: p\nVersion: v\nArchitecture: a\nDescription: d\n"
	p, v, a := parseControlFields(c)
	if p != "p" || v != "v" || a != "a" {
		t.Errorf("parse failed: %s %s %s", p, v, a)
	}
}

func TestGeneratePackagesFile(t *testing.T) {
	pkgs := []*repoPackage{
		{
			Control:  "Package: a\n",
			Filename: "a.deb",
			Size:     100,
			SHA256:   "hash",
		},
	}
	out := generatePackagesFile(pkgs)
	s := string(out)
	if !strings.Contains(s, "Package: a") {
		t.Error("missing control content")
	}
	if !strings.Contains(s, "Filename: a.deb") {
		t.Error("missing filename")
	}
	if !strings.Contains(s, "SHA256: hash") {
		t.Error("missing hash")
	}
}

func TestGenerateReleaseFile(t *testing.T) {
	info := ArchiveInfo{Origin: "TestOrigin", Codename: "stable"}
	out := generateReleaseFile(info, []byte("pkgs"), []byte("pkgsgz"))
	s := string(out)

	if !strings.Contains(s, "Origin: TestOrigin") {
		t.Error("missing Origin")
	}
	if !strings.Contains(s, "Codename: stable") {
		t.Error("missing Codename")
	}
	if !strings.Contains(s, "SHA256:") {
		t.Error("missing SHA256 header")
	}
	// Check for file entry
	if !strings.Contains(s, "Packages") {
		t.Error("missing Packages entry")
	}
}

// Helper to generate a temporary GPG key
func generateTestKey(t *testing.T) string {
	entity, err := openpgp.NewEntity("Test", "test", "test@example.com", nil)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PrivateKeyType, nil)
	if err != nil {
		t.Fatalf("armor encode failed: %v", err)
	}
	if err := entity.SerializePrivate(w, nil); err != nil {
		t.Fatalf("serialize failed: %v", err)
	}
	w.Close()
	return buf.String()
}

func TestSignBytes(t *testing.T) {
	key := generateTestKey(t)
	data := []byte("sign me")

	signed, err := signBytes(data, key)
	if err != nil {
		t.Fatalf("signBytes failed: %v", err)
	}

	if !strings.Contains(string(signed), "-----BEGIN PGP SIGNED MESSAGE-----") {
		t.Error("output does not look like a signed message")
	}
}

func TestExtractPublicKey(t *testing.T) {
	key := generateTestKey(t)

	// Armored
	pubArmored, err := extractPublicKey(key, true)
	if err != nil {
		t.Fatalf("extractPublicKey armored failed: %v", err)
	}
	if !strings.Contains(string(pubArmored), "-----BEGIN PGP PUBLIC KEY BLOCK-----") {
		t.Error("output does not look like an armored public key")
	}

	// Binary
	pubBin, err := extractPublicKey(key, false)
	if err != nil {
		t.Fatalf("extractPublicKey binary failed: %v", err)
	}
	if len(pubBin) == 0 {
		t.Error("binary key is empty")
	}
}

func TestGenerateHierarchicalRelease(t *testing.T) {
	info := ArchiveInfo{Origin: "Hierarchical"}
	entries := []releaseFileEntry{
		{Path: "main/binary-amd64/Packages", Size: 100, Hash: "h1"},
		{Path: "main/binary-arm64/Packages", Size: 200, Hash: "h2"},
	}

	out := generateHierarchicalRelease(info, entries)
	s := string(out)

	if !strings.Contains(s, "Origin: Hierarchical") {
		t.Error("missing Origin")
	}
	// Check entries exist and are sorted (amd64 before arm64)
	if !strings.Contains(s, "h1 100 main/binary-amd64/Packages") {
		t.Error("missing amd64 entry")
	}
}

func TestParseControlFileFull(t *testing.T) {
	content := `Package: my-pkg
Version: 1.2.3
Architecture: amd64
Depends: libc6, git
Description: A test package
 This is the extended description.
Extra: value
`
	var m Metadata
	m.ExtraFields = make(map[string]string)
	if err := parseControlFile(content, &m); err != nil {
		t.Fatalf("parseControlFile failed: %v", err)
	}

	if m.Package != "my-pkg" {
		t.Errorf("expected Package my-pkg, got %s", m.Package)
	}
	if m.Version != "1.2.3" {
		t.Errorf("expected Version 1.2.3, got %s", m.Version)
	}
	if len(m.Depends) != 2 || m.Depends[0] != "libc6" || m.Depends[1] != "git" {
		t.Errorf("expected Depends [libc6 git], got %v", m.Depends)
	}
	if !strings.Contains(m.Description, "A test package") {
		t.Errorf("description mismatch")
	}
	if m.ExtraFields["Extra"] != "value" {
		t.Errorf("expected Extra field value, got %s", m.ExtraFields["Extra"])
	}
}

func TestSplitList(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a, b", []string{"a", "b"}},
		{" a , b , c ", []string{"a", "b", "c"}},
	}

	for _, tt := range tests {
		got := splitList(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("splitList(%q) len = %d, want %d", tt.input, len(got), len(tt.want))
		}
	}
}

func TestParseReleaseFile(t *testing.T) {
	content := `Origin: TestOrigin
Label: TestLabel
Suite: stable
Codename: bookworm
Architectures: amd64 arm64
Components: main
Description: Test Description
`
	var info ArchiveInfo
	if err := parseReleaseFile(content, &info); err != nil {
		t.Fatalf("parseReleaseFile failed: %v", err)
	}

	if info.Origin != "TestOrigin" {
		t.Errorf("expected Origin TestOrigin, got %s", info.Origin)
	}
	if info.Label != "TestLabel" {
		t.Errorf("expected Label TestLabel, got %s", info.Label)
	}
	if info.Codename != "bookworm" {
		t.Errorf("expected Codename bookworm, got %s", info.Codename)
	}
	if info.Architectures != "amd64 arm64" {
		t.Errorf("expected Architectures amd64 arm64, got %s", info.Architectures)
	}
}

func TestParsePackagesIndex(t *testing.T) {
	content := `Package: pkg1
Version: 1.0
Architecture: amd64
Filename: pool/main/p/pkg1/pkg1.deb
Size: 1024
SHA256: hash1

Package: pkg2
Version: 2.0
Architecture: all
Filename: http://example.com/pkg2.deb
`
	pkgs, err := parsePackagesIndex(content)
	if err != nil {
		t.Fatalf("parsePackagesIndex failed: %v", err)
	}

	if len(pkgs) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(pkgs))
	}

	// Check that Size/SHA256 were removed from ExtraFields
	if _, ok := pkgs[0].Metadata.ExtraFields["Size"]; ok {
		t.Error("Size field should be removed from ExtraFields")
	}
}

func TestBumpVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"1.0", "1.0-1"},
		{"1.0-1", "1.0-2"},
		{"1.0-9", "1.0-10"},
		{"1.0-1.2", "1.0-1.3"},
		{"1.0-1.9", "1.0-1.a"},
		{"1.0-a", "1.0-b"},
		{"1.0-z", "1.0-z0"},
		{"1.0-1ubuntu1", "1.0-1ubuntu2"},
		{"1.0-1ubuntu9", "1.0-1ubuntua"},
		{"1.0-", "1.0-1"},
		{"1.0-foo+", "1.0-fop+"},
	}

	for _, tt := range tests {
		if got := BumpVersion(tt.input); got != tt.want {
			t.Errorf("BumpVersion(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
