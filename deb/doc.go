// Package deb provides a pure Go library for manipulating Debian packages and APT repositories.
//
// # Design Philosophy
//
// The package is designed to operate primarily in-memory, treating Debian packages and
// repositories as structured objects that can be read from, modified, and written to
// streams (io.Reader/io.Writer). This approach eliminates the need for temporary
// files or external system dependencies like 'dpkg' or 'apt-ftparchive', making it
// ideal for serverless environments, CI/CD pipelines, and cross-platform tools.
//
// # Features
//
// Package Management:
//   - Read and parse .deb files from any io.Reader.
//   - Create new packages from scratch or patch existing ones.
//   - Modify control metadata, maintainer scripts, and payload files.
//   - Generate valid .deb archives deterministically.
//
// Repository Management:
//   - Create and manage APT repositories in-memory.
//   - Support for both flat and standard (hierarchical) repository layouts.
//   - Automatic generation of indices: Packages, Packages.gz, Release.
//   - GPG signing of Release files (InRelease) using Go's openpgp.
//   - Import existing repositories from tar.gz streams.
//
// Versioning:
//   - Implements Debian version comparison logic.
//   - Utilities for intelligent version bumping (upstream and iteration).
package deb
