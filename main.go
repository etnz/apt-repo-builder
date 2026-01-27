package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/etnz/apt-repo-builder/apt"
	"github.com/etnz/apt-repo-builder/github"
	"gopkg.in/yaml.v3"
)

// Config is a business object holding the application's configuration.
type Config struct {
	Repositories   []apt.RepoConfig
	GithubProjects []github.Repo
	ArchiveInfo    apt.ArchiveInfo
}

var cache = make(map[string]apt.CachedAsset)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: apt-repo-builder <command> [flags]")
		fmt.Println("Commands: index-all, push-deb")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "index-all":
		indexAll(os.Args[2:])
	case "push-deb":
		pushDeb(os.Args[2:])
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func indexAll(args []string) {
	fs := flag.NewFlagSet("index-all", flag.ExitOnError)
	outDir := fs.String("out", "dist", "Output directory for indices")
	confPath := fs.String("config", "apt-repo-builder.yaml", "Path to config file")
	cachePath := fs.String("cache", "repo-cache.json", "Path to cache file")
	repo := fs.String("repo", "", "Target GitHub repo (owner/name) to upload indices")
	indexTag := fs.String("index-tag", "", "Tag to upload indices to")
	fs.Parse(args)

	// Read YAML configuration
	config, err := decodeConfig(*confPath)
	if err != nil {
		fmt.Printf("Fatal: Could not read or parse config file %s: %v\n", *confPath, err)
		os.Exit(1)
	}

	loadCache(*cachePath)

	if err := os.MkdirAll(*outDir, 0755); err != nil {
		fmt.Printf("Fatal: Could not create output directory %s: %v\n", *outDir, err)
		os.Exit(1)
	}

	fmt.Println("Building APT Repository Index...")

	githubToken := os.Getenv("GITHUB_TOKEN")
	gpgPrivateKey := os.Getenv("GPG_PRIVATE_KEY")

	urls := github.FetchAllDebURLs(config.GithubProjects, githubToken)
	masterIndex, err := apt.IndexAll(config.Repositories, urls, cache, config.ArchiveInfo, gpgPrivateKey)
	if err != nil {
		fmt.Printf("Fatal: %v\n", err)
		os.Exit(1)
	}

	saveCache(*cachePath)
	masterIndex.SaveTo(*outDir)

	// Upload Indices if requested
	if *repo != "" && *indexTag != "" {
		fmt.Printf("Uploading indices to %s @ %s...\n", *repo, *indexTag)
		github.UploadRepoIndices(*repo, *indexTag, githubToken, masterIndex)
	}
}

func pushDeb(args []string) {
	fs := flag.NewFlagSet("push-deb", flag.ExitOnError)
	confPath := fs.String("config", "apt-repo-builder.yaml", "Path to config file")
	cachePath := fs.String("cache", "repo-cache.json", "Path to cache file")
	srcDir := fs.String("src", "./build", "Directory containing local .deb files")
	repo := fs.String("repo", "", "Target GitHub repo (owner/name)")
	tag := fs.String("tag", "", "Target versioned release tag for binaries")
	indexTag := fs.String("index-tag", "", "Tag to upload indices to")
	// dryRun := fs.Bool("dry-run", false, "Run verification without upload")
	fs.Parse(args)

	if *repo == "" || *tag == "" || *indexTag == "" {
		fmt.Println("Error: --repo, --tag, and --index-tag are required for push-deb")
		os.Exit(1)
	}

	// Read Config
	config, err := decodeConfig(*confPath)
	if err != nil {
		fmt.Printf("Fatal: Could not read config: %v\n", err)
		os.Exit(1)
	}

	loadCache(*cachePath)
	githubToken := os.Getenv("GITHUB_TOKEN")

	// Build Master Index (from config)
	urls := github.FetchAllDebURLs(config.GithubProjects, githubToken)
	masterIndex, err := apt.IndexAll(config.Repositories, urls, cache, config.ArchiveInfo, "")
	if err != nil {
		fmt.Printf("Fatal: Failed to build master index: %v\n", err)
		os.Exit(1)
	}
	// The current github repo being published to is also a standard apt repo.
	currentRepo := apt.RepoConfig{
		URL: fmt.Sprintf("https://github.com/%s/releases/download/%s", *repo, *indexTag),
	}
	// Fetch Remote Index (Packages)
	currentRemoteIndex, err := apt.FetchPackageIndexFrom(currentRepo, cache)
	if err != nil {
		fmt.Printf("Warning: Could not fetch existing index (starting fresh): %v\n", err)
		currentRemoteIndex = apt.NewPackageIndex()
	}

	// Validate and Prune Local Files
	files, _ := filepath.Glob(filepath.Join(*srcDir, "*.deb"))
	var toUpload []string
	localIndex := apt.NewPackageIndex()

	for _, f := range files {
		pkg, skip, err := apt.ConflictFree(f, masterIndex)
		if err != nil {
			if strings.Contains(err.Error(), "version conflict") {
				fmt.Printf("Fatal: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Skipping %s: %v\n", f, err)
			continue
		}
		if skip {
			fmt.Printf("Skipping %s (already published)\n", filepath.Base(f))
			os.Remove(f) // Prune
			continue
		}

		toUpload = append(toUpload, f)

		// Add to index
		newPkg := github.PredictRemote(*repo, *tag, pkg)
		if err := localIndex.Add(newPkg); err != nil {
			fmt.Printf("Fatal: Failed to add package to local index: %v\n", err)
			os.Exit(1)
		}
	}

	// Reindex the currentRemote.
	if err := currentRemoteIndex.Append(localIndex); err != nil {
		fmt.Printf("Fatal: Failed to merge local index: %v\n", err)
		os.Exit(1)
	}
	if err := currentRemoteIndex.ComputeIndices(config.ArchiveInfo, os.Getenv("GPG_PRIVATE_KEY")); err != nil {
		fmt.Printf("Fatal: Failed to compute indices: %v\n", err)
		os.Exit(1)
	}

	// Upload All.
	if err := github.PushDeb(*repo, *tag, *indexTag, githubToken, toUpload, currentRemoteIndex); err != nil {
		fmt.Printf("Fatal: %v\n", err)
		os.Exit(1)
	}

	saveCache(*cachePath)
}

func decodeConfig(path string) (*Config, error) {
	// Internal DTOs for YAML deserialization
	type yamlRepoConfig struct {
		URL           string   `yaml:"url"`
		Suite         string   `yaml:"suite"`
		Component     string   `yaml:"component"`
		Architectures []string `yaml:"architectures"`
	}
	type yamlArchiveInfo struct {
		Origin        string `yaml:"origin"`
		Label         string `yaml:"label"`
		Suite         string `yaml:"suite"`
		Codename      string `yaml:"codename"`
		Architectures string `yaml:"architectures"`
		Components    string `yaml:"components"`
		Description   string `yaml:"description"`
	}
	type yamlGithubProject struct {
		Name  string `yaml:"name"`
		Owner string `yaml:"owner"`
	}
	type yamlConfig struct {
		Repositories   []yamlRepoConfig    `yaml:"repositories"`
		GithubProjects []yamlGithubProject `yaml:"github_projects"`
		ArchiveInfo    yamlArchiveInfo     `yaml:"archive_info"`
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var dto yamlConfig
	if err := yaml.Unmarshal(data, &dto); err != nil {
		return nil, err
	}

	// Map DTO to business object
	config := &Config{
		ArchiveInfo: apt.ArchiveInfo{
			Origin:        dto.ArchiveInfo.Origin,
			Label:         dto.ArchiveInfo.Label,
			Suite:         dto.ArchiveInfo.Suite,
			Codename:      dto.ArchiveInfo.Codename,
			Architectures: dto.ArchiveInfo.Architectures,
			Components:    dto.ArchiveInfo.Components,
			Description:   dto.ArchiveInfo.Description,
		},
		Repositories:   make([]apt.RepoConfig, len(dto.Repositories)),
		GithubProjects: make([]github.Repo, len(dto.GithubProjects)),
	}
	for i, r := range dto.Repositories {
		config.Repositories[i] = apt.RepoConfig{
			URL:           r.URL,
			Suite:         r.Suite,
			Component:     r.Component,
			Architectures: r.Architectures,
		}
	}
	for i, p := range dto.GithubProjects {
		config.GithubProjects[i] = github.Repo{
			Name:  p.Name,
			Owner: p.Owner,
		}
	}

	return config, nil
}

func decodeCache(path string) (map[string]apt.CachedAsset, error) {
	type jsonCachedAsset struct {
		ContentHash string `json:"content_hash"`
		FileHash    string `json:"file_hash"`
		Size        int64  `json:"size"`
		Control     string `json:"control"`
		URL         string `json:"url"`
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]apt.CachedAsset), nil
		}
		return nil, err
	}

	var internalCache map[string]jsonCachedAsset
	if err := json.Unmarshal(data, &internalCache); err != nil {
		// Corrupt cache is not a fatal error, just start fresh.
		fmt.Printf("Warning: could not parse cache file %s: %v. Starting fresh.\n", path, err)
		return make(map[string]apt.CachedAsset), nil
	}

	// Map from DTO to business object
	cache := make(map[string]apt.CachedAsset, len(internalCache))
	for url, asset := range internalCache {
		cache[url] = apt.CachedAsset{
			ContentHash: asset.ContentHash,
			FileHash:    asset.FileHash,
			Size:        asset.Size,
			Control:     asset.Control,
			URL:         asset.URL,
		}
	}
	return cache, nil
}

func encodeCache(path string, cache map[string]apt.CachedAsset) error {
	type jsonCachedAsset struct {
		ContentHash string `json:"content_hash"`
		FileHash    string `json:"file_hash"`
		Size        int64  `json:"size"`
		Control     string `json:"control"`
		URL         string `json:"url"`
	}

	// Map from business object to DTO
	internalCache := make(map[string]jsonCachedAsset, len(cache))
	for url, asset := range cache {
		internalCache[url] = jsonCachedAsset{
			ContentHash: asset.ContentHash,
			FileHash:    asset.FileHash,
			Size:        asset.Size,
			Control:     asset.Control,
			URL:         asset.URL,
		}
	}

	data, err := json.MarshalIndent(internalCache, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

func loadCache(path string) {
	var err error
	cache, err = decodeCache(path)
	if err != nil {
		// This error is not fatal, just means we have no cache.
		// The decoder function already handles os.IsNotExist and parsing errors.
		fmt.Printf("Warning: could not load cache from %s: %v. Starting fresh.\n", path, err)
		cache = make(map[string]apt.CachedAsset)
	}
}

func saveCache(path string) {
	if err := encodeCache(path, cache); err != nil {
		fmt.Printf("Warning: could not save cache to %s: %v\n", path, err)
	}
}
