//go:build ignore

// Command goroutineleak runs go test with a goroutine leak check in every test
// binary. Run it from inside a module, with go test's own flags and packages:
//
//	go run goroutineleak.go -race -count=1 ./...
//
// Each package with tests gets a TestMain through -overlay that fails the binary
// when the runtime's goroutineleak profile reports goroutines blocked on something
// no live goroutine can reach. A package's own TestMain is renamed in the overlay
// and called from the added one, so it must return rather than call os.Exit.
//
// Overlay files are compiled into the test binary like any other source. The go
// command's caveat that overlays "will not appear" when tests run is about the
// files a running test can read from disk, not about what was compiled.
//
// Only the main module's packages are instrumented, so a package from outside it,
// such as one in std, runs without the check.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type pkg struct {
	Dir          string
	ImportPath   string
	GoFiles      []string
	CgoFiles     []string
	TestGoFiles  []string
	XTestGoFiles []string
}

// names are the files and package-level identifiers the overlay adds. The per-run
// suffix keeps them from colliding with anything a package already has.
type names struct {
	addedFile, refusedFile, found, passed, testMain string
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	for _, arg := range args {
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		if name == "args" {
			break
		}
		if name == "overlay" {
			fmt.Fprintln(os.Stderr, "goroutineleak: -overlay is set by this wrapper and cannot be passed through")
			return 2
		}
	}

	tmp, err := os.MkdirTemp("", "goroutineleak-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "goroutineleak:", err)
		return 2
	}
	defer os.RemoveAll(tmp)

	overlayPath, err := writeOverlay(tmp, args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "goroutineleak:", err)
		return 2
	}

	cmd := exec.Command("go", append([]string{"test", "-overlay", overlayPath}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "goroutineleak:", err)
		return 2
	}
	return 0
}

// writeOverlay covers every package in the module rather than the ones named in
// args, so a problem in one package fails that package's tests, never the run.
func writeOverlay(tmp string, args []string) (string, error) {
	pkgs, err := listPackages(listFlags(args))
	if err != nil {
		return "", err
	}

	// Uppercase, so no element of the file names can read as a GOOS or GOARCH constraint.
	suffix := rand.Text()
	ids := names{
		addedFile:   "zz_goroutineleak_" + suffix + "_check_test.go",
		refusedFile: "zz_goroutineleak_" + suffix + "_refused_test.go",
		found:       "goroutineLeakFound" + suffix,
		passed:      "goroutineLeakPassed" + suffix,
		testMain:    "goroutineLeakTestMain" + suffix,
	}

	replace := map[string]string{}
	for i, p := range pkgs {
		if err := instrument(p, ids, filepath.Join(tmp, fmt.Sprint(i)), replace); err != nil {
			return "", fmt.Errorf("%s: %w", p.ImportPath, err)
		}
	}

	data, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		return "", err
	}
	path := filepath.Join(tmp, "overlay.json")
	return path, os.WriteFile(path, data, 0o600)
}

// listPackages runs from the caller's directory, as go test does, so a relative
// -modfile resolves the same way for both; the module path covers every package.
func listPackages(flags []string) ([]pkg, error) {
	var modFlags []string
	for _, flag := range flags {
		if strings.HasPrefix(flag, "-mod=") || strings.HasPrefix(flag, "-modfile=") {
			modFlags = append(modFlags, flag)
		}
	}
	// With no module arguments, go list -m names only the main module, not its dependencies.
	modCmd := exec.Command("go", slices.Concat([]string{"list", "-m", "-f", "{{.Path}}"}, modFlags)...)
	modCmd.Stderr = os.Stderr
	out, err := modCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -m: %w", err)
	}
	var patterns []string
	for _, path := range strings.Fields(string(out)) {
		patterns = append(patterns, path+"/...")
	}
	if len(patterns) == 0 {
		return nil, errors.New("not inside a module")
	}

	// No -test: TestGoFiles and XTestGoFiles are filled without it, and it would add
	// the test binaries' own packages to the list.
	cmd := exec.Command("go", slices.Concat([]string{"list", "-e", "-json"}, flags, patterns)...)
	cmd.Stderr = os.Stderr
	out, err = cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}

	var pkgs []pkg
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p pkg
		err := dec.Decode(&p)
		if errors.Is(err, io.EOF) {
			return pkgs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decoding go list: %w", err)
		}
		pkgs = append(pkgs, p)
	}
}

// listFlags returns the go test flags that change which files or modules go list
// sees, each as -name or -name=value: -race, for one, selects files behind a race
// build constraint. Everything after -args belongs to the test binary.
func listFlags(args []string) []string {
	var flags []string
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") {
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		switch name {
		case "args":
			return flags
		case "race", "msan", "asan":
			flags = append(flags, args[i])
		case "tags", "mod", "modfile", "compiler":
			if !hasValue && i+1 < len(args) {
				i++
				value = args[i]
			}
			flags = append(flags, "-"+name+"="+value)
		}
	}
	return flags
}

// instrument adds one hook per test binary: internal and external test files build
// into a single binary with a single TestMain, so one hook covers both.
func instrument(p pkg, ids names, tmp string, replace map[string]string) error {
	files := slices.Concat(p.TestGoFiles, p.XTestGoFiles)
	if len(files) == 0 {
		return nil
	}
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return err
	}

	fset := token.NewFileSet()
	var pkgName, existingFile string
	var existing *ast.FuncDecl
	var existingSrc []byte
	external := false
	// The parsed files, and any TestMain declared as a var, const or type, both keyed
	// by whether they belong to the external test package.
	scope := map[bool][]*ast.File{}
	nonHook := map[bool]token.Pos{}
	for i, name := range files {
		isExternal := i >= len(p.TestGoFiles)
		path := filepath.Join(p.Dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			// Left alone: go test reports the syntax error if the package is selected.
			return nil
		}
		scope[isExternal] = append(scope[isExternal], f)
		if pkgName == "" {
			pkgName, external = f.Name.Name, isExternal
		}
		if pos := declaredTestMain(f); pos.IsValid() {
			nonHook[isExternal] = pos
		}
		fn := findTestMain(f)
		if fn == nil {
			continue
		}
		if !isHook(fn) {
			reason := fmt.Sprintf("%s: TestMain is not func TestMain(*testing.M), and the goroutine leak check needs that name for its own hook; rename it", fset.Position(fn.Pos()))
			return refuse(p, ids, f.Name.Name, reason, tmp, replace)
		}
		if err := checkReturns(fset, f, fn); err != nil {
			return refuse(p, ids, f.Name.Name, err.Error(), tmp, replace)
		}
		pkgName, external, existing, existingFile, existingSrc = f.Name.Name, isExternal, fn, path, src
	}

	// The internal test package shares a scope with the package's own files, where
	// go test treats even a func TestMain as an ordinary function.
	if !external {
		for _, name := range slices.Concat(p.GoFiles, p.CgoFiles) {
			f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, parser.SkipObjectResolution)
			if err != nil {
				return nil
			}
			scope[false] = append(scope[false], f)
			pos := declaredTestMain(f)
			if fn := findTestMain(f); fn != nil {
				pos = fn.Pos()
			}
			if _, seen := nonHook[false]; pos.IsValid() && !seen {
				nonHook[false] = pos
			}
		}
	}
	if pos, ok := nonHook[external]; ok {
		reason := fmt.Sprintf("%s: this TestMain is not the test hook, and the goroutine leak check needs that name for its own hook; rename it", fset.Position(pos))
		return refuse(p, ids, pkgName, reason, tmp, replace)
	}
	// Only the declaration is renamed, so any other reference would reach the added hook.
	if existing != nil {
		if pos := referencesTestMain(scope[external], existing); pos.IsValid() {
			reason := fmt.Sprintf("%s: TestMain is referred to here, and the goroutine leak check renames it; move what both need into a helper", fset.Position(pos))
			return refuse(p, ids, pkgName, reason, tmp, replace)
		}
	}

	// Either way the check runs only once the tests passed, so a failure is not
	// buried under the goroutines it left behind.
	body := "\tcode := m.Run()\n\tif code == 0 && " + ids.found + "() {\n\t\tcode = 1\n\t}\n\tgoroutineleakos.Exit(code)\n"
	if existing != nil {
		offset := fset.Position(existing.Name.Pos()).Offset
		renamed := slices.Concat(existingSrc[:offset], []byte(ids.testMain), existingSrc[offset+len("TestMain"):])
		copyPath := filepath.Join(tmp, "renamed.go")
		if err := os.WriteFile(copyPath, renamed, 0o600); err != nil {
			return err
		}
		replace[existingFile] = copyPath
		body = "\t" + ids.testMain + "(m)\n\tif " + ids.passed + "(m) && " + ids.found + "() {\n\t\tgoroutineleakos.Exit(1)\n\t}\n"
	}

	source := strings.NewReplacer("PACKAGE", pkgName, "BODY", body, "FOUND", ids.found, "PASSED", ids.passed).Replace(addedSource)
	addedPath := filepath.Join(tmp, ids.addedFile)
	if err := os.WriteFile(addedPath, []byte(source), 0o600); err != nil {
		return err
	}
	replace[filepath.Join(p.Dir, ids.addedFile)] = addedPath
	return nil
}

// refuse maps a file into the package that panics when its tests start, so the
// problem fails that package alone and only when it is tested.
func refuse(p pkg, ids names, pkgName, reason, tmp string, replace map[string]string) error {
	source := strings.NewReplacer("PACKAGE", pkgName, "REASON", strconv.Quote("goroutineleak: "+reason)).Replace(refusedSource)
	path := filepath.Join(tmp, ids.refusedFile)
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		return err
	}
	replace[filepath.Join(p.Dir, ids.refusedFile)] = path
	return nil
}

func findTestMain(f *ast.File) *ast.FuncDecl {
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "TestMain" {
			return fn
		}
	}
	return nil
}

// declaredTestMain returns where f declares TestMain as a variable, constant or
// type, which would collide with the added hook.
func declaredTestMain(f *ast.File) token.Pos {
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			switch s := spec.(type) {
			case *ast.ValueSpec:
				for _, name := range s.Names {
					if name.Name == "TestMain" {
						return name.Pos()
					}
				}
			case *ast.TypeSpec:
				if s.Name.Name == "TestMain" {
					return s.Name.Pos()
				}
			}
		}
	}
	return token.NoPos
}

// referencesTestMain returns where files refer to TestMain other than by decl, its
// declaration. A field, method, selector or composite literal key of that name is
// not a reference to it.
func referencesTestMain(files []*ast.File, decl *ast.FuncDecl) token.Pos {
	skip := map[*ast.Ident]bool{decl.Name: true}
	found := token.NoPos
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			if found.IsValid() {
				return false
			}
			switch n := n.(type) {
			case *ast.SelectorExpr:
				skip[n.Sel] = true
			case *ast.FuncDecl:
				if n.Recv != nil {
					skip[n.Name] = true
				}
			case *ast.Field:
				for _, name := range n.Names {
					skip[name] = true
				}
			case *ast.KeyValueExpr:
				if id, ok := n.Key.(*ast.Ident); ok {
					skip[id] = true
				}
			case *ast.Ident:
				if n.Name == "TestMain" && !skip[n] {
					found = n.Pos()
				}
			}
			return true
		})
	}
	return found
}

// isHook reports whether fn is the TestMain hook by the go command's own rule: one
// *M or *pkg.M parameter and no results, with the type itself left to the compiler,
// so an alias of testing.M counts. go test rejects type parameters, and it runs a
// TestMain(*testing.T) as an ordinary test instead.
func isHook(fn *ast.FuncDecl) bool {
	params := fn.Type.Params.List
	if fn.Type.TypeParams != nil || (fn.Type.Results != nil && len(fn.Type.Results.List) > 0) || len(params) != 1 || len(params[0].Names) > 1 {
		return false
	}
	star, ok := params[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	switch t := star.X.(type) {
	case *ast.Ident:
		return t.Name == "M"
	case *ast.SelectorExpr:
		return t.Sel.Name == "M"
	}
	return false
}

// importName returns the name f refers to path by: "" when f does not import it,
// "." for a dot import.
func importName(f *ast.File, path string) string {
	name := ""
	for _, imp := range f.Imports {
		if imp.Path.Value != strconv.Quote(path) {
			continue
		}
		switch {
		case imp.Name == nil:
			name = filepath.Base(path)
		default:
			name = imp.Name.Name
		}
	}
	return name
}

// checkReturns rejects a TestMain that exits once the tests have run, since the
// leak check runs after it returns and would never be reached. An exit in a defer
// or a closure can run after m.Run wherever it is written, so either counts.
//
// Order is source order, not control flow: an exit written after m.Run is refused
// even on a branch that cannot follow it, since a refusal that names the line is
// safer than a check that silently never runs.
func checkReturns(fset *token.FileSet, f *ast.File, fn *ast.FuncDecl) error {
	// A dot import spells os.Exit as a bare Exit, so both forms count.
	osName := importName(f, "os")
	params := fn.Type.Params.List
	if osName == "" || fn.Body == nil || len(params) == 0 {
		return nil
	}
	// An unnamed or blank parameter leaves m empty: m.Run cannot be called, so no exit can be placed before it.
	m := ""
	if len(params[0].Names) == 1 && params[0].Names[0].Name != "_" {
		m = params[0].Names[0].Name
	}
	isExitRef := func(e ast.Expr) bool {
		switch e := e.(type) {
		case *ast.Ident:
			return osName == "." && e.Name == "Exit"
		case *ast.SelectorExpr:
			id, ok := e.X.(*ast.Ident)
			return ok && osName != "." && id.Name == osName && e.Sel.Name == "Exit"
		}
		return false
	}
	isRunRef := func(e ast.Expr) bool {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok || m == "" {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		return ok && id.Name == m && sel.Sel.Name == "Run"
	}

	runPos := token.NoPos
	var exits []*ast.CallExpr
	var exitValue, runValue ast.Expr
	indirect := map[*ast.CallExpr]bool{}
	skip := map[ast.Node]bool{}
	var inspect func(n ast.Node, deferredOrClosure bool)
	inspect = func(root ast.Node, deferredOrClosure bool) {
		ast.Inspect(root, func(n ast.Node) bool {
			if !deferredOrClosure {
				switch n := n.(type) {
				case *ast.DeferStmt:
					inspect(n.Call, true)
					return false
				case *ast.FuncLit:
					inspect(n.Body, true)
					return false
				}
			}
			if sel, ok := n.(*ast.SelectorExpr); ok {
				skip[sel.Sel] = true
			}
			// ast.Inspect visits a call before its Fun, so a direct os.Exit or m.Run call
			// is marked in skip before the value checks below can see its selector.
			if call, ok := n.(*ast.CallExpr); ok {
				if isRunRef(call.Fun) {
					if runPos == token.NoPos || call.Pos() < runPos {
						runPos = call.Pos()
					}
					skip[call.Fun] = true
				}
				if isExitRef(call.Fun) {
					exits = append(exits, call)
					indirect[call] = deferredOrClosure
					skip[call.Fun] = true
				}
				return true
			}
			if e, ok := n.(ast.Expr); ok && !skip[e] {
				if exitValue == nil && isExitRef(e) {
					exitValue = e
				}
				if runValue == nil && isRunRef(e) {
					runValue = e
				}
			}
			return true
		})
	}
	inspect(fn.Body, false)

	// An os.Exit held in a variable can be called anywhere, so it cannot be placed before m.Run.
	if exitValue != nil {
		return fmt.Errorf("%s: TestMain uses os.Exit as a value; call it directly, before m.Run, or not at all, so the goroutine leak check runs", fset.Position(exitValue.Pos()))
	}
	// Likewise an m.Run held in a variable hides when the tests have run.
	if runValue != nil {
		return fmt.Errorf("%s: TestMain uses m.Run as a value; call m.Run directly, so the goroutine leak check can tell when the tests have run", fset.Position(runValue.Pos()))
	}
	if m == "" && len(exits) > 0 {
		return fmt.Errorf("%s: TestMain calls os.Exit but cannot call m.Run; name the *testing.M parameter and call m.Run, so the goroutine leak check runs", fset.Position(exits[0].Pos()))
	}

	for _, exit := range exits {
		if indirect[exit] {
			return fmt.Errorf("%s: TestMain calls os.Exit from a defer or closure; call it directly, before m.Run, so the goroutine leak check runs", fset.Position(exit.Pos()))
		}
		mentionsM := false
		ast.Inspect(exit, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == m {
				mentionsM = true
			}
			return !mentionsM
		})
		if mentionsM || (runPos != token.NoPos && exit.Pos() > runPos) {
			return fmt.Errorf("%s: TestMain calls os.Exit once m.Run has run; return instead, so the goroutine leak check runs", fset.Position(exit.Pos()))
		}
	}
	return nil
}

const refusedSource = `// Code generated by goroutineleak.go; DO NOT EDIT.

package PACKAGE

func init() {
	panic(REASON)
}
`

// Imports are aliased so they cannot collide with a package-level name. PASSED
// reads m's exit code the way the go command's own test main does once TestMain
// returns, since testing.M does not export it. Should a Go release rename the
// field, PASSED reports true and the check runs rather than panicking.
const addedSource = `// Code generated by goroutineleak.go; DO NOT EDIT.

package PACKAGE

import (
	goroutineleakbytes "bytes"
	goroutineleakfmt "fmt"
	goroutineleakos "os"
	goroutineleakreflect "reflect"
	goroutineleakpprof "runtime/pprof"
	goroutineleaktesting "testing"
)

func TestMain(m *goroutineleaktesting.M) {
BODY}

func PASSED(m *goroutineleaktesting.M) bool {
	code := goroutineleakreflect.ValueOf(m).Elem().FieldByName("exitCode")
	return !code.CanInt() || code.Int() == 0
}

func FOUND() bool {
	profile := goroutineleakpprof.Lookup("goroutineleak")
	if profile == nil {
		goroutineleakfmt.Fprintln(goroutineleakos.Stderr, "goroutineleak: this Go toolchain has no goroutineleak profile; it needs Go 1.27 or later")
		return true
	}
	var buf goroutineleakbytes.Buffer
	if err := profile.WriteTo(&buf, 1); err != nil {
		goroutineleakfmt.Fprintln(goroutineleakos.Stderr, "goroutineleak:", err)
		return true
	}
	if profile.Count() == 0 {
		return false
	}
	goroutineleakfmt.Fprintf(goroutineleakos.Stderr, "goroutineleak: %d leaked goroutine(s)\n%s", profile.Count(), buf.String())
	return true
}
`
