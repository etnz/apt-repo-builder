package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/crypto/openpgp"
	"golang.org/x/crypto/openpgp/clearsign"
	"gopkg.in/yaml.v3"
)

type RemoteRepo struct {
	Name  string `yaml:"name"`
	Owner string `yaml:"owner" json:"owner"`
	Limit int    `yaml:"limit" json:"limit"`
}

type ArchiveInfo struct {
	Origin        string `yaml:"origin"`
	Label         string `yaml:"label"`
	Suite         string `yaml:"suite"`
	Codename      string `yaml:"codename"`
	Architectures string `yaml:"architectures"`
	Components    string `yaml:"components"`
	Description   string `yaml:"description"`
}

type Config struct {
	RemoteRepos []RemoteRepo `yaml:"remote_repos"`
	ArchiveInfo ArchiveInfo  `yaml:"archive_info"`
}

type CachedAsset struct {
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Control string `json:"control"`
}

type GithubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

var cache = make(map[string]CachedAsset)

func main() {
	// Define command line flags to mimic nfpm behavior
	outDir := flag.String("out", "dist", "Output directory for the APT repository indices")
	confPath := flag.String("config", "apt-repo-config.yaml", "Path to the repository configuration file")
	cachePath := flag.String("cache-file", "repo-cache.json", "Path to the repository cache file")
	flag.Parse()

	// Read YAML configuration
	confData, err := os.ReadFile(*confPath)
	if err != nil {
		fmt.Printf("Fatal: Could not read config: %v\n", err)
		os.Exit(1)
	}

	var config Config
	if err := yaml.Unmarshal(confData, &config); err != nil {
		fmt.Printf("Fatal: Parse error: %v\n", err)
		os.Exit(1)
	}

	loadCache(*cachePath)

	os.MkdirAll(*outDir, 0755)
	packagesPath := filepath.Join(*outDir, "Packages")
	pkgFile, err := os.Create(packagesPath)
	if err != nil {
		fmt.Printf("Fatal: Output error: %v\n", err)
		os.Exit(1)
	}
	defer pkgFile.Close()

	fmt.Println("Building APT Repository Index...")

	githubToken := os.Getenv("GITHUB_TOKEN")

	// Process Remotes
	for _, repo := range config.RemoteRepos {
		fmt.Printf("Scraping %s/%s...\n", repo.Owner, repo.Name)
		releases, err := fetchGithubReleases(repo.Owner, repo.Name, githubToken)
		if err != nil {
			fmt.Printf("  Error: %v\n", err)
			continue
		}

		indexedCount := 0
		for _, rel := range releases {
			if indexedCount >= repo.Limit {
				break
			}

			for _, asset := range rel.Assets {
				if strings.HasSuffix(asset.Name, ".deb") {
					fmt.Printf("  + %s (%s)\n", asset.Name, rel.TagName)
					if err := processPackage(asset.BrowserDownloadURL, pkgFile); err != nil {
						fmt.Printf("    Error: %v\n", err)
					} else {
						indexedCount++
					}
					break
				}
			}
		}
	}

	pkgFile.Close()
	saveCache(*cachePath)
	generateAptIndices(config, *outDir)
}

func fetchGithubReleases(owner, repo, token string) ([]GithubRelease, error) {
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

	var releases []GithubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	return releases, nil
}

func processPackage(url string, w io.Writer) error {
	if cached, ok := cache[url]; ok {
		return writeStanza(w, cached.Control, url, cached.SHA256, cached.Size)
	}

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	tmp, err := os.CreateTemp("", "pkg-*.deb")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	hasher := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hasher), resp.Body)
	if err != nil {
		return err
	}
	tmp.Close()

	control, err := extractControl(tmp.Name())
	if err != nil {
		return err
	}

	sha := hex.EncodeToString(hasher.Sum(nil))
	cache[url] = CachedAsset{SHA256: sha, Size: size, Control: control}

	return writeStanza(w, control, url, sha, size)
}

func writeStanza(w io.Writer, control, filename, sha string, size int64) error {
	fmt.Fprint(w, control)
	if !strings.HasSuffix(control, "\n") {
		fmt.Fprint(w, "\n")
	}
	fmt.Fprintf(w, "Filename: %s\nSize: %d\nSHA256: %s\n\n", filename, size, sha)
	return nil
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

func generateAptIndices(config Config, outputDir string) {
	fmt.Println("Finalizing indices...")

	pkgPath := filepath.Join(outputDir, "Packages")
	gzPath := pkgPath + ".gz"

	// Gzip Packages
	pData, _ := os.ReadFile(pkgPath)
	f, _ := os.Create(gzPath)
	gw := gzip.NewWriter(f)
	gw.Write(pData)
	gw.Close()
	f.Close()

	// Release File
	relPath := filepath.Join(outputDir, "Release")
	rf, _ := os.Create(relPath)
	i := config.ArchiveInfo
	fmt.Fprintf(rf, "Origin: %s\nLabel: %s\nSuite: %s\nCodename: %s\nArchitectures: %s\nComponents: %s\nDescription: %s\nSHA256:\n",
		i.Origin, i.Label, i.Suite, i.Codename, i.Architectures, i.Components, i.Description)

	for _, name := range []string{"Packages", "Packages.gz"} {
		d, _ := os.ReadFile(filepath.Join(outputDir, name))
		h := sha256.Sum256(d)
		fmt.Fprintf(rf, " %x %d %s\n", h, len(d), name)
	}
	rf.Close()

	// Signing
	key := os.Getenv("GPG_PRIVATE_KEY")
	if key != "" {
		if err := sign(relPath, key, filepath.Join(outputDir, "InRelease")); err != nil {
			fmt.Printf("Signing failed: %v\n", err)
		} else {
			fmt.Println("Successfully signed InRelease.")
		}
	}
}

func sign(inputPath, key, outputPath string) error {
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(key))
	if err != nil {
		return err
	}
	var signer *openpgp.Entity
	for _, e := range entities {
		if e.PrivateKey != nil {
			signer = e
			break
		}
	}
	if signer == nil {
		return fmt.Errorf("no private key")
	}

	out, _ := os.Create(outputPath)
	defer out.Close()
	w, err := clearsign.Encode(out, signer.PrivateKey, nil)
	if err != nil {
		return err
	}
	content, _ := os.ReadFile(inputPath)
	w.Write(content)
	return w.Close()
}

func loadCache(path string) {
	d, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(d, &cache)
	}
}

func saveCache(path string) {
	d, _ := json.MarshalIndent(cache, "", "  ")
	os.WriteFile(path, d, 0644)
}
