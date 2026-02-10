// Package manifest provides functionality to define and build APT repositories using declarative configuration files.
package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/etnz/apt-repo-builder/deb"
	"go.yaml.in/yaml/v3"
)

// NewRepository loads and parses a Repository configuration from the specified file path.
// It supports both JSON and YAML formats based on the file extension.
func NewRepository(path string) (*Repository, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read archivefile: %w", err)
	}

	var archive Repository
	if err := unmarshal(path, content, &archive); err != nil {
		return nil, fmt.Errorf("failed to parse archivefile: %w", err)
	}

	archive.filePath = path
	archive.engine, err = newTemplateEngine(archive.Defines)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize template engine: %w", err)
	}

	if archive.Path == "" {
		return nil, fmt.Errorf("archivefile must specify 'repo'")
	}
	return &archive, nil
}

// Repository represents the configuration for an APT repository archive.
// It defines the output directory, global variables, and the list of packages to include.
type Repository struct {
	// Path is the directory path where the repository will be generated.
	Path string `json:"path" yaml:"path"`
	// Defines is a map of global variables available to templates.
	Defines map[string]string `json:"defines" yaml:"defines"`
	// Packages is a list of paths to package definition files to include in the repository.
	Packages []string `json:"packages" yaml:"packages"`

	filePath string
	engine   *templateEngine
}

// LoadRepository initializes the underlying deb.Repository from the configured Path.
// If the directory does not exist, it creates a new empty repository in memory.
func (a *Repository) LoadRepository() (*deb.Repository, error) {
	repo, err := deb.NewRepositoryFromDir(a.resolve(a.Path))
	if err != nil {
		if os.IsNotExist(err) {
			return &deb.Repository{
				ArchiveInfo: deb.ArchiveInfo{
					Origin: "deb-pm",
					Label:  "Managed Repository",
				},
			}, nil
		}
		return nil, err
	}
	return repo, nil
}

// LoadPackages reads and parses all package definition files listed in the configuration.
// It resolves paths relative to the Repository file and initializes template engines for each package.
func (a *Repository) LoadPackages() ([]Package, error) {
	var pkgs []Package

	for _, pkgFileRaw := range a.Packages {
		// pkgFile can be
		//  - a relative path to the repository file
		//  - an absolute file path on the machine
		//  - a URL
		//
		// We want to find the resource, or course, but also get a valid string for error message
		pkgFile, err := a.engine.render("package-list", pkgFileRaw)
		if err != nil {
			return nil, fmt.Errorf("rendering package path %q: %w", pkgFileRaw, err)
		}
		pkgPath := a.resolve(pkgFile)

		if strings.HasSuffix(strings.ToLower(pkgPath), ".deb") {
			eng, err := a.engine.sub(nil)
			if err != nil {
				return nil, fmt.Errorf("failed to create engine for %s: %w", pkgPath, err)
			}
			pkg := Package{
				Input:    pkgPath,
				filePath: pkgPath,
				engine:   eng,
			}
			pkgs = append(pkgs, pkg)
			continue
		}

		pkgContent, err := a.loadResource(pkgFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read package definition %s: %v", pkgPath, err)
		}

		var pkg Package
		if err := unmarshal(pkgFile, []byte(pkgContent), &pkg); err != nil {
			return nil, fmt.Errorf("failed to parse package definition %s: %v", pkgPath, err)
		}

		pkg.engine, err = a.engine.sub(pkg.Defines)
		if err != nil {
			return nil, fmt.Errorf("failed to process defines for %s: %w", pkgPath, err)
		}

		// if the file path is a URL, use
		pkg.filePath = pkgPath
		pkgs = append(pkgs, pkg)
	}

	return pkgs, nil
}

// Compile orchestrates the repository building process.
// It loads the repository, processes all packages, applies them, and saves the result.
func (a *Repository) Compile(gpgKey string, l Listener) error {
	if l == nil {
		l = func(fmt.Stringer) {}
	}

	repo, err := a.LoadRepository()
	if err != nil {
		return fmt.Errorf("failed to load repo: %w", err)
	}
	l(EventRepositoryLoadSuccess{Path: a.Path})

	repo.GPGKey = gpgKey

	pkgs, err := a.LoadPackages()
	if err != nil {
		return fmt.Errorf("failed to load packages: %w", err)
	}

	for _, pkg := range pkgs {
		debPkg, err := pkg.Apply(repo)
		if err != nil {
			return fmt.Errorf("failed to apply package %q: %w", pkg.filePath, err)
		}
		if debPkg != nil {
			l(EventPackageApplySuccess{
				FilePath:     pkg.filePath,
				Package:      debPkg.Metadata.Package,
				Version:      debPkg.Metadata.Version,
				Architecture: debPkg.Metadata.Architecture,
			})
		} else {
			// Should not happen if err is nil, but safe fallback
			l(EventPackageApplySuccess{FilePath: pkg.filePath})
		}
	}

	ops, err := a.SaveRepository(repo)
	if err != nil {
		return fmt.Errorf("failed to save repo: %w", err)
	}

	for _, op := range ops {
		l(EventFileOperation{
			Path:      op.Path,
			OldDigest: op.OldDigest,
			NewDigest: op.NewDigest,
			Created:   op.OldDigest == "",
			Updated:   op.OldDigest != "" && op.OldDigest != op.NewDigest,
		})
	}
	l(EventRepositorySaveSuccess{Path: a.Path})

	return nil
}

// SaveRepository writes the current state of the deb.Repository to the configured Path.
func (a *Repository) SaveRepository(repo *deb.Repository) ([]deb.FileOperation, error) {
	return repo.WriteToDir(a.resolve(a.Path))
}

func (a *Repository) resolve(path string) string {
	if filepath.IsAbs(path) || strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return filepath.Join(filepath.Dir(a.filePath), path)
}

func (a *Repository) loadResource(path string) (string, error) {
	resolved := a.resolve(path)
	content, err := os.ReadFile(resolved)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// unmarshal parses JSON or YAML based on file extension.
func unmarshal(path string, data []byte, v interface{}) error {
	ext := strings.ToLower(filepath.Ext(path))
	r := bytes.NewReader(data)
	if ext == ".yaml" || ext == ".yml" {
		dec := yaml.NewDecoder(r)
		dec.KnownFields(true)
		return dec.Decode(v)
	}
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
