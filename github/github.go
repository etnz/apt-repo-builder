package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/etnz/apt-repo-builder/apt"
)

// Repo defines a GitHub repository to harvest packages from.
type Repo struct {
	Name  string
	Owner string
}

type release struct {
	ID      int64   `json:"id"`
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func fetchReleases(owner, repo, token string) ([]release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", owner, repo)
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API status %d", resp.StatusCode)
	}

	var releases []release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	return releases, nil
}

// FetchDebURLs scans a GitHub repository's Releases and returns the download URLs
// for all assets ending in ".deb".
func FetchDebURLs(owner, repo, token string) ([]string, error) {
	releases, err := fetchReleases(owner, repo, token)
	if err != nil {
		return nil, err
	}
	var urls []string
	for _, rel := range releases {
		for _, asset := range rel.Assets {
			if strings.HasSuffix(asset.Name, ".deb") {
				urls = append(urls, asset.BrowserDownloadURL)
			}
		}
	}
	return urls, nil
}

func uploadAsset(repoSlug, tag, filePath, token string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	stat, _ := f.Stat()
	return uploadAssetFromReader(repoSlug, tag, filepath.Base(filePath), f, stat.Size(), token)
}

func uploadAssetFromReader(repoSlug, tag, fileName string, content io.Reader, size int64, token string) error {
	parts := strings.Split(repoSlug, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid repo slug")
	}
	owner, repo := parts[0], parts[1]

	// 1. Get Release ID by Tag
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", owner, repo, tag)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "token "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("release not found: %s", tag)
	}
	var rel release
	json.NewDecoder(resp.Body).Decode(&rel)

	// 2. Check if asset exists and delete it (overwrite)
	for _, a := range rel.Assets {
		if a.Name == fileName {
			delUrl := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/assets/%d", owner, repo, a.ID)
			delReq, _ := http.NewRequest("DELETE", delUrl, nil)
			delReq.Header.Set("Authorization", "token "+token)
			http.DefaultClient.Do(delReq)
			break
		}
	}

	// 3. Upload
	uploadUrl := fmt.Sprintf("https://uploads.github.com/repos/%s/%s/releases/%d/assets?name=%s", owner, repo, rel.ID, fileName)
	upReq, _ := http.NewRequest("POST", uploadUrl, content)
	upReq.Header.Set("Authorization", "token "+token)
	upReq.Header.Set("Content-Type", "application/octet-stream")
	upReq.ContentLength = size

	upResp, err := http.DefaultClient.Do(upReq)
	if err != nil {
		return err
	}
	defer upResp.Body.Close()
	if upResp.StatusCode != 201 {
		body, _ := io.ReadAll(upResp.Body)
		return fmt.Errorf("upload failed: %s %s", upResp.Status, string(body))
	}
	return nil
}

// UploadIndex uploads the generated APT metadata files (Packages, Release, InRelease)
// to a specific GitHub Release tag. This effectively updates the repository index
// hosted on GitHub.
func UploadIndex(repoSlug, tag, token string, idx *apt.PackageIndex) error {
	// Check completeness
	if len(idx.ReleaseContent) == 0 {
		return fmt.Errorf("incomplete repository: Release missing")
	}

	assets := []struct {
		Name    string
		Content []byte
	}{
		{"Packages", idx.PackagesContent},
		{"Packages.gz", idx.PackagesGzContent},
		{"Release", idx.ReleaseContent},
		{"InRelease", idx.InReleaseContent},
		{"public.key", idx.PublicKeyContent},
	}

	for _, a := range assets {
		if len(a.Content) == 0 {
			continue
		}
		if err := uploadAssetFromReader(repoSlug, tag, a.Name, bytes.NewReader(a.Content), int64(len(a.Content)), token); err != nil {
			return fmt.Errorf("failed to upload %s: %w", a.Name, err)
		} else {
			fmt.Printf("Uploaded %s\n", a.Name)
		}
	}
	return nil
}

// PredictRemote prepares a local package for the index by rewriting its Filename.
// Instead of the local path, it sets the Filename to the URL where the file *will* be
// available after upload to GitHub Releases.
func PredictRemote(repo, tag string, localPkg *apt.Package) *apt.Package {
	dlUrl := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, tag, filepath.Base(localPkg.Filename))
	newPkg := *localPkg
	newPkg.Filename = dlUrl
	return &newPkg
}

// PushDeb performs the component-level publish operation:
// 1. Uploads the .deb binaries to the target Release.
// 2. Uploads the updated repository indices to the index Release.
func PushDeb(repoSlug, tag, token string, files []string) error {
	for _, f := range files {
		fmt.Printf("Uploading binary %s to %s...\n", filepath.Base(f), tag)
		if err := uploadAsset(repoSlug, tag, f, token); err != nil {
			return fmt.Errorf("error uploading binary %s: %w", f, err)
		}
	}
	return nil
}

// FetchAllDebs aggregates .deb download URLs from multiple GitHub repositories.
func FetchAllDebs(projects []Repo, token string) []string {
	var urls []string
	for _, proj := range projects {
		fmt.Printf("Scraping %s/%s...\n", proj.Owner, proj.Name)
		u, err := FetchDebURLs(proj.Owner, proj.Name, token)
		if err != nil {
			fmt.Printf("  Error: %v\n", err)
			continue
		}
		urls = append(urls, u...)
	}
	return urls
}
