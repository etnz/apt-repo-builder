package deb

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blakesmith/ar"
)

// Package represents the comprehensive definition of a Debian binary package.
// It separates metadata (Control), hooks (Scripts), and payload (Files).
type Package struct {
	Metadata Metadata
	Scripts  Scripts
	Files    []File

	// ExtraControlFiles contains arbitrary control files to be added to the control archive.
	// Keys are filenames (e.g., "templates", "conffiles", "triggers"), values are the content.
	// Reserved names ("control", "md5sums", "conffiles", "preinst", "postinst", "prerm", "postrm", "config") are ignored.
	ExtraControlFiles map[string]string

	originalContentDigest string
	onDiskDigest          string
}

// Metadata maps directly to the fields in the Debian 'control' file.
//
// Reference: https://www.debian.org/doc/debian-policy/ch-controlfields.html#binary-package-control-files-debian-control
type Metadata struct {
	// Package is the name of the package. It must consist only of lower case
	// letters (a-z), digits (0-9), plus (+) and minus (-) signs, and periods (.).
	// It must be at least two characters long and must start with an alphanumeric character.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-controlfields.html#s-f-package
	Package string

	// Version is the version number of the package. The format is: [epoch:]upstream_version[-debian_revision].
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-controlfields.html#s-f-version
	Version string

	// Architecture specifies the hardware architecture the package is compiled for.
	// Common values: "amd64", "arm64". Use "all" for architecture-independent packages (e.g. scripts, docs).
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-controlfields.html#s-f-architecture
	Architecture string

	// Maintainer is the name and email address of the person responsible for this package.
	// Format: "Name <email@address.com>".
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-controlfields.html#s-f-maintainer
	Maintainer string

	// Description contains the package synopsis and extended description.
	// The first line is the synopsis (short description). Subsequent lines are the extended description.
	// Note: The packager logic must handle the specific indentation rules (preceding spaces) for the extended body.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-controlfields.html#s-f-description
	Description string

	// Section classifies the package into a category (e.g., "utils", "web", "devel").
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-controlfields.html#s-f-section
	Section string

	// Priority represents the importance of this package (e.g., "optional", "required", "extra").
	// Most user-created packages should be "optional".
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-controlfields.html#s-f-priority
	Priority string

	// Homepage is the URL of the upstream project's home page.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-controlfields.html#s-f-homepage
	Homepage string

	// Essential, if set to true, indicates that the package is essential for the system to function.
	// Users are warned if they try to remove it. Use with extreme caution.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-controlfields.html#s-f-essential
	Essential bool

	// Depends lists packages that must be installed for this package to provide a significant amount of functionality.
	// Format: "package-name (>= version)".
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-relationships.html#s-binarydeps
	Depends []string

	// PreDepends lists packages that must be installed and configured *before* the installation of this package can be attempted.
	// Only use this if absolutely necessary (e.g., if a preinst script relies on another package).
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-relationships.html#s-binarydeps
	PreDepends []string

	// Recommends lists packages that would be found together with this one in all but unusual installations.
	// apt-get installs these by default.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-relationships.html#s-binarydeps
	Recommends []string

	// Suggests lists packages that are related to this one and can enhance its usefulness, but are not necessary.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-relationships.html#s-binarydeps
	Suggests []string

	// Enhances is the reverse of Suggests. It indicates that this package enhances the functionality of the listed packages.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-relationships.html#s-binarydeps
	Enhances []string

	// Conflicts lists packages that cannot be unpacked or installed while this package is installed.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-relationships.html#s-conflicts
	Conflicts []string

	// Breaks lists packages that this package breaks. It is a weaker form of Conflicts.
	// It allows the broken package to remain unpacked but not configured.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-relationships.html#s-breaks
	Breaks []string

	// Replaces lists packages whose files are overwritten by this package.
	// Used in conjunction with Conflicts or Breaks to handle file path collisions.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-relationships.html#s-replaces
	Replaces []string

	// Provides lists virtual packages that this package provides (e.g., "www-browser").
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-relationships.html#s-virtual
	Provides []string

	// BuiltUsing identifies the source packages used to build this binary package.
	// Crucial for Go static binaries to track security of dependencies.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-controlfields.html#s-f-built-using
	BuiltUsing string

	// Source identifies the source package name if it differs from the binary package name.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-controlfields.html#s-f-source
	Source string

	// ExtraFields holds any custom or non-standard fields that should be written to the control file.
	// Examples include "Bugs", "Origin", or internal metadata.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-controlfields.html#user-defined-fields
	ExtraFields map[string]string
}

// Scripts holds the executable maintainer scripts.
// These are executed by dpkg at different stages of the package lifecycle.
//
// Reference: https://www.debian.org/doc/debian-policy/ch-maintainerscripts.html
type Scripts struct {
	// PreInst is the script executed before the package is unpacked.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-maintainerscripts.html#s-mscriptsinstact
	PreInst string

	// PostInst is the script executed after the package is unpacked.
	// Common use: starting services, running ldconfig.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-maintainerscripts.html#s-mscriptsinstact
	PostInst string

	// PreRm is the script executed before the package is removed.
	// Common use: stopping services.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-maintainerscripts.html#s-mscriptsremact
	PreRm string

	// PostRm is the script executed after the package is removed.
	// Common use: purging configuration data.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-maintainerscripts.html#s-mscriptsremact
	PostRm string

	// Config is the script used for debconf configuration.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-maintainerscripts.html
	Config string
}

// File represents a single file resource to be installed on the target system.
type File struct {
	// DestPath is the absolute path where the file will be placed on the target system (e.g., "/usr/bin/app").
	DestPath string

	// Mode is the file permission mode (e.g., 0755 for executables, 0644 for text).
	Mode int64

	// Body is the source of the file content.
	Body string

	// IsConf, if true, marks this file as a configuration file in the 'conffiles' list.
	// dpkg will prompt the user before overwriting this file during upgrades.
	//
	// Reference: https://www.debian.org/doc/debian-policy/ch-files.html#s-config-files
	IsConf bool

	// ModTime is the modification time stored in the archive.
	// If zero, the current time is used.
	ModTime time.Time
}

// StandardFilename returns the canonical filename for the package.
// Format: {Package}_{Version}_{Architecture}.deb
//
// Reference: https://www.debian.org/doc/manuals/debian-faq/ch-pkg_basics.en.html#s-pkgname
func (p *Package) StandardFilename() string {
	return fmt.Sprintf("%s_%s_%s.deb", p.Metadata.Package, p.Metadata.Version, p.Metadata.Architecture)
}

// UpstreamVersion returns the upstream part of the version (everything before the last hyphen).
func (p *Package) UpstreamVersion() string {
	v := p.Metadata.Version
	lastHyphen := strings.LastIndex(v, "-")
	if lastHyphen == -1 {
		return v
	}
	return v[:lastHyphen]
}

// Iteration returns the debian revision part of the version (everything after the last hyphen).
func (p *Package) Iteration() string {
	v := p.Metadata.Version
	lastHyphen := strings.LastIndex(v, "-")
	if lastHyphen == -1 {
		return ""
	}
	return v[lastHyphen+1:]
}

// Set updates a specific field in the package's control metadata.
func (p *Package) Set(key, value string) {
	switch ControlField(key) {
	case FieldPackage:
		p.Metadata.Package = value
	case FieldVersion:
		p.Metadata.Version = value
	case FieldArchitecture:
		p.Metadata.Architecture = value
	case FieldMaintainer:
		p.Metadata.Maintainer = value
	case FieldDescription:
		p.Metadata.Description = value
	case FieldSection:
		p.Metadata.Section = value
	case FieldPriority:
		p.Metadata.Priority = value
	case FieldHomepage:
		p.Metadata.Homepage = value
	case FieldEssential:
		p.Metadata.Essential = (value == "yes")
	case FieldDepends:
		p.Metadata.Depends = splitList(value)
	case FieldPreDepends:
		p.Metadata.PreDepends = splitList(value)
	case FieldRecommends:
		p.Metadata.Recommends = splitList(value)
	case FieldSuggests:
		p.Metadata.Suggests = splitList(value)
	case FieldEnhances:
		p.Metadata.Enhances = splitList(value)
	case FieldConflicts:
		p.Metadata.Conflicts = splitList(value)
	case FieldBreaks:
		p.Metadata.Breaks = splitList(value)
	case FieldReplaces:
		p.Metadata.Replaces = splitList(value)
	case FieldProvides:
		p.Metadata.Provides = splitList(value)
	case FieldBuiltUsing:
		p.Metadata.BuiltUsing = value
	case FieldSource:
		p.Metadata.Source = value
	case FieldInstalledSize:
		// ignored, computed at generation time.
	default:
		if p.Metadata.ExtraFields == nil {
			p.Metadata.ExtraFields = make(map[string]string)
		}
		p.Metadata.ExtraFields[key] = value
	}
}

// WriteTo generates the .deb package and writes it to the provided io.Writer.
// It returns the total number of bytes written and any error encountered.
// This satisfies the io.WriterTo interface.
func (p *Package) WriteTo(w io.Writer) (int64, error) {
	// Wrapper to count bytes written for io.WriterTo return value
	cw := &countingWriter{w: w}

	// 1. Build Data Archive (data.tar.gz)
	// We must build this first to calculate MD5 sums of files for the control archive.
	dataBuf := new(bytes.Buffer)
	md5Map, installedSize, err := p.buildDataArchive(dataBuf)
	if err != nil {
		return cw.n, fmt.Errorf("building data archive: %w", err)
	}

	// 2. Build Control Archive (control.tar.gz)
	// Requires metadata and the MD5 sums calculated in step 1.
	controlBuf := new(bytes.Buffer)
	if err := p.buildControlArchive(controlBuf, md5Map, installedSize); err != nil {
		return cw.n, fmt.Errorf("building control archive: %w", err)
	}

	// 3. Assemble the final AR archive
	// The outer container of the .deb file.
	arW := ar.NewWriter(cw)

	// 3a. Write AR Global Header (!<arch>\n)
	if err := arW.WriteGlobalHeader(); err != nil {
		return cw.n, fmt.Errorf("writing ar global header: %w", err)
	}

	// 3b. Write debian-binary file (Must be first member)
	// Reference: https://manpages.debian.org/unstable/dpkg-dev/deb.5.en.html#FORMAT
	if err := addBufferToAr(arW, string(PkgDebianBinary), []byte("2.0\n")); err != nil {
		return cw.n, fmt.Errorf("writing %s: %w", PkgDebianBinary, err)
	}

	// 3c. Write control.tar.gz (Must be second member)
	if err := addBufferToAr(arW, string(PkgControlTarGz), controlBuf.Bytes()); err != nil {
		return cw.n, fmt.Errorf("writing %s: %w", PkgControlTarGz, err)
	}

	// 3d. Write data.tar.gz (Must be third member)
	if err := addBufferToAr(arW, string(PkgDataTarGz), dataBuf.Bytes()); err != nil {
		return cw.n, fmt.Errorf("writing %s: %w", PkgDataTarGz, err)
	}

	return cw.n, nil
}

// buildDataArchive creates the data.tar.gz containing the package files.
// It returns a map of file paths to MD5 checksums and the total installed size in bytes.
func (p *Package) buildDataArchive(w io.Writer) (map[string]string, int64, error) {
	gw := gzip.NewWriter(w)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	md5Map := make(map[string]string)
	var installedSize int64

	for _, file := range p.Files {
		// We must read the whole file to calculate size and MD5 before writing the tar header.
		content := []byte(file.Body)

		// Calculate MD5
		hash := md5.Sum(content)
		md5Map[file.DestPath] = hex.EncodeToString(hash[:])

		size := int64(len(content))
		installedSize += size

		// Prepare Tar Header
		// Remove leading slash to make path relative (standard for data.tar)
		relPath := strings.TrimPrefix(file.DestPath, "/")
		// Ensure it starts with ./ for strict Debian compliance
		if !strings.HasPrefix(relPath, "./") {
			relPath = "./" + relPath
		}

		header := &tar.Header{
			Name:    relPath,
			Size:    size,
			Mode:    file.Mode,
			ModTime: file.ModTime,
		}
		if header.ModTime.IsZero() {
			header.ModTime = time.Now()
		}

		if err := tw.WriteHeader(header); err != nil {
			return nil, 0, err
		}
		if _, err := tw.Write(content); err != nil {
			return nil, 0, err
		}
	}
	return md5Map, installedSize, nil
}

// buildControlArchive creates the control.tar.gz containing metadata files.
func (p *Package) buildControlArchive(w io.Writer, md5Map map[string]string, installedSize int64) error {
	gw := gzip.NewWriter(w)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	// Helper to write a file to the tarball
	writeEntry := func(name ControlFile, content []byte, mode int64) error {
		header := &tar.Header{
			Name:    "./" + string(name),
			Size:    int64(len(content)),
			Mode:    mode,
			ModTime: time.Now(),
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		_, err := tw.Write(content)
		return err
	}

	// 1. control
	controlContent := p.generateControlFile(installedSize)
	if err := writeEntry(FileControl, []byte(controlContent), 0644); err != nil {
		return fmt.Errorf("writing control: %w", err)
	}

	// 2. md5sums
	md5Content := p.generateMd5sums(md5Map)
	if err := writeEntry(FileMd5sums, []byte(md5Content), 0644); err != nil {
		return fmt.Errorf("writing md5sums: %w", err)
	}

	// 3. conffiles
	var conffiles []string
	for _, f := range p.Files {
		if f.IsConf {
			conffiles = append(conffiles, f.DestPath)
		}
	}
	if len(conffiles) > 0 {
		content := strings.Join(conffiles, "\n") + "\n"
		if err := writeEntry(FileConffiles, []byte(content), 0644); err != nil {
			return fmt.Errorf("writing conffiles: %w", err)
		}
	}

	// 4. Maintainer Scripts
	scripts := map[ControlFile]string{
		FilePreinst:  p.Scripts.PreInst,
		FilePostinst: p.Scripts.PostInst,
		FilePrerm:    p.Scripts.PreRm,
		FilePostrm:   p.Scripts.PostRm,
		FileConfig:   p.Scripts.Config,
	}
	for name, body := range scripts {
		if body != "" {
			if err := writeEntry(name, []byte(body), 0755); err != nil {
				return fmt.Errorf("writing %s: %w", name, err)
			}
		}
	}

	// 5. Extra Control Files
	var extraNames []string
	for name := range p.ExtraControlFiles {
		extraNames = append(extraNames, name)
	}
	sort.Strings(extraNames)

	for _, name := range extraNames {
		// Skip reserved files that are handled explicitly
		switch ControlFile(name) {
		case FileControl, FileMd5sums, FileConffiles, FilePreinst, FilePostinst, FilePrerm, FilePostrm, FileConfig:
			continue
		}
		content := p.ExtraControlFiles[name]
		if content != "" {
			if err := writeEntry(ControlFile(name), []byte(content), 0644); err != nil {
				return fmt.Errorf("writing extra control file %s: %w", name, err)
			}
		}
	}

	return nil
}

func (p *Package) generateControlFile(installedBytes int64) string {
	var b strings.Builder

	writeField := func(field ControlField, value string) {
		if value != "" {
			fmt.Fprintf(&b, "%s: %s\n", field, value)
		}
	}

	// Mandatory fields
	writeField(FieldPackage, p.Metadata.Package)
	writeField(FieldVersion, p.Metadata.Version)
	writeField(FieldArchitecture, p.Metadata.Architecture)
	writeField(FieldMaintainer, p.Metadata.Maintainer)

	// Installed-Size is in kilobytes, rounded up
	kbytes := (installedBytes + 1023) / 1024
	writeField(FieldInstalledSize, fmt.Sprintf("%d", kbytes))

	// Optional fields
	writeField(FieldSection, p.Metadata.Section)
	writeField(FieldPriority, p.Metadata.Priority)
	writeField(FieldHomepage, p.Metadata.Homepage)

	if p.Metadata.Essential {
		writeField(FieldEssential, "yes")
	}

	// Relationships
	writeRel := func(field ControlField, items []string) {
		if len(items) > 0 {
			writeField(field, strings.Join(items, ", "))
		}
	}
	writeRel(FieldDepends, p.Metadata.Depends)
	writeRel(FieldPreDepends, p.Metadata.PreDepends)
	writeRel(FieldRecommends, p.Metadata.Recommends)
	writeRel(FieldSuggests, p.Metadata.Suggests)
	writeRel(FieldEnhances, p.Metadata.Enhances)
	writeRel(FieldConflicts, p.Metadata.Conflicts)
	writeRel(FieldBreaks, p.Metadata.Breaks)
	writeRel(FieldReplaces, p.Metadata.Replaces)
	writeRel(FieldProvides, p.Metadata.Provides)

	writeField(FieldBuiltUsing, p.Metadata.BuiltUsing)
	writeField(FieldSource, p.Metadata.Source)

	// Extra fields
	for k, v := range p.Metadata.ExtraFields {
		writeField(ControlField(k), v)
	}

	// Description
	if p.Metadata.Description != "" {
		lines := strings.Split(p.Metadata.Description, "\n")
		writeField(FieldDescription, lines[0])
		for _, line := range lines[1:] {
			if strings.TrimSpace(line) == "" {
				fmt.Fprintf(&b, " .\n")
			} else {
				// Ensure extended description lines start with a space
				if strings.HasPrefix(line, " ") {
					fmt.Fprintf(&b, "%s\n", line)
				} else {
					fmt.Fprintf(&b, " %s\n", line)
				}
			}
		}
	}

	return b.String()
}

func (p *Package) generateMd5sums(md5Map map[string]string) string {
	var paths []string
	for path := range md5Map {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var b strings.Builder
	for _, path := range paths {
		// md5sums file expects paths relative to root, usually without leading slash
		cleanPath := strings.TrimPrefix(path, "/")
		fmt.Fprintf(&b, "%s  %s\n", md5Map[path], cleanPath)
	}
	return b.String()
}

// NewPackage creates a Package struct from a .deb file reader.
func NewPackage(r io.Reader) (*Package, error) {
	pkg := &Package{
		Metadata:          Metadata{ExtraFields: make(map[string]string)},
		ExtraControlFiles: make(map[string]string),
	}
	var conffiles []string

	arR := ar.NewReader(r)
	for {
		header, err := arR.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading ar header: %w", err)
		}

		if strings.HasPrefix(header.Name, "control.tar") {
			var tr *tar.Reader
			if strings.HasSuffix(header.Name, ".gz") {
				gzr, err := gzip.NewReader(arR)
				if err != nil {
					return nil, fmt.Errorf("opening control.tar.gz: %w", err)
				}
				defer gzr.Close()
				tr = tar.NewReader(gzr)
			} else {
				tr = tar.NewReader(arR)
			}

			for {
				th, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					return nil, fmt.Errorf("reading control tar header: %w", err)
				}

				name := filepath.Base(th.Name)
				var buf bytes.Buffer
				if _, err := io.Copy(&buf, tr); err != nil {
					return nil, fmt.Errorf("reading %s: %w", name, err)
				}
				content := buf.String()

				switch ControlFile(name) {
				case FileControl:
					if err := parseControlFile(content, &pkg.Metadata); err != nil {
						return nil, fmt.Errorf("parsing control file: %w", err)
					}
				case FileConffiles:
					conffiles = strings.Split(strings.TrimSpace(content), "\n")
				case FilePreinst:
					pkg.Scripts.PreInst = content
				case FilePostinst:
					pkg.Scripts.PostInst = content
				case FilePrerm:
					pkg.Scripts.PreRm = content
				case FilePostrm:
					pkg.Scripts.PostRm = content
				case FileConfig:
					pkg.Scripts.Config = content
				case FileMd5sums:
					// Ignore
				default:
					if !strings.HasPrefix(name, ".") {
						pkg.ExtraControlFiles[name] = content
					}
				}
			}
		} else if strings.HasPrefix(header.Name, "data.tar") {
			var tr *tar.Reader
			if strings.HasSuffix(header.Name, ".gz") {
				gzr, err := gzip.NewReader(arR)
				if err != nil {
					return nil, fmt.Errorf("opening data.tar.gz: %w", err)
				}
				defer gzr.Close()
				tr = tar.NewReader(gzr)
			} else {
				tr = tar.NewReader(arR)
			}

			for {
				th, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					return nil, fmt.Errorf("reading data tar header: %w", err)
				}

				if th.Typeflag != tar.TypeReg {
					continue
				}

				var buf bytes.Buffer
				if _, err := io.Copy(&buf, tr); err != nil {
					return nil, fmt.Errorf("reading file %s: %w", th.Name, err)
				}

				destPath := "/" + strings.TrimPrefix(th.Name, "./")
				destPath = strings.ReplaceAll(destPath, "//", "/")

				pkg.Files = append(pkg.Files, File{
					DestPath: destPath,
					Mode:     th.Mode,
					Body:     buf.String(),
					ModTime:  th.ModTime,
				})
			}
		}
	}

	if len(conffiles) > 0 {
		confSet := make(map[string]bool)
		for _, cf := range conffiles {
			if cf != "" {
				confSet[cf] = true
			}
		}
		for i := range pkg.Files {
			if confSet[pkg.Files[i].DestPath] {
				pkg.Files[i].IsConf = true
			}
		}
	}

	return pkg, nil
}

// Digest computes a deterministic SHA256 hash of the package content.
// It includes metadata, scripts, and file contents, but excludes file modification times
// and is insensitive to the order of files in the payload.
func (p *Package) Digest() string {
	// Ensure Installed-Size is up to date.
	// TODO it's a problem if the source has a wrong installed size, this will change the value of the Package that is supposed to be immutable.
	// We should probably calculate the installed size in NewPackage and store it in the Metadata,
	// then use that value here instead of recalculating it.
	var installedSize int64
	for _, f := range p.Files {
		installedSize += int64(len(f.Body))
	}
	kbytes := (installedSize + 1023) / 1024
	p.Set(string(FieldInstalledSize), fmt.Sprintf("%d", kbytes))

	h := sha256.New()

	// write appends a length-prefixed string to the hash to ensure uniqueness.
	write := func(s string) {
		fmt.Fprintf(h, "%d:%s\x00", len(s), s)
	}

	// 1. Metadata
	// Standard fields
	write(p.Metadata.Package)
	write(p.Metadata.Version)
	write(p.Metadata.Architecture)
	write(p.Metadata.Maintainer)
	write(p.Metadata.Description)
	write(p.Metadata.Section)
	write(p.Metadata.Priority)
	write(p.Metadata.Homepage)
	write(fmt.Sprintf("%v", p.Metadata.Essential))
	write(p.Metadata.BuiltUsing)
	write(p.Metadata.Source)

	// List fields (Order matters)
	lists := [][]string{
		p.Metadata.Depends,
		p.Metadata.PreDepends,
		p.Metadata.Recommends,
		p.Metadata.Suggests,
		p.Metadata.Enhances,
		p.Metadata.Conflicts,
		p.Metadata.Breaks,
		p.Metadata.Replaces,
		p.Metadata.Provides,
	}
	for _, list := range lists {
		write(fmt.Sprintf("%d", len(list)))
		for _, v := range list {
			write(v)
		}
	}

	// ExtraFields (Sorted by key)
	var extraKeys []string
	for k := range p.Metadata.ExtraFields {
		extraKeys = append(extraKeys, k)
	}
	sort.Strings(extraKeys)
	for _, k := range extraKeys {
		write(k)
		write(p.Metadata.ExtraFields[k])
	}

	// 2. Scripts
	write(p.Scripts.PreInst)
	write(p.Scripts.PostInst)
	write(p.Scripts.PreRm)
	write(p.Scripts.PostRm)
	write(p.Scripts.Config)

	// 3. ExtraControlFiles (Sorted by key)
	var controlKeys []string
	for k := range p.ExtraControlFiles {
		controlKeys = append(controlKeys, k)
	}
	sort.Strings(controlKeys)
	for _, k := range controlKeys {
		write(k)
		write(p.ExtraControlFiles[k])
	}

	// 4. Files (Sorted by DestPath)
	files := make([]File, len(p.Files))
	copy(files, p.Files)
	sort.Slice(files, func(i, j int) bool {
		return files[i].DestPath < files[j].DestPath
	})

	for _, f := range files {
		write(f.DestPath)
		write(fmt.Sprintf("%d", f.Mode))
		write(fmt.Sprintf("%v", f.IsConf))
		write(f.Body)
	}

	return hex.EncodeToString(h.Sum(nil))
}

// Equal compares two packages for data equality using their Digest.
func (p *Package) Equal(other *Package) bool {
	if p == nil && other == nil {
		return true
	}
	if p == nil || other == nil {
		return false
	}
	return p.Digest() == other.Digest()
}

// SetOriginalState records the digests of the package when loaded from disk.
func (p *Package) SetOriginalState(contentDigest, diskDigest string) {
	p.originalContentDigest = contentDigest
	p.onDiskDigest = diskDigest
}

// IsOriginal checks if the package content matches the state when it was loaded
// and if the provided disk digest matches the original file on disk.
func (p *Package) IsOriginal(currentContentDigest, diskDigest string) bool {
	return p.originalContentDigest != "" &&
		p.onDiskDigest != "" &&
		p.originalContentDigest == currentContentDigest &&
		p.onDiskDigest == diskDigest
}
