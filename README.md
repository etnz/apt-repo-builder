# deb-pm

UNDER TEST

**deb-pm** is a transactional, serverless Debian repository manager. It allows you to mint, patch, and manage Debian packages and repositories purely from the command line, without requiring `dpkg`, `apt-ftparchive`, or root privileges.

It is designed for modern CI/CD pipelines where repositories are artifacts (tarballs) rather than static directory trees.

## Installation

```bash
go install github.com/etnz/apt-repo-builder/cmd/deb-pm@latest
```

## Library

This tool is built on top of the `deb` package, a pure Go library for manipulating Debian packages and APT repositories in-memory.

*   [GoDoc Documentation](https://pkg.go.dev/github.com/etnz/apt-repo-builder/deb)

## CLI Documentation

For a complete reference of all commands and flags, please see [Documentation.md](Documentation.md).

## Usage Examples

### Simple Configuration Package

Create a simple meta-package from scratch that installs a configuration file.

```bash
deb-pm deb \
  --repo repo.tar.gz \
  --meta Package="my-app" \
  --meta Version="1.0.0" \
  --meta Architecture="all" \
  --meta Maintainer="Dev <deb@example.com>" \
  --meta Description="My awesome CLI tool" \
  --inject "./bin/my-app:/usr/bin/my-app" \
  --mode "0755:/usr/bin/my-app" \
  --conffile "./local.conf:/etc/my-app/local.conf"
```

### Binary Package with Variables

`deb-pm` is designed to be used with shell scripts. By using command-line flags, you can leverage the full power of Bash variables to define your package metadata and content dynamically.

```bash
NAME="my-app"
VERSION="1.0.0"

deb-pm deb \
  --repo repo.tar.gz \
  --meta Package="${NAME}" \
  --meta Version="${VERSION}" \
  --meta Architecture="amd64" \
  --meta Maintainer="Dev <dev@example.com>" \
  --meta Description="My awesome CLI tool" \
  --inject "./bin/${NAME}:/usr/bin/${NAME}" \
  --mode "0755:/usr/bin/${NAME}" \
  --conffile "./local.conf:/etc/${NAME}/local.conf"
```

### Binary Package with Variables And Templates

You can inject variables into your configuration files or scripts at build time using the Go template syntax.

**`local.conf`**
```conf
copyright= (c) {{.NAME}} {{.VERSION}}
```

**Build Command**

```bash
NAME="my-app"
VERSION="1.0.0"

deb-pm deb \
  --repo repo.tar.gz \
  --define NAME="${NAME}" \
  --define VERSION="${VERSION}" \
  --meta Package="${NAME}" \
  --meta Version="${VERSION}" \
  --meta Architecture="amd64" \
  --meta Maintainer="Dev <dev@example.com>" \
  --meta Description="My awesome CLI tool" \
  --inject "./bin/${NAME}:/usr/bin/${NAME}" \
  --mode "0755:/usr/bin/${NAME}" \
  --conffile-tpl "./local.conf:/etc/${NAME}/local.conf"
```
