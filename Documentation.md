# apt-repo-builder CLI Documentation

This document provides a detailed reference for the `apt-repo-builder` command-line interface, including environment variables, configuration, and subcommands.

## Environment Variables

The tool relies on two critical environment variables for authentication and security. These must be set in the shell environment where you run the tool.

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

### `GPG_PRIVATE_KEY` (Optional but Recommended)

The ASCII-armored GPG private key used to sign the repository metadata (`InRelease`). If provided, the tool will generate a signed `InRelease` file and a `public.key` file. If omitted, the repository will be unsigned, which requires clients to use `[trusted=yes]` or similar insecure flags to install packages.

*   **Format**: An ASCII-armored block starting with `-----BEGIN PGP PRIVATE KEY BLOCK-----`.
*   **How to get it**:
    1.  Generate a key (if you don't have one):
        ```bash
        gpg --full-generate-key
        # Select RSA and RSA (default), 4096 bits, and no expiration.
        # Enter your name and email (e.g., "Repo Bot <bot@example.com>").
        ```
    2.  Export the private key:
        ```bash
        # List keys to find the ID (e.g., ABC12345...)
        gpg --list-secret-keys --keyid-format LONG
        
        # Export (replace KEY_ID with your key's ID)
        gpg --armor --export-secret-keys KEY_ID > private.key
        ```
    3.  Set the variable:
        ```bash
        export GPG_PRIVATE_KEY=$(cat private.key)
        ```

## Commands

### `index-all`

The `index-all` command is the "heavy lifter" used primarily for the **Aggregator** use case. It scrapes all configured sources (standard APT repos and GitHub projects), downloads necessary metadata, and builds a unified repository index.

**Usage:**
```bash
apt-repo-builder index-all [flags]
```

**Flags:**

*   `--config string`
    *   **Description**: Path to the YAML configuration file defining the repository structure and sources.
    *   **Default**: `apt-repo-builder.yaml`
    *   **Format**: Relative or absolute file path.

*   `--out string`
    *   **Description**: Local directory where the generated index files (`Packages`, `Packages.gz`, `Release`, `InRelease`, `public.key`) will be written.
    *   **Default**: `dist`
    *   **Note**: This directory is created automatically if it does not exist.

*   `--cache string`
    *   **Description**: Path to the JSON cache file. This file stores hashes and metadata of remote files to speed up subsequent runs and avoid re-downloading large binaries.
    *   **Default**: `repo-cache.json`

*   `--repo string` (Optional)
    *   **Description**: The target GitHub repository to upload the generated indices to.
    *   **Format**: `owner/repo` (e.g., `my-org/my-tools`).
    *   **Requirement**: Must be used in conjunction with `--index-tag`.

*   `--index-tag string` (Optional)
    *   **Description**: The GitHub Release tag where the index files should be uploaded.
    *   **Format**: A string tag name (e.g., `repo`, `stable`, `v1`).
    *   **Behavior**: The tool will overwrite existing assets with the same name in this release.

### `push-deb`

The `push-deb` command is for the **Project Maintainer** workflow. It takes local `.deb` files, validates them against the existing repository to ensure immutability (preventing version conflicts), uploads them, and then updates the repository index.

**Usage:**
```bash
apt-repo-builder push-deb [flags]
```

**Flags:**

*   `--config string`
    *   **Description**: Path to the YAML configuration file. Used to read `archive_info` for index generation and to build a "master index" of all configured sources for validation purposes.
    *   **Default**: `apt-repo-builder.yaml`

*   `--src string`
    *   **Description**: Directory containing the local `.deb` files you want to publish.
    *   **Default**: `./build`
    *   **Behavior**: The tool scans for `*.deb` files in this directory.

*   `--cache string`
    *   **Description**: Path to the JSON cache file.
    *   **Default**: `repo-cache.json`

*   `--repo string` (Required)
    *   **Description**: The target GitHub repository where binaries and indices will be hosted.
    *   **Format**: `owner/repo`.

*   `--tag string` (Required)
    *   **Description**: The GitHub Release tag where the **binary** (`.deb`) files should be uploaded.
    *   **Format**: A version tag (e.g., `v1.0.0`).
    *   **Note**: This is typically the release tag for the specific version of the software you are publishing.

*   `--index-tag string` (Required)
    *   **Description**: The GitHub Release tag where the **index** files (`Packages`, `Release`, etc.) reside.
    *   **Format**: A stable tag name (e.g., `repo`).
    *   **Behavior**: The tool fetches the existing index from this tag, merges the new packages, and uploads the updated index back to this tag.

## Configuration File (`apt-repo-builder.yaml`)

The configuration file defines the metadata for your repository and the sources you want to include.

*   **`archive_info`**: Defines the APT metadata (Origin, Label, Codename, etc.) that appears in the `Release` file.
*   **`repositories`**: A list of standard APT repositories to scrape (e.g., upstream Ubuntu/Debian repos).
*   **`github_projects`**: A list of GitHub repositories to scrape for `.deb` assets.

Example:
```yaml
github_projects:
  - owner: "cli"
    name: "cli"
```
