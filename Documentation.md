

## Configuration Reference

### Repository Configuration

The repository manifest (usually `repository.yml`) defines the output location, global variables, and the list of packages to include.

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/etnz/apt-repo-builder/master/repository.schema.json

# The directory where the repository structure (dists/, pool/) will be generated or updated.
# Can be relative to this file or absolute.
path: "dist"

# Global variables available to all templates in the configuration.
# These can be referenced as {{ .VAR_NAME }} in this file and in package files.
defines:
  # Key, value pair.
  KEY1: "value"

  # Value can reference other variables.
  KEY2: "{{ .KEY1 }}_plus"

# List of packages to include in the repository.
# Entries can be absolute or relative paths to manifest files to generate a .deb file or paths to .deb files that will be included.
# path can be a file path or a web URL (http, https)
packages:
  # 1. Reference to a local package manifest file (YAML/JSON).
  - "my-component.yml"

  # 2. Reference to a remote package manifest file.
  - "https://raw.githubusercontent.com/org/repo/main/packages/remote-tool.yml"

  # 3. Direct inclusion of a local .deb file (no manifest needed).
  # The file is copied as-is into the repository.
  - "binaries/legacy-tool_1.0.0_amd64.deb"

  # 4. Direct inclusion of a remote .deb file.
  # The file is downloaded and included.
  - "https://github.com/org/releases/download/v1.0.0/tool.deb"

  # 5. Using variables in paths.
  - "{{ .BASE_URL }}/plugin-{{ .VERSION }}.deb"
```

### Package Configuration

Package files (e.g., `my-package.yml`) define how to build or patch a single Debian package.

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/etnz/apt-repo-builder/master/package.schema.json

# Optional: Start from an existing .deb file to patch it.
# If omitted, an empty package is created from scratch.
input: "base-package.deb"

# Local variables for this package.
# Can reference global defines from repository.yml.
defines:
  # Key, value pair.
  LOCAL_KEY1: "my-app"
  # Local variables can reference global variables.
  LOCAL_KEY2: "{{ .KEY1 }}_plus"
  # Local variables can reference other local variables. The order doesn't matter as long as there is no cycles.
  LOCAL_KEY3: "{{ .LOCAL_KEY1 }}_plus"


# Metadata fields for the Debian control file.
meta:
  Package: "{{ .APP_NAME }}"
  Version: "{{ .VERSION }}" # Inherited from global defines
  Architecture: "amd64"
  Maintainer: "Jane Doe <jane@example.com>"
  Description: |
    My Application
    This is a longer description of the application.
    It supports multiline strings.

  # Classification
  Section: "utils"            # Application category (e.g., utils, web, net)
  Priority: "optional"        # Importance (optional, extra, required)

  # Links & Source
  Homepage: "https://example.com"
  Source: "my-app-src"        # Source package name (if different)

  # Relationships
  Depends: "curl, libc6"      # Absolute dependencies required to run
  Pre-Depends: "tar"          # Required before installation script runs
  Recommends: "wget"          # Installed by default, but not strictly required
  Suggests: "doc-pkg"         # Related but not installed by default
  Enhances: "other-pkg"       # Enhances functionality of another package
  Conflicts: "old-app"        # Cannot be installed with this package
  Breaks: "lib-old (< 1.0)"   # Breaks specific versions of other packages
  Replaces: "old-app"         # Replaces files from another package
  Provides: "virtual-browser" # Provides a virtual package capability

  # Advanced
  Essential: "no"             # If yes, removal requires confirmation (dangerous)
  Built-Using: "go-1.21"      # For static binaries, tracking source dependencies

  # Custom fields are allowed and will be added to the control file.
  X-Custom-Field: "custom-value"

# Files to add to the package payload (data.tar.gz).
#
# By default, files are processed as templates.
# Use raw: true to disable templating for a specific file.
injects:
  # Simple file injection
  - src: "./bin/app"          # Local source path, relative to this file or absolute, or web URL. It's content will be treated as a template.
    dst: "/usr/bin/my-app"    # Absolute path on target system
    mode: "0755"              # Octal permissions (string)

  # Configuration file with templating
  - src: "./configs/app.conf"
    dst: "/etc/my-app/app.conf"
    conffile: true            # Mark as configuration file (dpkg will prompt on overwrite)

  # Download and inject a file from a URL
  - src: "https://example.com/assets/logo.png"
    dst: "/usr/share/my-app/logo.png"
    mode: "0644"
    raw: true                 # Binary files should usually be raw to avoid template errors

# Maintainer scripts (control.tar.gz).
scripts:
  - src: "./scripts/postinst.sh"
    dst: "postinst"           # Must be one of: preinst, postinst, prerm, postrm, config

# Auxiliary control files (control.tar.gz).
control_files:
  - src: "./triggers"
    dst: "triggers"           # Filename in the control archive

## Repository Integrity & Development Workflow

### Immutability and Errors

`deb-pm` is designed to protect the integrity of your repository. It enforces a strict rule: **You cannot overwrite an existing package version with different content.**

If you run `deb-pm` multiple times on the same configuration without changing anything, it will succeed (idempotent). However, if you modify a package definition (e.g., change a script or a file) but keep the same `Version` in the metadata, `deb-pm` will fail with an error stating that the package already exists.

**Why?**
APT repositories rely on checksums (SHA256) listed in the `Packages` index to verify downloaded `.deb` files. If you change a `.deb` file in place without updating the version:
1.  Clients with an old `Packages` index will fail to verify the new file (checksum mismatch).
2.  Caching proxies and mirrors may serve the old file or a corrupted mix.
3.  It breaks the principle of immutable releases.

### Recommended Workflows

#### 1. The Release Flow
When you make changes to a package, the standard practice is to **bump the version number** in your YAML configuration (e.g., change `1.0.0` to `1.0.1`). This creates a new package file, which `deb-pm` happily adds to the repository index.

#### 2. The Local Dev Loop
When developing (e.g., debugging a `postinst` script), you might not want to bump the version for every trial. To iterate on the *same* version locally, you must reset the repository to its state *before* that version was added (or to a state where that version matches your new build).

Since `deb-pm` operates directly on the output directory, it doesn't know what the "original" state was. You need to handle this reset.

**Using Git (Recommended)**
If your `dist` folder is tracked in git (as an artifact repository), you can use git to reset the state before building.

```bash
# 1. Reset the dist folder to the remote/clean state
git checkout HEAD -- dist/
git clean -fd dist/

# 2. Run the build with your changes
deb-pm repo.yml
```

**Using File Copies**
If you are not using git, you can maintain a "clean" copy of the previous repository state.

```bash
# 1. Restore dist from a backup/previous state
rsync -a --delete dist-prev/ dist/

# 2. Run the build
deb-pm repo.yml
```
```
