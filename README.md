# APT Repository Builder

A stateless, zero-infrastructure aggregator that transforms GitHub Releases into a fully functional APT repository.

## Why?

Traditional APT repository managers (Aptly, Reprepro) are **stateful**. They require a local database and a persistent filesystem to track packages. This makes them difficult and "noisy" to use in modern CI/CD environments like GitHub Actions.

**This tool is different:**

* **Stateless:** No database required. The "Source of Truth" is your configuration file and your GitHub Releases.

* **Native Go:** Handles `.deb` (ar/tar) parsing and GPG/OpenPGP signing natively. No `dpkg-deb` or `gpg` binary needed.

* **CDN Powered:** The index points `apt` directly to GitHub's global CDN for binary downloads. You only host the small text indices.

* **Fast:** Supports a JSON-based cache to skip re-downloading and re-scanning assets that haven't changed.

## Installation

```bash
go get github.com/youruser/apt-repo-builder
```

## Configuration

The builder uses a simple YAML configuration (`apt-repo-config.yaml`):

```yaml
remote_repos:
  - name: "my-app-binary"
    owner: "my-org"
    limit: 3 # Keep only the latest 3 versions in the index

archive_info:
  origin: "my-repo"
  label: "my-repo"
  suite: "stable"
  codename: "stable"
  architectures: "amd64 arm64"
  components: "main"
  description: "My Custom APT Repository"
```

## Usage

### Local Execution

```bash
export GITHUB_TOKEN="your_token"
export GPG_PRIVATE_KEY=$(cat private.key)
go run main.go --config apt-repo-config.yaml --out ./public --cache-file repo-cache.json
```

### GitHub Actions

The tool is designed to run in a workflow. Every time it runs, it generates a fresh "Snapshot" of your repository based on the latest releases of your target projects.

## Setup `apt` on the Client

Once you have hosted the generated files (e.g., in a GitHub Release or GCS), users can add your repo:

```bash
# 1. Add the GPG key
curl -fsSL https://your-repo.com/public.key | sudo gpg --dearmor -o /etc/apt/keyrings/my-repo.gpg

# 2. Add the source
echo "deb [signed-by=/etc/apt/keyrings/my-repo.gpg] https://your-repo.com/stable ./" | sudo tee /etc/apt/sources.list.d/my-repo.list

# 3. Update and install
sudo apt update && sudo apt install my-app-binary
```
