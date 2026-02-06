package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/etnz/apt-repo-builder/deb"
)

// Package represents the definition of a Debian package.
// It contains metadata, file injections, scripts, and other build instructions
// loaded from a configuration file.
type Package struct {
	// Input is the path to an optional source .deb package to patch.
	Input string `json:"input" yaml:"input"`
	// Defines is a map of local variables available to templates in this package.
	Defines map[string]string `json:"defines" yaml:"defines"`
	// Meta contains fields to set or override in the package control file.
	Meta map[string]string `json:"meta" yaml:"meta"`
	// Injects is a list of files to add to the package payload.
	Injects []File `json:"injects" yaml:"injects"`
	// Scripts is a list of maintainer scripts to add to the package.
	Scripts []File `json:"scripts" yaml:"scripts"`
	// ControlFiles is a list of auxiliary control files to add.
	ControlFiles []File `json:"control_files" yaml:"control_files"`

	filePath string
	engine   *templateEngine
}

func (p *Package) resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(filepath.Dir(p.filePath), path)
}

func (p *Package) loadResource(path string, raw bool) (string, error) {
	resolved := p.resolve(path)
	content, err := os.ReadFile(resolved)
	if err != nil {
		return "", err
	}
	if raw {
		return string(content), nil
	}
	return p.engine.render(path, string(content))
}

// File represents a file resource to be injected into the package.
type File struct {
	// Src is the path to the source file (relative to the package definition file).
	Src string `json:"src" yaml:"src"`
	// Dst is the absolute path where the file will be installed on the target system.
	Dst string `json:"dst" yaml:"dst"`
	// Raw indicates whether the file should be treated as raw content (true) or processed as a template (false).
	Raw bool `json:"raw" yaml:"raw"`
	// Mode is the file permissions in octal string format (e.g., "0755").
	Mode string `json:"mode" yaml:"mode"`
	// Conffile indicates if the file should be marked as a configuration file.
	Conffile bool `json:"conffile" yaml:"conffile"`
}

// Apply generates a deb.Package from the definition and adds it to the provided repository.
// It renders templates, loads resources, and populates the package structure.
func (p *Package) Apply(repo *deb.Repository) (*deb.Package, error) {
	input, err := p.engine.render("input", p.Input)
	if err != nil {
		return nil, fmt.Errorf("rendering input: %w", err)
	}

	var pkg *deb.Package
	if input == "" {
		pkg = &deb.Package{Metadata: deb.Metadata{ExtraFields: make(map[string]string)}}
	} else {
		f, err := os.Open(p.resolve(input))
		if err != nil {
			return nil, fmt.Errorf("reading input: %w", err)
		}
		defer f.Close()
		pkg, err = deb.NewPackage(f)
		if err != nil {
			return nil, fmt.Errorf("reading input: %w", err)
		}
	}

	for k, v := range p.Meta {
		val, err := p.engine.render("meta."+k, v)
		if err != nil {
			return nil, fmt.Errorf("rendering meta %s: %w", k, err)
		}
		pkg.Set(k, val)
	}

	for i, f := range p.Injects {
		src, err := p.engine.render(fmt.Sprintf("injects[%d].src", i), f.Src)
		if err != nil {
			return nil, err
		}
		dst, err := p.engine.render(fmt.Sprintf("injects[%d].dst", i), f.Dst)
		if err != nil {
			return nil, err
		}

		var mode int64 = 0644
		if f.Mode != "" {
			modeStr, err := p.engine.render(fmt.Sprintf("injects[%d].mode", i), f.Mode)
			if err != nil {
				return nil, err
			}
			mode, err = strconv.ParseInt(modeStr, 8, 64)
			if err != nil {
				return nil, fmt.Errorf("parsing mode %s: %w", modeStr, err)
			}
		}

		content, err := p.loadResource(src, f.Raw)
		if err != nil {
			return nil, err
		}
		pkg.Files = append(pkg.Files, deb.File{
			DestPath: dst,
			Mode:     mode,
			Body:     content,
			IsConf:   f.Conffile,
		})
	}

	for i, f := range p.Scripts {
		src, err := p.engine.render(fmt.Sprintf("scripts[%d].src", i), f.Src)
		if err != nil {
			return nil, err
		}
		dst, err := p.engine.render(fmt.Sprintf("scripts[%d].dst", i), f.Dst)
		if err != nil {
			return nil, err
		}
		content, err := p.loadResource(src, f.Raw)
		if err != nil {
			return nil, err
		}

		switch dst {
		case "preinst":
			pkg.Scripts.PreInst = content
		case "postinst":
			pkg.Scripts.PostInst = content
		case "prerm":
			pkg.Scripts.PreRm = content
		case "postrm":
			pkg.Scripts.PostRm = content
		case "config":
			pkg.Scripts.Config = content
		default:
			return nil, fmt.Errorf("unknown script dst: %s", dst)
		}
	}

	for i, f := range p.ControlFiles {
		src, err := p.engine.render(fmt.Sprintf("control_files[%d].src", i), f.Src)
		if err != nil {
			return nil, err
		}
		dst, err := p.engine.render(fmt.Sprintf("control_files[%d].dst", i), f.Dst)
		if err != nil {
			return nil, err
		}
		content, err := p.loadResource(src, f.Raw)
		if err != nil {
			return nil, err
		}
		if pkg.ExtraControlFiles == nil {
			pkg.ExtraControlFiles = make(map[string]string)
		}
		pkg.ExtraControlFiles[dst] = content
	}

	existing, err := repo.Append(pkg)
	if err != nil {
		return nil, err
	}

	if existing != nil {
		return existing, nil
	}
	return pkg, nil
}
