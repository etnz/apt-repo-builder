CURRENTLY UNDER TEST

# apt-repo-builder


**apt-repo-builder** is a stateless, "serverless" tool that turns GitHub Releases into fully functional APT repositories.

It decouples metadata from payload:
*   **Metadata** (`Packages`, `Release`, `InRelease`) is hosted on a single named GitHub Release (e.g tag "repo").
*   **Payloads** (`.deb` files) are served directly from GitHub's global CDN (via release asset URLs).

For detailed CLI usage, environment variables, and configuration options, see the Documentation.

## Use Case 1: The Project Maintainer

You build a tool (e.g., with GoReleaser or nfpm) and attach `.deb` files to your GitHub Releases. You want users to install and update your tool via `apt` without setting up complex infrastructure like Artifactory or Aptly.

### Workflow
1.  Build your `.deb` package.
2.  Run `apt-repo-builder push-deb` in your CI pipeline.

This command:
1.  **Verifies** your local `.deb` against the existing repository (ensuring version immutability).
2.  **Uploads** the `.deb` to your target Release.
3.  **Regenerates and signs** the APT indices (`Packages`, `Release`, `InRelease`).
4.  **Publishes** the indices to a specific "index" tag (e.g., `repo`).

### Example (GitHub Actions)

```bash
# Assuming you have built ./dist/my-tool_1.0.0_amd64.deb
apt-repo-builder push-deb \
  --config apt-repo-builder.yaml \
  --src ./dist \
  --repo my-org/my-tool \
  --tag v1.0.0 \
  --index-tag repo
```



## Use Case 2: The Aggregator (Personal Repository)

You want a single APT repository that contains your favorite tools (e.g., `cli/gh`, `sharkdp/bat`, `BurntSushi/ripgrep`), even if the authors don't provide an APT repo themselves.

**apt-repo-builder** can scrape multiple GitHub projects, aggregate their releases, and build a master index for you.

### Workflow
1.  Define your sources in `apt-repo-builder.yaml`.
2.  Run `apt-repo-builder index-all`.

This command:
1.  **Scrapes** the latest `.deb` assets from the configured GitHub projects.
2.  **Downloads** metadata (caching heavily to avoid redownloading binaries).
3.  **Builds** a unified APT index.
4.  **Publishes** the index to your personal repository.

### Configuration (`apt-repo-builder.yaml`)

```yaml
# Your repository metadata
archive_info:
  origin: "MyPersonalRepo"
  label: "MyTools"
  suite: "stable"
  codename: "stable"
  architectures: "amd64 arm64"
  components: "main"
  description: "My collection of essential CLI tools"

# Projects to aggregate
github_projects:
  - owner: "cli"
    name: "cli"
  - owner: "sharkdp"
    name: "bat"
```

## Configuration

See [Documentation](Documentation.md) for full configuration reference.

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

See [Documentation](Documentation.md) for full command reference.

### Local Execution

```bash
export GITHUB_TOKEN="your_token"
export GPG_PRIVATE_KEY=$(cat private.key)
go run main.go --config apt-repo-config.yaml --out ./public --cache-file repo-cache.json
```

### GitHub Actions

The tool is designed to run in a workflow. Every time it runs, it generates a fresh "Snapshot" of your repository based on the latest releases of your target projects.

## Setup `apt` on the Client

Once you have hosted the generated files users can add your repo:

```bash
# 1. Add the GPG key
curl -fsSL https://github.com/<owner>/<repo>/releases/download/<index-tag>/public.key | sudo gpg --dearmor -o /etc/apt/keyrings/my-repo.gpg

# 2. Add the source
echo "deb [signed-by=/etc/apt/keyrings/my-repo.gpg] https://github.com/<owner>/<repo>/releases/download/<index-tag>/ ./" | sudo tee /etc/apt/sources.list.d/my-repo.list

# 3. Update and install
sudo apt update && sudo apt install my-app-binary
```
