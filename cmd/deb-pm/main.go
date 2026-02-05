package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"text/template"

	"github.com/etnz/apt-repo-builder/deb"
)

// Custom flag types for repeated flags
type arrayFlags []string

// String implements the flag.Value interface.
func (i *arrayFlags) String() string {
	return strings.Join(*i, ", ")
}

// Set implements the flag.Value interface.
func (i *arrayFlags) Set(value string) error {
	*i = append(*i, value)
	return nil
}

type kvFlags map[string]string

// String implements the flag.Value interface.
func (i *kvFlags) String() string {
	s := []string{}
	for k, v := range *i {
		s = append(s, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(s, ", ")
}

// Set implements the flag.Value interface.
func (i *kvFlags) Set(value string) error {
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid format, expected KEY=VALUE")
	}
	(*i)[parts[0]] = parts[1]
	return nil
}

// main is the entry point for the deb-pm CLI tool.
func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "deb":
		runDeb(os.Args[2:])
	case "purge":
		runPurge(os.Args[2:])
	default:
		printUsage()
		os.Exit(1)
	}
}

// printUsage prints the help message to stdout.
func printUsage() {
	fmt.Println("Usage: deb-pm <command> [flags]")
	fmt.Println("\nCommands:")
	fmt.Println("  deb      Mint & Manage packages")
	fmt.Println("  purge    Cleanup repository")
}

// runDeb executes the 'deb' subcommand, which handles package creation and insertion.
func runDeb(args []string) {
	fs := flag.NewFlagSet("deb", flag.ExitOnError)

	// Context Flags
	defines := make(kvFlags)
	fs.Var(&defines, "define", "Define variables for templates (KEY=VAL)")

	// Input Flags
	var input string
	fs.StringVar(&input, "input", "", "Source package path or URL")

	// Mutation Flags
	meta := make(kvFlags)
	fs.Var(&meta, "meta", "Overwrite control fields (Key=Value)")

	var injects arrayFlags
	fs.Var(&injects, "inject", "Inject payload file (src:dst)")

	var injectTpls arrayFlags
	fs.Var(&injectTpls, "inject-tpl", "Inject payload file with template (src:dst)")

	var conffiles arrayFlags
	fs.Var(&conffiles, "conffile", "Inject configuration file (src:dst)")

	var conffileTpls arrayFlags
	fs.Var(&conffileTpls, "conffile-tpl", "Inject configuration file with template (src:dst)")

	var modes arrayFlags
	fs.Var(&modes, "mode", "Set file mode (mode:dst)")

	var scripts arrayFlags
	fs.Var(&scripts, "script", "Inject maintainer script (src:dst)")

	var scriptTpls arrayFlags
	fs.Var(&scriptTpls, "script-tpl", "Inject maintainer script with template (src:dst)")

	var controls arrayFlags
	fs.Var(&controls, "control", "Inject auxiliary control file (src:dst)")

	var controlTpls arrayFlags
	fs.Var(&controlTpls, "control-tpl", "Inject auxiliary control file with template (src:dst)")

	// Repo & Strategy Flags
	var repoPath string
	fs.StringVar(&repoPath, "repo", "", "Path to repo.tar.gz")
	var strategy string
	fs.StringVar(&strategy, "strategy", "strict", "Conflict resolution strategy (safe, bump, strict, overwrite)")
	var prune bool
	fs.BoolVar(&prune, "prune", false, "Enable pruning logic")

	fs.Parse(args)

	if repoPath == "" {
		log.Fatal("--repo is required")
	}

	// 1. Load Repo
	repo, err := loadRepo(repoPath)
	if err != nil {
		if os.IsNotExist(err) {
			repo = &deb.Repository{
				ArchiveInfo: deb.ArchiveInfo{
					Origin: "deb-pm",
					Label:  "Managed Repository",
				},
			}
		} else {
			log.Fatalf("Failed to load repo: %v", err)
		}
	}

	// 2. Prepare Package (Scratch or Patch)
	var pkg *deb.Package
	if input == "" {
		// Scratch Mode
		pkg = &deb.Package{
			Metadata: deb.Metadata{
				ExtraFields: make(map[string]string),
			},
		}
	} else {
		// Patch Mode
		var err error
		pkg, err = readDeb(input)
		if err != nil {
			log.Fatalf("Failed to read input deb: %v", err)
		}
	}

	// 3. Apply Mutations
	if err := applyMutations(pkg, meta, injects, injectTpls, conffiles, conffileTpls, modes, scripts, scriptTpls, controls, controlTpls, defines); err != nil {
		log.Fatalf("Failed to apply mutations: %v", err)
	}

	// 4. Apply Strategy & Add to Repo
	if err := addToRepo(repo, pkg, strategy); err != nil {
		log.Fatalf("Failed to add package to repo: %v", err)
	}

	// 5. Prune
	if prune {
		pruneRepo(repo, pkg)
	}

	// 6. Save Repo
	if err := saveRepo(repo, repoPath); err != nil {
		log.Fatalf("Failed to save repo: %v", err)
	}

	fmt.Println("Operation completed successfully.")
}

// runPurge executes the 'purge' subcommand, which handles repository cleanup.
func runPurge(args []string) {
	fs := flag.NewFlagSet("purge", flag.ExitOnError)
	var repoPath string
	fs.StringVar(&repoPath, "repo", "", "Path to repo.tar.gz")
	var nameRegex string
	fs.StringVar(&nameRegex, "name", "", "Regex for package name")
	var versionRegex string
	fs.StringVar(&versionRegex, "version", "", "Regex for version")
	var archRegex string
	fs.StringVar(&archRegex, "arch", "", "Regex for architecture")
	var keepMax int
	fs.IntVar(&keepMax, "keep-max", -1, "Retain last N versions")
	var versionUnit string
	fs.StringVar(&versionUnit, "version-unit", "full", "Sorting unit (full|upstream)")

	fs.Parse(args)

	if repoPath == "" {
		log.Fatal("--repo is required")
	}

	repo, err := loadRepo(repoPath)
	if err != nil {
		log.Fatalf("Failed to load repo: %v", err)
	}

	// TODO: Implement filtering and purging logic
	log.Println("Purge logic not yet implemented")

	if err := saveRepo(repo, repoPath); err != nil {
		log.Fatalf("Failed to save repo: %v", err)
	}
}

// --- Helpers ---

// loadRepo opens a repository tarball and parses it into a Repository struct.
func loadRepo(path string) (*deb.Repository, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return deb.NewRepository(f)
}

// saveRepo writes the Repository struct back to a tarball at the specified path.
func saveRepo(repo *deb.Repository, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// deb.Repository.WriteTo writes a tarball
	_, err = repo.WriteTo(f)
	return err
}

// readDeb reads a .deb package from a local file path or a URL.
func readDeb(path string) (*deb.Package, error) {
	var r io.ReadCloser
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		resp, err := http.Get(path)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return nil, fmt.Errorf("fetching %s: status %d", path, resp.StatusCode)
		}
		r = resp.Body
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		r = f
	}
	defer r.Close()

	return deb.NewPackage(r)
}

// applyMutations applies requested changes (metadata updates, file injections, scripts) to the package.
func applyMutations(pkg *deb.Package, meta kvFlags, injects, injectTpls, conffiles, conffileTpls, modes, scripts, scriptTpls, controls, controlTpls arrayFlags, defines kvFlags) error {
	// Meta
	for k, v := range meta {
		pkg.Set(k, v)
	}

	// Injects
	for _, item := range injects {
		src, dst, err := parseSrcDst(item)
		if err != nil {
			return err
		}
		content, err := processContent(src, false, defines)
		if err != nil {
			return err
		}
		pkg.Files = append(pkg.Files, deb.File{
			DestPath: dst,
			Mode:     0644, // Default mode TODO - allow override with a mode flag mode:target
			Body:     content,
		})
	}

	for _, item := range injectTpls {
		src, dst, err := parseSrcDst(item)
		if err != nil {
			return err
		}
		content, err := processContent(src, true, defines)
		if err != nil {
			return err
		}
		pkg.Files = append(pkg.Files, deb.File{
			DestPath: dst,
			Mode:     0644,
			Body:     content,
		})
	}

	// Conffiles
	for _, item := range conffiles {
		src, dst, err := parseSrcDst(item)
		if err != nil {
			return err
		}
		content, err := processContent(src, false, defines)
		if err != nil {
			return err
		}
		pkg.Files = append(pkg.Files, deb.File{
			DestPath: dst,
			Mode:     0644,
			Body:     content,
			IsConf:   true,
		})
	}

	for _, item := range conffileTpls {
		src, dst, err := parseSrcDst(item)
		if err != nil {
			return err
		}
		content, err := processContent(src, true, defines)
		if err != nil {
			return err
		}
		pkg.Files = append(pkg.Files, deb.File{
			DestPath: dst,
			Mode:     0644,
			Body:     content,
			IsConf:   true,
		})
	}

	// Modes
	for _, item := range modes {
		parts := strings.SplitN(item, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid mode format %q, expected mode:dst", item)
		}
		modeStr := parts[0]
		dst := parts[1]

		mode, err := strconv.ParseInt(modeStr, 8, 64)
		if err != nil {
			return fmt.Errorf("invalid octal mode %q for %s: %w", modeStr, dst, err)
		}

		found := false
		for i := range pkg.Files {
			if pkg.Files[i].DestPath == dst {
				pkg.Files[i].Mode = mode
				found = true
			}
		}
		if !found {
			return fmt.Errorf("destination path %q not found for mode change", dst)
		}
	}

	// Scripts
	handleScript := func(item string, isTpl bool) error {
		src, dst, err := parseSrcDst(item)
		if err != nil {
			return err
		}
		content, err := processContent(src, isTpl, defines)
		if err != nil {
			return err
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
			return fmt.Errorf("unknown script destination: %s", dst)
		}
		return nil
	}

	for _, item := range scripts {
		if err := handleScript(item, false); err != nil {
			return err
		}
	}
	for _, item := range scriptTpls {
		if err := handleScript(item, true); err != nil {
			return err
		}
	}

	// Controls
	handleControl := func(item string, isTpl bool) error {
		src, dst, err := parseSrcDst(item)
		if err != nil {
			return err
		}
		content, err := processContent(src, isTpl, defines)
		if err != nil {
			return err
		}

		if pkg.ExtraControlFiles == nil {
			pkg.ExtraControlFiles = make(map[string]string)
		}
		pkg.ExtraControlFiles[dst] = content

		return nil
	}

	for _, item := range controls {
		if err := handleControl(item, false); err != nil {
			return err
		}
	}
	for _, item := range controlTpls {
		if err := handleControl(item, true); err != nil {
			return err
		}
	}

	return nil
}

// processContent reads content from a source (file or URL) and optionally executes it as a template.
func processContent(src string, isTpl bool, defines kvFlags) (string, error) {
	var rawContent string
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		resp, err := http.Get(src)
		if err != nil {
			return "", fmt.Errorf("fetching %s: %w", src, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("fetching %s: status %d", src, resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("reading body of %s: %w", src, err)
		}
		rawContent = string(body)
	} else {
		body, err := os.ReadFile(src)
		if err != nil {
			return "", fmt.Errorf("reading file %s: %w", src, err)
		}
		rawContent = string(body)
	}

	if isTpl {
		tmpl, err := template.New(src).Option("missingkey=error").Parse(rawContent)
		if err != nil {
			return "", err
		}
		var buf strings.Builder
		if err := tmpl.Execute(&buf, defines); err != nil {
			return "", err
		}
		return buf.String(), nil
	}
	return rawContent, nil
}

// parseSrcDst splits a string in the format "source:destination".
func parseSrcDst(s string) (string, string, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("expected src:dst")
	}
	return parts[0], parts[1], nil
}

// addToRepo adds a package to the repository, respecting the conflict resolution strategy.
func addToRepo(repo *deb.Repository, pkg *deb.Package, strategy string) error {
	if err := repo.AddStrict(pkg); err != nil {
		switch strategy {
		case "strict":
			return err
		case "overwrite":
			repo.AddOverwrite(pkg)
			return nil
		case "safe":
			// TODO: Implement safe strategy (check content hash)
			log.Println("Strategy 'safe' not yet implemented, failing on conflict.")
			return err
		case "bump":
			upstream := pkg.UpstreamVersion()
			candidates := repo.PackagesByUpstream(pkg.Metadata.Package, upstream, pkg.Metadata.Architecture)
			if len(candidates) > 0 {
				latest := candidates[0]
				newVer := deb.BumpVersion(latest.Metadata.Version)
				pkg.Set("Version", newVer)
				return repo.AddStrict(pkg)
			}
			return err
		}
	}
	return nil
}

// pruneRepo enforces retention policies on the repository (e.g. max versions).
func pruneRepo(repo *deb.Repository, newPkg *deb.Package) {
	// TODO: Implement prune logic
	// 1. Check Iterations > 3
	// 2. Check Versions > 3
}
