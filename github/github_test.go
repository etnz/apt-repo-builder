package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/etnz/apt-repo-builder/apt"
)

// fakeGithub implements http.RoundTripper to mock GitHub API.
type fakeGithub struct {
	// Map "owner/repo" -> list of releases
	repos map[string][]*release
	// Map assetID -> content (for verification)
	assetsContent    map[int64][]byte
	nextAssetID      int64
	requestValidator func(*http.Request)
}

func newFakeGithub() *fakeGithub {
	return &fakeGithub{
		repos:         make(map[string][]*release),
		assetsContent: make(map[int64][]byte),
		nextAssetID:   1000,
	}
}

func (f *fakeGithub) addRelease(owner, repo, tag string, assets []asset) {
	key := owner + "/" + repo
	rel := &release{
		ID:      int64(len(f.repos[key]) + 1),
		TagName: tag,
		Assets:  assets,
	}
	f.repos[key] = append(f.repos[key], rel)
}

func (f *fakeGithub) RoundTrip(req *http.Request) (*http.Response, error) {
	if f.requestValidator != nil {
		f.requestValidator(req)
	}

	path := req.URL.Path
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	// parts example: ["repos", "owner", "repo", "releases", ...]

	if req.URL.Host == "api.github.com" {
		if len(parts) >= 4 && parts[0] == "repos" && parts[3] == "releases" {
			owner, repo := parts[1], parts[2]

			// GET /repos/:owner/:repo/releases
			if req.Method == "GET" && len(parts) == 4 {
				return f.listReleases(owner, repo)
			}

			// GET /repos/:owner/:repo/releases/tags/:tag
			if req.Method == "GET" && len(parts) == 6 && parts[4] == "tags" {
				return f.getReleaseByTag(owner, repo, parts[5])
			}

			// DELETE /repos/:owner/:repo/releases/assets/:id
			if req.Method == "DELETE" && len(parts) == 6 && parts[4] == "assets" {
				id, _ := strconv.ParseInt(parts[5], 10, 64)
				return f.deleteAsset(owner, repo, id)
			}
		}
	}

	if req.URL.Host == "uploads.github.com" {
		// POST /repos/:owner/:repo/releases/:id/assets
		if req.Method == "POST" && len(parts) >= 6 && parts[0] == "repos" && parts[3] == "releases" && parts[5] == "assets" {
			owner, repo := parts[1], parts[2]
			id, _ := strconv.ParseInt(parts[4], 10, 64)
			name := req.URL.Query().Get("name")
			return f.uploadAsset(owner, repo, id, name, req.Body)
		}
	}

	return &http.Response{
		StatusCode: 404,
		Body:       io.NopCloser(strings.NewReader("Not Found")),
		Header:     make(http.Header),
	}, nil
}

func (f *fakeGithub) listReleases(owner, repo string) (*http.Response, error) {
	key := owner + "/" + repo
	releases := f.repos[key]
	// Return empty list if nil, to match API behavior
	if releases == nil {
		releases = []*release{}
	}
	body, _ := json.Marshal(releases)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body))}, nil
}

func (f *fakeGithub) getReleaseByTag(owner, repo, tag string) (*http.Response, error) {
	key := owner + "/" + repo
	releases := f.repos[key]
	for _, rel := range releases {
		if rel.TagName == tag {
			body, _ := json.Marshal(rel)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body))}, nil
		}
	}
	return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("Not Found"))}, nil
}

func (f *fakeGithub) deleteAsset(owner, repo string, assetID int64) (*http.Response, error) {
	key := owner + "/" + repo
	releases := f.repos[key]
	for _, rel := range releases {
		for i, a := range rel.Assets {
			if a.ID == assetID {
				// Delete from slice
				rel.Assets = append(rel.Assets[:i], rel.Assets[i+1:]...)
				return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
			}
		}
	}
	return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("Asset not found"))}, nil
}

func (f *fakeGithub) uploadAsset(owner, repo string, releaseID int64, name string, body io.Reader) (*http.Response, error) {
	key := owner + "/" + repo
	releases := f.repos[key]
	for _, rel := range releases {
		if rel.ID == releaseID {
			newID := f.nextAssetID
			f.nextAssetID++

			content, _ := io.ReadAll(body)
			f.assetsContent[newID] = content

			newAsset := asset{
				ID:                 newID,
				Name:               name,
				BrowserDownloadURL: fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", owner, repo, rel.TagName, name),
			}
			rel.Assets = append(rel.Assets, newAsset)

			respBody, _ := json.Marshal(newAsset)
			return &http.Response{StatusCode: 201, Body: io.NopCloser(bytes.NewReader(respBody))}, nil
		}
	}
	return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("Release not found"))}, nil
}

// --- Tests ---

func TestFetchAllDebURLs(t *testing.T) {
	fake := newFakeGithub()
	oldTransport := http.DefaultClient.Transport
	http.DefaultClient.Transport = fake
	defer func() { http.DefaultClient.Transport = oldTransport }()

	// Setup Data
	fake.addRelease("owner1", "repo1", "v1.0", []asset{
		{Name: "app_1.0_amd64.deb", BrowserDownloadURL: "http://dl/app_1.0.deb"},
		{Name: "readme.txt", BrowserDownloadURL: "http://dl/readme.txt"},
	})
	fake.addRelease("owner2", "repo2", "v2.0", []asset{
		{Name: "tool_2.0_arm64.deb", BrowserDownloadURL: "http://dl/tool_2.0.deb"},
	})

	projects := []Repo{
		{Owner: "owner1", Name: "repo1"},
		{Owner: "owner2", Name: "repo2"},
	}

	urls := FetchAllDebURLs(projects, "dummy-token")

	if len(urls) != 2 {
		t.Errorf("Expected 2 URLs, got %d", len(urls))
	}
	expected := map[string]bool{
		"http://dl/app_1.0.deb":  true,
		"http://dl/tool_2.0.deb": true,
	}
	for _, u := range urls {
		if !expected[u] {
			t.Errorf("Unexpected URL: %s", u)
		}
	}
}

func TestPushDeb(t *testing.T) {
	fake := newFakeGithub()
	oldTransport := http.DefaultClient.Transport
	http.DefaultClient.Transport = fake
	defer func() { http.DefaultClient.Transport = oldTransport }()

	owner, repo := "myorg", "myrepo"
	tag, indexTag := "v1.0.0", "index"

	// Setup Releases
	// 1. Target release for binaries
	fake.addRelease(owner, repo, tag, []asset{
		// Simulate an existing asset that should be overwritten
		{ID: 555, Name: "test.deb", BrowserDownloadURL: "http://old/test.deb"},
	})
	// 2. Target release for indices
	fake.addRelease(owner, repo, indexTag, []asset{})

	// Create dummy local file
	tmpDir := t.TempDir()
	debPath := filepath.Join(tmpDir, "test.deb")
	os.WriteFile(debPath, []byte("binary-content"), 0644)

	// Create dummy index
	idx := &apt.PackageIndex{
		PackagesContent:   []byte("packages-content"),
		PackagesGzContent: []byte("packages-gz-content"),
		ReleaseContent:    []byte("release-content"),
		InReleaseContent:  []byte("inrelease-content"),
	}

	// Execute
	err := PushDeb(owner+"/"+repo, tag, indexTag, "dummy-token", []string{debPath}, idx)
	if err != nil {
		t.Fatalf("PushDeb failed: %v", err)
	}

	// Verify Binary Upload
	// The old asset (ID 555) should be gone, and a new one present
	releases := fake.repos[owner+"/"+repo]
	var binRel *release
	for _, r := range releases {
		if r.TagName == tag {
			binRel = r
			break
		}
	}

	found := false
	for _, a := range binRel.Assets {
		if a.Name == "test.deb" {
			if a.ID == 555 {
				t.Error("Old asset was not deleted")
			}
			found = true
			// Verify content
			if string(fake.assetsContent[a.ID]) != "binary-content" {
				t.Error("Uploaded binary content mismatch")
			}
		}
	}
	if !found {
		t.Error("New binary asset not found in release")
	}

	// Verify Index Upload
	var idxRel *release
	for _, r := range releases {
		if r.TagName == indexTag {
			idxRel = r
			break
		}
	}
	if len(idxRel.Assets) != 4 {
		t.Errorf("Expected 4 index assets, got %d", len(idxRel.Assets))
	}
}

func TestUploadRepoIndices_Incomplete(t *testing.T) {
	idx := &apt.PackageIndex{} // Empty
	err := UploadRepoIndices("o/r", "tag", "tok", idx)
	if err == nil || !strings.Contains(err.Error(), "incomplete repository") {
		t.Errorf("Expected incomplete error, got %v", err)
	}
}

func TestPredictRemote(t *testing.T) {
	localPkg := &apt.Package{
		Filename: "/some/local/path/package_1.0_amd64.deb",
	}
	remotePkg := PredictRemote("owner/repo", "v1.0.0", localPkg)

	expected := "https://github.com/owner/repo/releases/download/v1.0.0/package_1.0_amd64.deb"
	if remotePkg.Filename != expected {
		t.Errorf("Expected %s, got %s", expected, remotePkg.Filename)
	}
}

func TestTokenPassing(t *testing.T) {
	fake := newFakeGithub()
	oldTransport := http.DefaultClient.Transport
	http.DefaultClient.Transport = fake
	defer func() { http.DefaultClient.Transport = oldTransport }()

	// Case 1: Token present
	token := "secret-token"
	fake.requestValidator = func(req *http.Request) {
		auth := req.Header.Get("Authorization")
		expected := "token " + token
		if auth != expected {
			t.Errorf("Expected Authorization header %q, got %q", expected, auth)
		}
	}
	_, _ = FetchDebURLs("o", "r", token)

	// Case 2: Token empty
	fake.requestValidator = func(req *http.Request) {
		auth := req.Header.Get("Authorization")
		if auth != "" {
			t.Errorf("Expected no Authorization header, got %q", auth)
		}
	}
	_, _ = FetchDebURLs("o", "r", "")
}
