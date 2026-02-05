# deb-pm CLI Documentation

This document provides a detailed reference for the `deb-pm` command-line interface.

## Commands

### `deb`

The `deb` command is the core of the tool. It allows you to mint new packages from scratch, patch existing ones, and manage the repository index.

**Usage:**
```bash
deb-pm deb [flags]
```

**Core Flags:**

*   `--repo string` (Required)
    *   Path to the repository tarball (e.g., `repo.tar.gz`). If it doesn't exist, a new one is created.

*   `--input string`
    *   Path or URL to a source `.deb` package to patch. If omitted, starts from an empty package.

*   `--strategy string`
    *   Conflict resolution strategy: `safe`, `bump`, `strict`, `overwrite`.
    *   Default: `strict`

*   `--prune`
    *   Enable pruning logic to enforce retention policies.

**Metadata Flags:**

*   `--meta Key=Value`
    *   Set a field in the `control` file (e.g., `Package=my-tool`, `Version=1.0.0`, `Depends=curl`).
    *   Can be repeated.
    *   Reference: Debian Policy: [Control files and their fields](https://www.debian.org/doc/debian-policy/ch-controlfields.html)

**Content Injection Flags:**

*   `--inject src:dst`
    *   Add a file to the package payload.
    *   `src`: Local path or URL.
    *   `dst`: Absolute path on the target system.

*   `--inject-tpl src:dst`
    *   Same as `--inject`, but processes the file as a Go template.

*   `--conffile src:dst`
    *   Add a configuration file (automatically added to `conffiles` list).
    *   Reference: Debian Policy: [Configuration files](https://www.debian.org/doc/debian-policy/ch-files.html#configuration-files)

*   `--conffile-tpl src:dst`
    *   Same as `--conffile`, but processes as a template.

*   `--mode mode:dst`
    *   Set the file permissions for a specific destination path (e.g., `0755:/usr/bin/my-tool`).
    *   **Important:** Binaries and executable scripts must be executable (typically `0755`). See Debian Policy: Permissions and owners.

**Script Flags:**

*   `--script src:dst`
    *   Inject a maintainer script.
    *   `dst` must be one of: `preinst`, `postinst`, `prerm`, `postrm`, `config`.
    *   Reference: Debian Policy: [Maintainer Scripts](https://www.debian.org/doc/debian-policy/ch-binary.html#maintainer-scripts)

*   `--script-tpl src:dst`
    *   Same as `--script`, but processes as a template.

**Control File Flags:**

**Warning:** In Debian terminology, "Control Files" refers to any file in the control archive (e.g., `postinst`, `md5sums`, `triggers`), not just the main metadata file named `control`. This naming collision is unfortunate but standard. See Debian Policy: Binary package control files.

*   `--control src:dst`
    *   Inject an auxiliary control file (e.g., `triggers`, `templates`).
    *   Note: `control`, `conffiles`, `md5sums`, and maintainer scripts (`postinst`, etc.) are handled automatically or via specific flags and cannot be overwritten here.

*   `--control-tpl src:dst`
    *   Same as `--control`, but processes as a template.

**Context Flags:**

*   `--define KEY=VALUE`
    *   Define a variable available to templates.

### `purge`

Cleanup the repository based on retention policies.

*Not yet implemented.*
