# deb-pm

UNDER TEST

**deb-pm** is a transactional, serverless Debian repository manager. It allows you to mint, patch, and manage Debian packages and repositories purely from the command line, without requiring `dpkg`, `apt-ftparchive`, or root privileges.

It is designed for modern CI/CD pipelines where repositories are artifacts.

## Installation

```bash
go install github.com/etnz/apt-repo-builder/cmd/deb-pm@latest
```

## Library

This tool is built on top of the `deb` package, a pure Go library for manipulating Debian packages and APT repositories in-memory.

*   [GoDoc Documentation](https://pkg.go.dev/github.com/etnz/apt-repo-builder/deb)

## Usage

```shell
$ deb-pm [repositoryfile]
```

If no file is specified, `deb-pm` looks for `repository.yml`, `repository.yaml`, or `repository.json` in the current directory.


## Usage Examples

### Declarative Repository

Define your repository and packages in YAML configuration files and build them in one go.

**`repo.yml`**
```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/etnz/apt-repo-builder/master/repository.schema.json

# the local folder that contains the current version of the repository (empty supported)
path: "dist"

# defines global variables that are available everywhere in the configuration.
defines:
  VERSION: "1.0.0"

# packages is a list of package manifest files in yaml|json file or directly .deb files to be included.
packages:
  - my-package.yml
```

**`my-package.yml`**
```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/etnz/apt-repo-builder/master/package.schema.json

# manifest file to generate a .deb package file.

# meta adds debian metadata info.
meta:
  Package: "my-app"
  Version: "{{ .VERSION }}" # values are treated as Go templates.
  Architecture: "amd64"
  Maintainer: "Dev <dev@example.com>"
  Description: "My awesome CLI tool"

# injects add files in the data archive.
injects:
  - src: "./bin/my-app"
    dst: "/usr/bin/my-app"
    mode: "0755"
  - src: "./local.conf" # note that the local.conf will be treated as a template too.
    dst: "/etc/my-app/local.conf" 
    conffile: true
```

**Build**

```bash
deb-pm repo.yml
```
