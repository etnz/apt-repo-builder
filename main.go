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
	// Upstream is the list of APT repositories to use for integrity checks (The Upstream World)
	Upstream []apt.RepoConfig
	// ProjectSources is the list of GitHub projects to be indexed (The Project World)
	ProjectSources []github.Repo
	// ArchiveInfo is the metadata to use when generating a new the APT repository.
	ArchiveInfo apt.ArchiveInfo
}

// cache is an in-memory cache of previously fetched .deb files.
var cache = make(map[string]apt.CachedAsset)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: apt-repo-builder <command> [flags]")
		fmt.Println("Commands: index, add")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "index":
		indexProject(os.Args[2:])
	case "add":
		addDeb(os.Args[2:])
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func indexProject(args []string) {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	outDir := fs.String("out", "dist", "Output directory for indices")
	confPath := fs.String("config", "apt-repo-builder.yaml", "Path to config file")
	cachePath := fs.String("cache", "repo-cache.json", "Path to cache file")
	to := fs.String("to", "", "Target GitHub repo slug (github.com/owner/repo/tags/tag)")
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

	fmt.Println("Building World Index...")

	githubToken := os.Getenv("GITHUB_TOKEN")
	gpgPrivateKey := os.Getenv("GPG_PRIVATE_KEY")

	urls := github.FetchAllDebs(config.ProjectSources, githubToken)
	worldIndex, err := apt.IndexWorld(config.Upstream, urls, cache, config.ArchiveInfo, gpgPrivateKey)
	if err != nil {
		fmt.Printf("Fatal: %v\n", err)
		os.Exit(1)
	}

	saveCache(*cachePath)
	worldIndex.SaveTo(*outDir)

	// Upload Indices if requested
	if *to != "" {
		owner, repo, tag, err := parseSlug(*to)
		if err != nil {
			fmt.Printf("Fatal: Invalid slug: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Uploading indices to %s/%s @ %s...\n", owner, repo, tag)
		github.UploadIndex(fmt.Sprintf("%s/%s", owner, repo), tag, githubToken, worldIndex)
	}
}

func addDeb(args []string) {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	confPath := fs.String("config", "apt-repo-builder.yaml", "Path to config file")
	cachePath := fs.String("cache", "repo-cache.json", "Path to cache file")
	srcDir := fs.String("src", "./build", "Directory containing local .deb files")
	to := fs.String("to", "", "Target GitHub release slug (github.com/owner/repo/tags/tag)")
	localIndexFlag := fs.Bool("local-index", false, "Generate a local-only index for valid ingress files")
	prune := fs.Bool("prune", false, "Remove invalid/duplicate local debs")

	fs.Parse(args)

	// Read Config
	config, err := decodeConfig(*confPath)
	if err != nil {
		fmt.Printf("Fatal: Could not read config: %v\n", err)
		os.Exit(1)
	}

	loadCache(*cachePath)
	githubToken := os.Getenv("GITHUB_TOKEN")

	// Build World Index
	urls := github.FetchAllDebs(config.ProjectSources, githubToken)
	worldIndex, err := apt.IndexWorld(config.Upstream, urls, cache, config.ArchiveInfo, "")
	if err != nil {
		fmt.Printf("Fatal: Failed to build world index: %v\n", err)
		os.Exit(1)
	}

	// Prepare local index.
	localIndex := apt.NewPackageIndex()

	// Validate and Prune Local Files.
	files, _ := filepath.Glob(filepath.Join(*srcDir, "*.deb"))
	var toUpload []string

	for _, f := range files {
		pkg, fresh, err := apt.AddPackage(f, worldIndex)
		if err != nil {
			fmt.Printf("Fatal: %v\n", err)
			os.Exit(1)
		}
		if !fresh {
			fmt.Printf("Already published with same content, skipping %s\n", filepath.Base(f))
			if *prune {
				os.Remove(f)
			}
			continue
		}

		toUpload = append(toUpload, f)

		if err := localIndex.Add(pkg); err != nil {
			fmt.Printf("Fatal: Failed to add package to local index: %v\n", err)
			os.Exit(1)
		}

	}

	if *localIndexFlag {
		if err := localIndex.Index(config.ArchiveInfo, os.Getenv("GPG_PRIVATE_KEY")); err != nil {
			fmt.Printf("Fatal: Failed to compute indices: %v\n", err)
			os.Exit(1)
		}
		localIndex.SaveTo(*srcDir)
	}

	if *to != "" {
		owner, repoName, tag, err := parseSlug(*to)
		if err != nil {
			fmt.Printf("Fatal: Invalid slug: %v\n", err)
			os.Exit(1)
		}
		repo := fmt.Sprintf("%s/%s", owner, repoName)

		if err := github.PushDeb(repo, tag, githubToken, toUpload); err != nil {
			fmt.Printf("Fatal: %v\n", err)
			os.Exit(1)
		}
	}

	saveCache(*cachePath)
}

func parseSlug(slug string) (owner, repo, tag string, err error) {
	// github.com/owner/repo/tags/tag
	parts := strings.Split(slug, "/")
	if len(parts) < 5 || parts[0] != "github.com" || parts[3] != "tags" {
		return "", "", "", fmt.Errorf("invalid slug format, expected github.com/owner/repo/tags/tag")
	}
	return parts[1], parts[2], parts[4], nil
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
	type yamlProject struct {
		ArchiveInfo yamlArchiveInfo `yaml:"archive_info"`
		Sources     []string        `yaml:"sources"`
	}
	type yamlUpstream struct {
		Sources []yamlRepoConfig `yaml:"sources"`
	}
	type yamlConfig struct {
		Project  yamlProject  `yaml:"project"`
		Upstream yamlUpstream `yaml:"upstream"`
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
			Origin:        dto.Project.ArchiveInfo.Origin,
			Label:         dto.Project.ArchiveInfo.Label,
			Suite:         dto.Project.ArchiveInfo.Suite,
			Codename:      dto.Project.ArchiveInfo.Codename,
			Architectures: dto.Project.ArchiveInfo.Architectures,
			Components:    dto.Project.ArchiveInfo.Components,
			Description:   dto.Project.ArchiveInfo.Description,
		},
		Upstream:       make([]apt.RepoConfig, len(dto.Upstream.Sources)),
		ProjectSources: make([]github.Repo, len(dto.Project.Sources)),
	}
	for i, r := range dto.Upstream.Sources {
		config.Upstream[i] = apt.RepoConfig{
			URL:           r.URL,
			Suite:         r.Suite,
			Component:     r.Component,
			Architectures: r.Architectures,
		}
	}
	for i, s := range dto.Project.Sources {
		// Parse "https://github.com/owner/repo"
		trimmed := strings.TrimPrefix(s, "https://")
		trimmed = strings.TrimPrefix(trimmed, "http://")
		parts := strings.Split(trimmed, "/")
		if len(parts) >= 3 && parts[0] == "github.com" {
			config.ProjectSources[i] = github.Repo{
				Owner: parts[1],
				Name:  parts[2],
			}
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
