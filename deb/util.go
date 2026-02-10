package deb

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/blakesmith/ar"
)

// countingWriter wraps an io.Writer and counts the bytes written.
// It is typically used to calculate the size of a file or archive entry
// as it is being written.
type countingWriter struct {
	w io.Writer
	n int64
}

// Write writes p to the underlying io.Writer and increments the byte count.
func (cw *countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.n += int64(n)
	return n, err
}

// addBufferToAr writes a named byte slice as a file entry to the AR archive.
// It constructs the AR header with mode 0644 and the current timestamp.
func addBufferToAr(w *ar.Writer, name string, body []byte) error {
	header := &ar.Header{
		Name:    name,
		Size:    int64(len(body)),
		Mode:    0644,
		ModTime: time.Now(),
	}
	if err := w.WriteHeader(header); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

// parseDeb parses the binary content of a .deb file.
// It calculates the SHA256 hash of the file and extracts the control metadata,
// returning a repoPackage struct suitable for inclusion in an APT index.
func parseDeb(content []byte, filename string) (*repoPackage, error) {
	hash := sha256.Sum256(content)
	shaStr := hex.EncodeToString(hash[:])

	control, err := extractControlFromBytes(content)
	if err != nil {
		return nil, err
	}

	p, v, a := parseControlFields(control)

	return &repoPackage{
		Package:      p,
		Version:      v,
		Architecture: a,
		Control:      control,
		Filename:     filename,
		Size:         int64(len(content)),
		SHA256:       shaStr,
	}, nil
}

// extractControlFromBytes iterates through the AR archive structure of a .deb file
// to locate and decompress the 'control.tar.gz' (or 'control.tar') member,
// and then extracts the 'control' file content from within that tarball.
func extractControlFromBytes(data []byte) (string, error) {
	r := bytes.NewReader(data)
	arR := ar.NewReader(r)

	for {
		header, err := arR.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		if strings.HasPrefix(header.Name, "control.tar") {
			var tr *tar.Reader
			// Read the tar content
			tarData := make([]byte, header.Size)
			if _, err := io.ReadFull(arR, tarData); err != nil {
				return "", err
			}
			tarR := bytes.NewReader(tarData)

			if strings.HasSuffix(header.Name, ".gz") {
				gzr, err := gzip.NewReader(tarR)
				if err != nil {
					return "", err
				}
				defer gzr.Close()
				tr = tar.NewReader(gzr)
			} else {
				tr = tar.NewReader(tarR)
			}

			for {
				th, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					return "", err
				}
				if filepath.Base(th.Name) == "control" {
					var buf bytes.Buffer
					if _, err := io.Copy(&buf, tr); err != nil {
						return "", err
					}
					return buf.String(), nil
				}
			}
		}
	}
	return "", fmt.Errorf("control file not found")
}

// parseControlFields parses the raw text of a Debian control file to extract
// the Package name, Version, and Architecture fields.
func parseControlFields(control string) (string, string, string) {
	var p, v, a string
	lines := strings.Split(control, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, string(FieldPackage)+": ") {
			p = strings.TrimSpace(strings.TrimPrefix(line, string(FieldPackage)+": "))
		} else if strings.HasPrefix(line, string(FieldVersion)+": ") {
			v = strings.TrimSpace(strings.TrimPrefix(line, string(FieldVersion)+": "))
		} else if strings.HasPrefix(line, string(FieldArchitecture)+": ") {
			a = strings.TrimSpace(strings.TrimPrefix(line, string(FieldArchitecture)+": "))
		}
	}
	return p, v, a
}

// generatePackagesFile generates the content of the 'Packages' index file.
// It concatenates the control stanzas of all packages in the index and appends
// the mandatory Filename, Size, and SHA256 fields.
func generatePackagesFile(index []*repoPackage) []byte {
	var b bytes.Buffer
	for _, p := range index {
		b.WriteString(p.Control)
		if !strings.HasSuffix(p.Control, "\n") {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "Filename: %s\nSize: %d\nSHA256: %s\n\n", p.Filename, p.Size, p.SHA256)
	}
	return b.Bytes()
}

// generateReleaseFile generates the content of the 'Release' file for a flat repository.
// It includes repository metadata (Origin, Label, etc.) and the checksums for the
// Packages and Packages.gz files.
func generateReleaseFile(info ArchiveInfo, packages, packagesGz []byte) []byte {
	var b bytes.Buffer
	writeField := func(key ReleaseField, value string) {
		if value != "" {
			fmt.Fprintf(&b, "%s: %s\n", key, value)
		}
	}

	writeField(RelOrigin, info.Origin)
	writeField(RelLabel, info.Label)
	writeField(RelSuite, info.Suite)
	writeField(RelVersion, info.Version)
	writeField(RelCodename, info.Codename)
	if info.Date != "" {
		writeField(RelDate, info.Date)
	} else {
		writeField(RelDate, time.Now().UTC().Format(time.RFC1123Z))
	}
	writeField(RelValidUntil, info.ValidUntil)
	writeField(RelArchitectures, info.Architectures)
	writeField(RelComponents, info.Components)
	writeField(RelDescription, info.Description)
	writeField(RelNotAutomatic, info.NotAutomatic)
	writeField(RelButAutomaticUpgrades, info.ButAutomaticUpgrades)
	writeField(RelAcquireByHash, info.AcquireByHash)
	fmt.Fprintf(&b, "%s:\n", RelSHA256)

	hPkg := sha256.Sum256(packages)
	fmt.Fprintf(&b, " %x %d %s\n", hPkg, len(packages), "Packages")

	hGz := sha256.Sum256(packagesGz)
	fmt.Fprintf(&b, " %x %d %s\n", hGz, len(packagesGz), "Packages.gz")

	return b.Bytes()
}

// signBytes signs the provided input bytes using the provided ASCII-armored PGP private key.
// It returns the signed message in ASCII-armored format (clearsigned).
func signBytes(input []byte, key string) ([]byte, error) {
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(key))
	if err != nil {
		return nil, err
	}
	var signer *openpgp.Entity
	for _, e := range entities {
		if e.PrivateKey != nil {
			signer = e
			break
		}
	}
	if signer == nil {
		return nil, fmt.Errorf("no private key found")
	}

	var out bytes.Buffer
	w, err := clearsign.Encode(&out, signer.PrivateKey, nil)
	if err != nil {
		return nil, err
	}
	w.Write(input)
	w.Close()
	return out.Bytes(), nil
}

// extractPublicKey extracts the public key from an ASCII-armored PGP private key.
// If armored is true, it returns the public key in ASCII-armored format.
// Otherwise, it returns the binary serialized public key.
func extractPublicKey(key string, armored bool) ([]byte, error) {
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(key))
	if err != nil {
		return nil, err
	}
	var signer *openpgp.Entity
	for _, e := range entities {
		if e.PrivateKey != nil {
			signer = e
			break
		}
	}
	if signer == nil {
		return nil, fmt.Errorf("no private key found")
	}

	var buf bytes.Buffer
	if armored {
		w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
		if err != nil {
			return nil, err
		}
		if err := signer.Serialize(w); err != nil {
			return nil, err
		}
		w.Close()
	} else {
		if err := signer.Serialize(&buf); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// generateHierarchicalRelease generates the content of the 'Release' file for a
// standard hierarchical repository (dists/...). It lists the checksums for all
// files in the repository structure (Packages, Packages.gz, etc.).
func generateHierarchicalRelease(info ArchiveInfo, entries []releaseFileEntry) []byte {
	var b bytes.Buffer
	writeField := func(key ReleaseField, value string) {
		if value != "" {
			fmt.Fprintf(&b, "%s: %s\n", key, value)
		}
	}

	writeField(RelOrigin, info.Origin)
	writeField(RelLabel, info.Label)
	writeField(RelSuite, info.Suite)
	writeField(RelVersion, info.Version)
	writeField(RelCodename, info.Codename)
	if info.Date != "" {
		writeField(RelDate, info.Date)
	} else {
		writeField(RelDate, time.Now().UTC().Format(time.RFC1123Z))
	}
	writeField(RelValidUntil, info.ValidUntil)
	writeField(RelArchitectures, info.Architectures)
	writeField(RelComponents, info.Components)
	writeField(RelDescription, info.Description)
	writeField(RelNotAutomatic, info.NotAutomatic)
	writeField(RelButAutomaticUpgrades, info.ButAutomaticUpgrades)
	writeField(RelAcquireByHash, info.AcquireByHash)
	fmt.Fprintf(&b, "%s:\n", RelSHA256)

	// Sort entries for deterministic output
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})

	for _, e := range entries {
		fmt.Fprintf(&b, " %s %d %s\n", e.Hash, e.Size, e.Path)
	}

	return b.Bytes()
}

// parseControlFile parses the content of a Debian control file and populates the Metadata struct.
// It handles standard fields mapping to struct fields and puts unknown fields into ExtraFields.
// It also handles multiline values (folded fields).
func parseControlFile(content string, m *Metadata) error {
	var currentKey string
	var currentValue strings.Builder

	flush := func() {
		if currentKey != "" {
			val := strings.TrimSpace(currentValue.String())
			switch ControlField(currentKey) {
			case FieldPackage:
				m.Package = val
			case FieldVersion:
				m.Version = val
			case FieldArchitecture:
				m.Architecture = val
			case FieldMaintainer:
				m.Maintainer = val
			case FieldDescription:
				m.Description = val
			case FieldSection:
				m.Section = val
			case FieldPriority:
				m.Priority = val
			case FieldHomepage:
				m.Homepage = val
			case FieldEssential:
				m.Essential = (val == "yes")
			case FieldDepends:
				m.Depends = splitList(val)
			case FieldPreDepends:
				m.PreDepends = splitList(val)
			case FieldRecommends:
				m.Recommends = splitList(val)
			case FieldSuggests:
				m.Suggests = splitList(val)
			case FieldEnhances:
				m.Enhances = splitList(val)
			case FieldConflicts:
				m.Conflicts = splitList(val)
			case FieldBreaks:
				m.Breaks = splitList(val)
			case FieldReplaces:
				m.Replaces = splitList(val)
			case FieldProvides:
				m.Provides = splitList(val)
			case FieldBuiltUsing:
				m.BuiltUsing = val
			case FieldSource:
				m.Source = val
			case FieldInstalledSize:
				//ignore installed size when reading

			default:
				m.ExtraFields[currentKey] = val
			}
		}
	}

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			currentValue.WriteString("\n" + line)
		} else if strings.Contains(line, ":") {
			flush()
			parts := strings.SplitN(line, ":", 2)
			currentKey = parts[0]
			currentValue.Reset()
			currentValue.WriteString(strings.TrimSpace(parts[1]))
		}
	}
	flush()
	return nil
}

// splitList splits a comma-separated string into a slice of strings, trimming whitespace from each element.
// It returns nil if the input string is empty.
func splitList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var res []string
	for _, p := range parts {
		res = append(res, strings.TrimSpace(p))
	}
	return res
}

// parseReleaseFile parses the content of a Release file and populates the ArchiveInfo struct.
// It maps standard Release fields to the struct fields.
func parseReleaseFile(content string, info *ArchiveInfo) error {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, " ") || line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch ReleaseField(key) {
		case RelOrigin:
			info.Origin = val
		case RelLabel:
			info.Label = val
		case RelSuite:
			info.Suite = val
		case RelVersion:
			info.Version = val
		case RelCodename:
			info.Codename = val
		case RelDate:
			info.Date = val
		case RelArchitectures:
			info.Architectures = val
		case RelComponents:
			info.Components = val
		case RelDescription:
			info.Description = val
		case RelValidUntil:
			info.ValidUntil = val
		case RelNotAutomatic:
			info.NotAutomatic = val
		case RelButAutomaticUpgrades:
			info.ButAutomaticUpgrades = val
		case RelAcquireByHash:
			info.AcquireByHash = val
		}
	}
	return nil
}

// parsePackagesIndex parses a Packages index file content.
// It splits the content into stanzas (separated by blank lines) and parses each stanza into a Package struct.
// It also handles special fields like Filename (mapping to ExternalURL) and removes index-specific fields
// (Size, SHA256, etc.) from the metadata to keep it clean.
func parsePackagesIndex(content string) ([]*Package, error) {
	var pkgs []*Package
	stanzas := strings.Split(content, "\n\n")
	for _, stanza := range stanzas {
		if strings.TrimSpace(stanza) == "" {
			continue
		}
		pkg := &Package{
			Metadata: Metadata{ExtraFields: make(map[string]string)},
		}
		if err := parseControlFile(stanza, &pkg.Metadata); err != nil {
			return nil, err
		}

		delete(pkg.Metadata.ExtraFields, "Filename")

		// Clean up other index-only fields from ExtraFields
		delete(pkg.Metadata.ExtraFields, "Size")
		delete(pkg.Metadata.ExtraFields, "SHA256")
		delete(pkg.Metadata.ExtraFields, "MD5sum")
		delete(pkg.Metadata.ExtraFields, "SHA1")

		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

// BumpVersion increments the iteration number of a Debian version string.
// It ensures the new version is considered newer by Debian sorting rules.
//
// Strategy:
//  1. If no iteration (no hyphen), append "-1".
//  2. If iteration is purely numeric, increment it (e.g. "1.0-1" -> "1.0-2").
//  3. Otherwise, find the last alphanumeric character in the iteration and bump it
//     using the range 0-9, a-z. (e.g. "1.0-1a" -> "1.0-1b", "1.0-19" -> "1.0-1a").
//     If the character is 'z', '0' is appended ("1.0-1z" -> "1.0-1z0").
func BumpVersion(v string) string {
	idx := strings.LastIndex(v, "-")
	if idx == -1 {
		return v + "-1"
	}
	prefix := v[:idx+1]
	rev := v[idx+1:]
	if rev == "" {
		return prefix + "1"
	}

	// Try numeric bump
	if i, err := strconv.Atoi(rev); err == nil {
		return prefix + strconv.Itoa(i+1)
	}

	// Alphanumeric bump
	runes := []rune(rev)
	for i := len(runes) - 1; i >= 0; i-- {
		c := runes[i]
		if c >= '0' && c < '9' {
			runes[i]++
			return prefix + string(runes)
		}
		if c == '9' {
			runes[i] = 'a'
			return prefix + string(runes)
		}
		if c >= 'a' && c < 'z' {
			runes[i]++
			return prefix + string(runes)
		}
		if c == 'z' {
			return prefix + string(runes[:i+1]) + "0" + string(runes[i+1:])
		}
	}
	return v + "1"
}
