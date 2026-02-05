package deb

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateControlFile(t *testing.T) {
	p := &Package{
		Metadata: Metadata{
			Package:      "test-pkg",
			Version:      "1.2.3",
			Architecture: "amd64",
			Maintainer:   "Maintainer <m@example.com>",
			Description:  "Short description\n Long description line 1\n Long description line 2",
			Depends:      []string{"libc6", "git"},
		},
	}

	// 2048 bytes -> 2KB installed size
	out := p.generateControlFile(2048)

	expectedLines := []string{
		"Package: test-pkg",
		"Version: 1.2.3",
		"Architecture: amd64",
		"Maintainer: Maintainer <m@example.com>",
		"Installed-Size: 2",
		"Depends: libc6, git",
		"Description: Short description",
		" Long description line 1",
		" Long description line 2",
	}

	for _, line := range expectedLines {
		if !strings.Contains(out, line) {
			t.Errorf("control file missing expected line: %q", line)
		}
	}
}

func TestGenerateMd5sums(t *testing.T) {
	p := &Package{Files: []File{}}
	md5Map := map[string]string{
		"/usr/bin/b": "hash_b",
		"/usr/bin/a": "hash_a",
	}

	out := p.generateMd5sums(md5Map)

	// Expect sorted output
	expected := "hash_a  usr/bin/a\nhash_b  usr/bin/b\n"
	if out != expected {
		t.Errorf("expected:\n%q\ngot:\n%q", expected, out)
	}
}

func TestBuildDataArchive(t *testing.T) {
	content := []byte("test content")
	p := &Package{
		Files: []File{
			{
				DestPath: "/usr/bin/test",
				Mode:     0755,
				Body:     string(content),
				ModTime:  time.Now(),
			},
		},
	}

	var buf bytes.Buffer
	md5Map, size, err := p.buildDataArchive(&buf)
	if err != nil {
		t.Fatalf("buildDataArchive failed: %v", err)
	}

	if size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), size)
	}

	hash := md5.Sum(content)
	expectedHash := hex.EncodeToString(hash[:])
	if got := md5Map["/usr/bin/test"]; got != expectedHash {
		t.Errorf("expected hash %s, got %s", expectedHash, got)
	}
}

func TestStandardFilename(t *testing.T) {
	p := &Package{
		Metadata: Metadata{
			Package:      "foo",
			Version:      "1.0.0",
			Architecture: "arm64",
		},
	}
	if got := p.StandardFilename(); got != "foo_1.0.0_arm64.deb" {
		t.Errorf("expected foo_1.0.0_arm64.deb, got %s", got)
	}
}

func TestIntegrationDebGeneration(t *testing.T) {
	// Ensure dpkg-deb is available
	if _, err := exec.LookPath("dpkg-deb"); err != nil {
		t.Skip("dpkg-deb not found, skipping integration test")
	}

	tmpDir := t.TempDir()
	debPath := filepath.Join(tmpDir, "test.deb")

	pkg := &Package{
		Metadata: Metadata{
			Package:      "test-integration",
			Version:      "1.0.0",
			Architecture: "amd64",
			Maintainer:   "Test User <test@example.com>",
			Description:  "Test integration package",
		},
		Files: []File{
			{
				DestPath: "/usr/bin/hello",
				Mode:     0755,
				Body:     "#!/bin/sh\necho hello\n",
				ModTime:  time.Now(),
			},
		},
	}

	f, err := os.Create(debPath)
	if err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	if _, err := pkg.WriteTo(f); err != nil {
		f.Close()
		t.Fatalf("WriteTo failed: %v", err)
	}
	f.Close()

	// Validate metadata
	out, err := exec.Command("dpkg-deb", "--info", debPath).CombinedOutput()
	if err != nil {
		t.Fatalf("dpkg-deb --info failed: %v\n%s", err, out)
	}
	info := string(out)
	if !strings.Contains(info, "Package: test-integration") {
		t.Errorf("missing Package field in info")
	}

	// Validate contents
	out, err = exec.Command("dpkg-deb", "--contents", debPath).CombinedOutput()
	if err != nil {
		t.Fatalf("dpkg-deb --contents failed: %v\n%s", err, out)
	}
	contents := string(out)
	if !strings.Contains(contents, "./usr/bin/hello") {
		t.Errorf("missing file in contents: %s", contents)
	}
}
