// Command doccheck reports every function and method without a doc comment,
// and every package without a package comment. Test files and generated files
// are skipped. It exits non-zero if it finds anything.
//
//	go run ./tools/doccheck ./...
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strings"
)

// main runs the check on the command-line arguments.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run checks the packages named by args ("./..." walks a tree, anything else
// is a single directory), writes findings to stdout and returns the exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		args = []string{"./..."}
	}
	var findings []string
	for _, arg := range args {
		dirs, err := packageDirs(arg)
		if err != nil {
			fmt.Fprintf(stderr, "doccheck: %v\n", err)
			return 2
		}
		for _, dir := range dirs {
			got, err := checkDir(dir)
			if err != nil {
				fmt.Fprintf(stderr, "doccheck: %v\n", err)
				return 2
			}
			findings = append(findings, got...)
		}
	}
	for _, f := range findings {
		fmt.Fprintln(stdout, f)
	}
	if len(findings) > 0 {
		fmt.Fprintf(stdout, "doccheck: %d missing doc comments\n", len(findings))
		return 1
	}
	return 0
}

// packageDirs expands a "dir/..." pattern into every directory under dir that
// holds Go files, skipping testdata, vendor and hidden directories. Any other
// argument is returned as is.
func packageDirs(arg string) ([]string, error) {
	root, ok := strings.CutSuffix(arg, "...")
	if !ok {
		return []string{arg}, nil
	}
	root = filepath.Clean(root)
	var dirs []string
	err := fs.WalkDir(os.DirFS(root), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != "." && (name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			if dir := filepath.Join(root, filepath.FromSlash(pathpkg.Dir(path))); !slices.Contains(dirs, dir) {
				dirs = append(dirs, dir)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	return dirs, nil
}

// checkDir parses the non-test, non-generated Go files in dir and returns a
// finding for each undocumented function and for a missing package comment.
func checkDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var findings []string
	parsed, hasPkgDoc := 0, false
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		if ast.IsGenerated(f) {
			continue
		}
		parsed++
		if f.Doc != nil {
			hasPkgDoc = true
		}
		for _, decl := range f.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok && fd.Doc == nil {
				findings = append(findings, fmt.Sprintf("%s: %s has no doc comment", fset.Position(fd.Pos()), funcName(fd)))
			}
		}
	}
	if parsed > 0 && !hasPkgDoc {
		findings = append(findings, dir+": package has no package comment")
	}
	return findings, nil
}

// funcName returns fd's name, qualified by its receiver type for methods.
func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	t := fd.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if idx, ok := t.(*ast.IndexExpr); ok {
		t = idx.X
	}
	if idx, ok := t.(*ast.IndexListExpr); ok {
		t = idx.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}
