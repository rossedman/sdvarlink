// Command sdvarlink-gen generates typed Go packages from Varlink IDL
// snapshots, reports API changes between systemd releases, and writes the
// interface reference for the docs.
//
// Usage:
//
//	sdvarlink-gen generate [-idl idl] [-out systemd] [-config idl/generate.json]
//	sdvarlink-gen diff [-idl idl] OLD NEW        # e.g. diff 257 260
//	sdvarlink-gen docs [-idl idl] [-out docs/reference]
//	sdvarlink-gen version
//
// IDL snapshots live in <idl>/v<N>/<interface>.varlink, one directory per
// systemd release. hack/capture-idl.sh captures them from a running
// systemd; hack/idl-from-source extracts them from a source checkout.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/rossedman/sdvarlink/idl"
	"github.com/rossedman/sdvarlink/internal/gen"
)

// Set by GoReleaser through -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "generate":
		err = generate(os.Args[2:])
	case "diff":
		err = diff(os.Args[2:])
	case "docs":
		err = docs(os.Args[2:])
	case "version", "-version", "--version":
		fmt.Printf("sdvarlink-gen %s (commit %s, built %s)\n", version, commit, date)
		return
	case "-h", "-help", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "sdvarlink-gen: unknown command %q\n", os.Args[1])
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sdvarlink-gen:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  sdvarlink-gen generate [-idl dir] [-out dir] [-config file]
  sdvarlink-gen diff [-idl dir] OLD NEW
  sdvarlink-gen docs [-idl dir] [-out dir] [-config file]
  sdvarlink-gen version
`)
	os.Exit(2)
}

type release struct {
	version string
	dir     string
	ifaces  map[string]*idl.Interface
	files   map[string]string // interface -> IDL file path
}

var versionDir = regexp.MustCompile(`^v(\d+)$`)

// loadReleases parses every snapshot directory, oldest first.
func loadReleases(root string) ([]*release, error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []*release
	for _, e := range ents {
		m := versionDir.FindStringSubmatch(e.Name())
		if !e.IsDir() || m == nil {
			continue
		}
		r, err := loadRelease(filepath.Join(root, e.Name()), m[1])
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := strconv.Atoi(out[i].version)
		b, _ := strconv.Atoi(out[j].version)
		return a < b
	})
	if len(out) == 0 {
		return nil, fmt.Errorf("no v<N> snapshot directories in %s", root)
	}
	return out, nil
}

func loadRelease(dir, version string) (*release, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.varlink"))
	if err != nil {
		return nil, err
	}
	r := &release{version: version, dir: dir, ifaces: map[string]*idl.Interface{}, files: map[string]string{}}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		iface, err := idl.Parse(string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		r.ifaces[iface.Name] = iface
		r.files[iface.Name] = p
	}
	return r, nil
}

func generate(args []string) error {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	idlDir := fs.String("idl", "idl", "directory of IDL snapshots")
	outDir := fs.String("out", "systemd", "output directory")
	cfgPath := fs.String("config", "idl/generate.json", "generator config")
	_ = fs.Parse(args) // ExitOnError

	cfg, err := gen.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	releases, err := loadReleases(*idlDir)
	if err != nil {
		return err
	}
	// Generated directories are rewritten from scratch.
	old, _ := filepath.Glob(filepath.Join(*outDir, "v*"))
	for _, d := range append(old, filepath.Join(*outDir, "latest")) {
		if err := os.RemoveAll(d); err != nil {
			return err
		}
	}
	newest := releases[len(releases)-1]
	count := 0
	for _, r := range releases {
		for name, iface := range r.ifaces {
			pkg := gen.PackageName(name)
			rel := filepath.Join("v"+r.version, pkg)
			if cfg.IsUnstable(name) {
				rel = filepath.Join("v"+r.version, "x", pkg)
			}
			dir := filepath.Join(*outDir, rel)
			idlFile := name + ".varlink"
			if err := cfg.ApplyFieldTypes(iface, r.version); err != nil {
				return err
			}
			files, err := gen.Package(cfg, iface, idlFile, r.version)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			for _, f := range files {
				if err := os.WriteFile(filepath.Join(dir, f.Name), f.Content, 0o644); err != nil {
					return err
				}
			}
			src, err := os.ReadFile(r.files[name])
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, idlFile), src, 0o644); err != nil {
				return err
			}
			count++
			if r == newest && !cfg.IsUnstable(name) {
				importPath := cfg.Module + "/" + filepath.ToSlash(filepath.Join(filepath.Base(*outDir), rel))
				alias, err := gen.Alias(files[0].Content, importPath, r.version)
				if err != nil {
					return err
				}
				adir := filepath.Join(*outDir, "latest", pkg)
				if err := os.MkdirAll(adir, 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(adir, pkg+".go"), alias, 0o644); err != nil {
					return err
				}
			}
		}
	}
	fmt.Printf("generated %d packages for %d systemd releases (latest = v%s)\n", count, len(releases), newest.version)
	return nil
}

func diff(args []string) error {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	idlDir := fs.String("idl", "idl", "directory of IDL snapshots")
	_ = fs.Parse(args) // ExitOnError
	if fs.NArg() != 2 {
		return errors.New("diff needs two versions, e.g. diff 257 260")
	}
	from, to := strings.TrimPrefix(fs.Arg(0), "v"), strings.TrimPrefix(fs.Arg(1), "v")
	a, err := loadRelease(filepath.Join(*idlDir, "v"+from), from)
	if err != nil {
		return err
	}
	b, err := loadRelease(filepath.Join(*idlDir, "v"+to), to)
	if err != nil {
		return err
	}
	fmt.Print(gen.DiffMarkdown(from, to, gen.Diff(a.ifaces, b.ifaces)))
	return nil
}

func docs(args []string) error {
	fs := flag.NewFlagSet("docs", flag.ExitOnError)
	idlDir := fs.String("idl", "idl", "directory of IDL snapshots")
	outDir := fs.String("out", "docs/reference", "output directory")
	cfgPath := fs.String("config", "idl/generate.json", "generator config")
	mkdocs := fs.String("mkdocs", "mkdocs.yml", "mkdocs.yml whose generated nav block to update (empty to skip)")
	_ = fs.Parse(args) // ExitOnError

	cfg, err := gen.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	releases, err := loadReleases(*idlDir)
	if err != nil {
		return err
	}
	var vs []gen.Release
	for _, r := range releases {
		vs = append(vs, gen.Release{Version: r.version, Interfaces: r.ifaces})
	}
	pages := gen.ReferenceDocs(cfg, vs)
	ifaceDir := filepath.Join(*outDir, "interfaces")
	os.RemoveAll(ifaceDir)
	if err := os.MkdirAll(ifaceDir, 0o755); err != nil {
		return err
	}
	var nav bytes.Buffer
	for _, p := range pages {
		if err := os.WriteFile(filepath.Join(*outDir, p.Path), []byte(p.Content), 0o644); err != nil {
			return err
		}
		if p.NavTitle != "" {
			fmt.Fprintf(&nav, "          - %s: reference/%s\n", p.NavTitle, p.Path)
		}
	}
	if *mkdocs != "" {
		if err := replaceBlock(*mkdocs, nav.String()); err != nil {
			return err
		}
	}
	fmt.Printf("wrote %d reference pages\n", len(pages))
	return nil
}

const (
	beginMarker = "# BEGIN generated interface nav"
	endMarker   = "# END generated interface nav"
)

func replaceBlock(path, body string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s := string(data)
	i := strings.Index(s, beginMarker)
	j := strings.Index(s, endMarker)
	if i < 0 || j < i {
		return fmt.Errorf("%s: markers %q / %q not found", path, beginMarker, endMarker)
	}
	lineEnd := strings.IndexByte(s[i:], '\n') + i + 1
	lineStart := strings.LastIndexByte(s[:j], '\n') + 1
	s = s[:lineEnd] + body + s[lineStart:]
	return os.WriteFile(path, []byte(s), 0o644)
}
