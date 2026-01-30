# apt-repo-builder CLI Documentation

This document provides a detailed reference for the `apt-repo-builder` command-line interface, including environment variables, configuration, and subcommands.

## Environment Variables

The tool relies on two critical environment variables for authentication and security. These must be set in the shell environment where you run the tool. This is the most convenient way to work with Github Actions.

### `GITHUB_TOKEN`

A GitHub Personal Access Token (PAT) used to read private releases (optional) and upload assets (required).

*   **Format**: A standard GitHub token string (e.g., `ghp_...` or `github_pat_...`).
*   **Permissions**:
    *   **Read**: Required for all operations involving `github_projects` in the configuration.
    *   **Write**: Required if you use the `--repo` flag to upload artifacts (indices or binaries).
    *   **Scopes**:
        *   For public repositories: `public_repo` is sufficient.
        *   For private repositories: `repo` (Full control of private repositories) is required.
*   **How to get it**:
    1.  Go to GitHub Settings -> Developer settings -> Personal access tokens -> Tokens (classic).
    2.  Click "Generate new token".
    3.  Select the appropriate scopes (`public_repo` or `repo`).
    4.  Copy the generated token immediately (you won't see it again).

### `GPG_PRIVATE_KEY`

This must be an ASCII-armored GPG private key, it used to sign the repository metadata (the `InRelease` file). If provided, the tool will generate a signed `InRelease` file and a public key files (binary `public.gpg` and armored `public.asc`(legacy)). If omitted, the repository will be unsigned, which requires clients to use `[trusted=yes]` or similar insecure flags to install packages.

*   **Format**: An ASCII-armored block starting with `-----BEGIN PGP PRIVATE KEY BLOCK-----`.
*   **How to get it**:
    1.  Generate a key (if you don't have one):
        ```bash
        gpg --full-generate-key
        # Select RSA and RSA (default), 4096 bits, and no expiration.
        # Enter your name and email (e.g., "Repo Bot <bot@example.com>").
        ```
    2.  Add it to the Github Secrets

## Commands

### `index`

The `index` command scrapes project sources and builds a repository index for all the project sources.

**Usage:**
```bash
apt-repo-builder index [flags]
```

**Flags:**

*   `--config string`
    *   **Description**: Path to the YAML configuration file defining the repository structure and sources.
    *   **Default**: `apt-repo-builder.yaml`

*   `--out string`
    *   **Description**: Local directory where the generated index files (`Packages`, `Packages.gz`, `Release`, `InRelease`, `public.gpg` and `public.asc`) will be written.
    *   **Default**: `dist`
    *   **Note**: This directory is created automatically if it does not exist.

*   `--cache string`
    *   **Description**: Path to a JSON cache file. This file stores hashes and metadata of remote files to speed up subsequent runs and avoid re-downloading large binaries. Works well with Github Actions cache.
    *   **Default**: `repo-cache.json`

*   `--to string` (Optional)
    *   **Description**: The target GitHub release slug to upload the generated indices to.
    *   **Format**: `github.com/owner/repo/tags/tag` (e.g., `github.com/my-org/my-tools/tags/repo`).
    *   **Behavior**: The tool will overwrite existing assets with the same name in this release.

### `add`

The `add` validates candidate `.deb` files against the project repositories to ensure immutability (preventing version conflicts), and optionally uploads them.

**Usage:**
```bash
apt-repo-builder add [flags]
```

**Flags:**

*   `--config string`
    *   **Description**: Path to the YAML configuration file. Used to build a reference index of all configured sources for validation purposes.
    *   **Default**: `apt-repo-builder.yaml`

*   `--src string`
    *   **Description**: Directory containing the candidate `.deb` files.
    *   **Default**: `./build`
    *   **Behavior**: The tool scans for `*.deb` files in this directory.

*   `--cache string`
    *   **Description**: Path to the JSON cache file.
    *   **Default**: `repo-cache.json`

*   `--to string` (Optional)
    *   **Description**: The target GitHub release slug where the **binary** (`.deb`) files should be uploaded.
    *   **Format**: `github.com/owner/repo/tags/tag` (e.g., `github.com/my-org/my-tool/tags/v1.0.0`).

*   `--local-index` (Optional)
    *   **Description**: If set, generates index files in the source directory. Useful for testing the repo locally before uploading.
    *   **Type**: Boolean
    *   **Default**: `false`

*   `--prune` (Optional)
    *   **Description**: If set, deletes local `.deb` files that should not be uploaded.
    *   **Type**: Boolean
    *   **Default**: `false`

## Configuration File (`apt-repo-builder.yaml`)

The configuration file defines the metadata for your repository and the sources you want to include.

### JSON Schema & Autocomplete

You can enable autocomplete and validation in editors like VS Code by referencing the JSON schema. Add the following comment to the top of your YAML file.


```yaml
# yaml-language-server: $schema=./apt-repo-builder.schema.json
# Configuration for apt-repo-builder

# Project configuration: Defines your repository identity and sources.
project:
  # Archive Info: Metadata written to the 'Release' file.
  # These fields are required by APT clients to identify and trust the repository.
  archive_info:
    # Origin: The entity responsible for this repository (e.g., your organization).
    origin: "My Organization"

    # Label: A short label for the repository.
    label: "my-repo"

    # Suite: The release suite (e.g., stable, testing, unstable).
    suite: "stable"

    # Codename: The release codename (e.g., stable, focal, jammy).
    codename: "stable"

    # Architectures: Space-separated list of supported architectures (e.g., "amd64 arm64").
    architectures: "amd64 arm64"

    # Components: Space-separated list of repository components (usually "main").
    components: "main"

    # Description: A human-readable description of the repository.
    description: "My Custom APT Repository"

  # Sources: List of GitHub repositories to scrape for .deb releases.
  # Format: "github.com/<owner>/<repo>"
  sources:
    - "github.com/my-org/my-tool"
    - "github.com/cli/cli"

# Upstream configuration: Defines external repositories for reference.
# These are used to build the "World Index" to ensure your packages don't conflict
# with upstream packages.
upstream:
  sources:
    # Example of an upstream APT repository (e.g., Ubuntu Focal).
    - url: "http://archive.ubuntu.com/ubuntu"
      suite: "focal"
      component: "main"
      architectures:
        - "amd64"
```
