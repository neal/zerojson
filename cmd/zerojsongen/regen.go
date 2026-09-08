package main

import (
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// loadWithBlankedOutput type-checks the package with the previous output
// replaced by an overlay, trying in order: (1) a STUB — the old file with
// every function body replaced by a panic, which keeps its declarations
// (the -pool Decoder type, the arena types) so hand-written code in the
// package that refers to them still type-checks, while the bodies a
// renamed field would break are gone; (2) that stub with every
// declaration the type checker reports an error in PRUNED, repeated until
// it type-checks or nothing is left to prune — a renamed TYPE leaves the
// stub with methods and arena fields that name it, and dropping just
// those keeps the declarations hand-written code still needs (a Decoder
// for a root type that was not renamed); (3) a blank file, for when even
// that fails. All are "replaceable" views of output this run regenerates,
// so nothing is lost by not seeing the old bodies or methods.
func loadWithBlankedOutput(base packages.Config, absOut string) *packages.Package {
	names, err := packages.Load(&packages.Config{Mode: packages.NeedName, Dir: base.Dir}, ".")
	if err != nil || len(names) != 1 || names[0].Name == "" {
		return nil
	}
	try := func(content []byte, label string) (*packages.Package, []packages.Error) {
		cfg := base
		cfg.Overlay = map[string][]byte{absOut: content}
		pkgs, err := packages.Load(&cfg, ".")
		if err == nil && len(pkgs) == 1 && len(pkgs[0].Errors) == 0 {
			return pkgs[0], nil
		}
		var errs []packages.Error
		if len(pkgs) == 1 {
			errs = pkgs[0].Errors
		}
		if os.Getenv("ZEROJSONGEN_DEBUG") != "" {
			log.Printf("load with %s overlay (%s) failed: err=%v", absOut, label, err)
			for _, e := range errs {
				log.Printf("  %v", e)
			}
		}
		return nil, errs
	}
	if stub, ok := stubOutput(absOut); ok {
		content := stub
		// Bounded: every round removes at least one declaration, and a
		// generated file has finitely many; the bound only caps the cost
		// of a pathological case before the blank overlay takes over.
		for round := 0; round < 32; round++ {
			pkg, errs := try(content, "stub round "+strconv.Itoa(round))
			if pkg != nil {
				return pkg
			}
			pruned, changed := pruneErroringDecls(content, absOut, errs)
			if !changed {
				break
			}
			content = pruned
		}
	}
	pkg, _ := try([]byte("package "+names[0].Name+"\n"), "blank")
	return pkg
}

// pruneErroringDecls removes from the overlay source every top-level
// declaration that one of errs is positioned inside, then drops imports
// no remaining code uses. It reports whether anything was removed.
func pruneErroringDecls(src []byte, absOut string, errs []packages.Error) ([]byte, bool) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, absOut, src, parser.ParseComments)
	if err != nil {
		return nil, false
	}
	lines := map[int]bool{}
	for _, e := range errs {
		// packages.Error.Pos is "file:line:col" or "file:line".
		file, rest, ok := strings.Cut(e.Pos, ":")
		if !ok {
			continue
		}
		if !samePath(file, absOut) && filepath.Base(file) != filepath.Base(absOut) {
			continue
		}
		lineStr, _, _ := strings.Cut(rest, ":")
		if line, err := strconv.Atoi(lineStr); err == nil {
			lines[line] = true
		}
	}
	if len(lines) == 0 {
		return nil, false
	}
	hits := func(n ast.Node) bool {
		from, to := fset.Position(n.Pos()).Line, fset.Position(n.End()).Line
		for l := from; l <= to; l++ {
			if lines[l] {
				return true
			}
		}
		return false
	}
	var keep []ast.Decl
	removed := false
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			// A signature or receiver naming the old type: the whole
			// function goes (its body is already a stub).
			if hits(d) {
				removed = true
				continue
			}
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				break
			}
			var specs []ast.Spec
			for _, sp := range d.Specs {
				ts, isType := sp.(*ast.TypeSpec)
				var st *ast.StructType
				if isType {
					st, _ = ts.Type.(*ast.StructType)
				}
				if st != nil && st.Fields != nil {
					// An arena struct with a cell typed by the old type:
					// drop just that FIELD. Removing the whole type would
					// cascade into the -pool Decoder that embeds the arena,
					// and from there into the hand-written code that names
					// the Decoder — exactly what this overlay exists to keep.
					var fields []*ast.Field
					for _, fl := range st.Fields.List {
						if hits(fl) {
							removed = true
							continue
						}
						fields = append(fields, fl)
					}
					st.Fields.List = fields
					if hits(ts.Name) || (ts.TypeParams != nil && hits(ts.TypeParams)) {
						removed = true
						continue
					}
					specs = append(specs, sp)
					continue
				}
				if hits(sp) {
					removed = true
					continue
				}
				specs = append(specs, sp)
			}
			if len(specs) == 0 {
				continue
			}
			d.Specs = specs
		}
		keep = append(keep, d)
	}
	if !removed {
		return nil, false
	}
	f.Decls = keep
	// Comments attached to removed declarations would otherwise be
	// re-emitted as free-floating text; keep only the file header.
	f.Comments = headerComments(f)
	pruneUnusedImports(f)
	var out strings.Builder
	if err := format.Node(&out, fset, f); err != nil {
		return nil, false
	}
	return []byte(out.String()), true
}

// headerComments returns the comment groups that precede the package
// clause (the generated-file marker among them).
func headerComments(f *ast.File) []*ast.CommentGroup {
	var keep []*ast.CommentGroup
	for _, g := range f.Comments {
		if g.End() < f.Package {
			keep = append(keep, g)
		}
	}
	return keep
}

// stubOutput returns the previous generated file with every function and
// method body replaced by `panic("regenerating")`, keeping declarations.
func stubOutput(path string) ([]byte, bool) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, false
	}
	stub := &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{
		Fun:  ast.NewIdent("panic"),
		Args: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: `"zerojsongen: stale generated code"`}},
	}}}}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
			fn.Body = stub
		}
	}
	pruneUnusedImports(f)
	var out strings.Builder
	if err := format.Node(&out, fset, f); err != nil {
		return nil, false
	}
	return []byte(out.String()), true
}

// pruneUnusedImports drops every import whose qualifier no longer appears
// in a selector expression: imports only removed bodies or declarations
// used would otherwise be "imported and not used" (a type-check error).
func pruneUnusedImports(f *ast.File) {
	used := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				used[id.Name] = true
			}
		}
		return true
	})
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}
		var keep []ast.Spec
		for _, sp := range gd.Specs {
			imp := sp.(*ast.ImportSpec)
			name := ""
			if imp.Name != nil {
				name = imp.Name.Name
			} else {
				pth := strings.Trim(imp.Path.Value, `"`)
				name = pth[strings.LastIndex(pth, "/")+1:]
			}
			if name == "_" || name == "." || used[name] {
				keep = append(keep, sp)
			}
		}
		gd.Specs = keep
	}
}

// guardGeneratedNames refuses to write generated source whose top-level
// declarations — types, functions, variables, and methods — collide with
// a HAND-WRITTEN declaration already in the package: a user type named
// like the -pool Decoder, or a hand-written MarshalEasyJSON on a type
// that -compat-easyjson would wrap. The package would otherwise fail to
// compile on a "redeclared" error inside a DO-NOT-EDIT file. Declarations
// living in replaceable generated files (the previous output, easyjson
// output) are exactly what this run supersedes and do not count.
func (g *generator) guardGeneratedNames(src, outPath string) {
	f, err := parser.ParseFile(token.NewFileSet(), outPath, src, 0)
	if err != nil {
		return // gofmt reports the syntax problem with a better message
	}
	scope := g.pkg.Types.Scope()
	if g.generatedNames == nil {
		g.generatedNames = map[string]bool{}
	}
	for name := range declarationNames(f) {
		if pos, ok := g.testNames[name]; ok {
			log.Fatalf("generated declaration %s collides with test declaration at %s", name, pos)
		}
		if g.generatedNames[name] {
			log.Fatalf("duplicate generated declaration %s — generated outputs cannot share this name", name)
		}
		g.generatedNames[name] = true
	}
	handWritten := func(obj types.Object) bool {
		if obj == nil || obj.Pkg() != g.pkg.Types {
			return false
		}
		return !g.replaceableMethodFiles[g.pkg.Fset.Position(obj.Pos()).Filename]
	}
	refuse := func(what string, obj types.Object) {
		log.Fatalf("%s is already declared by hand at %s — rename it, or drop the flag that generates it (the generated %s cannot coexist with it)",
			what, g.pkg.Fset.Position(obj.Pos()), filepath.Base(outPath))
	}
	// Unresolved identifiers exclude locals and selector names. A package
	// declaration can shadow a predeclared name used by the generated code,
	// even when none of our own declarations collide with it.
	for _, id := range f.Unresolved {
		if types.Universe.Lookup(id.Name) != nil {
			if pos, ok := g.testNames[id.Name]; ok {
				log.Fatalf("predeclared name %s is shadowed by test declaration at %s", id.Name, pos)
			}
			if obj := scope.Lookup(id.Name); handWritten(obj) {
				refuse("predeclared name "+id.Name, obj)
			}
		}
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				if obj := scope.Lookup(d.Name.Name); handWritten(obj) {
					refuse("func "+d.Name.Name, obj)
				}
				continue
			}
			recv := d.Recv.List[0].Type
			if star, ok := recv.(*ast.StarExpr); ok {
				recv = star.X
			}
			id, ok := recv.(*ast.Ident)
			if !ok {
				continue
			}
			tn, ok := scope.Lookup(id.Name).(*types.TypeName)
			if !ok {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok {
				continue
			}
			if m, index, _ := types.LookupFieldOrMethod(types.NewPointer(named), true, g.pkg.Types, d.Name.Name); handWritten(m) {
				// A declared method may legally shadow a promoted field; the
				// generated selector still reaches it via its embedding path.
				if _, field := m.(*types.Var); field && len(index) > 1 {
					continue
				}
				refuse("method "+id.Name+"."+d.Name.Name, m)
			}
		case *ast.GenDecl:
			for _, sp := range d.Specs {
				switch sp := sp.(type) {
				case *ast.TypeSpec:
					if obj := scope.Lookup(sp.Name.Name); handWritten(obj) {
						refuse("type "+sp.Name.Name, obj)
					}
				case *ast.ValueSpec:
					for _, n := range sp.Names {
						if obj := scope.Lookup(n.Name); handWritten(obj) {
							refuse(n.Name, obj)
						}
					}
				}
			}
		}
	}
}

// Ask go list for the active test files so build tags and external test
// packages are respected, without requiring stale generated tests to compile.
func loadTestNames(dir, packageName string) map[string]token.Position {
	pkgs, err := packages.Load(&packages.Config{Dir: dir, Mode: packages.NeedName | packages.NeedFiles, Tests: true}, ".")
	if err != nil {
		log.Fatalf("list test files: %v", err)
	}
	names := map[string]token.Position{}
	seen := map[string]bool{}
	for _, pkg := range pkgs {
		if pkg.Name != packageName {
			continue
		}
		for _, path := range pkg.GoFiles {
			if !strings.HasSuffix(path, "_test.go") || seen[path] {
				continue
			}
			seen[path] = true
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				log.Fatalf("parse test declarations: %v", err)
			}
			if hasZeroJSONHeader(f) {
				continue
			}
			for name, pos := range declarationNames(f) {
				names[name] = fset.Position(pos)
			}
		}
	}
	return names
}

// Methods use receiver-qualified keys; top-level declarations share one
// namespace. The same inventory checks both test and generated declarations.
func declarationNames(f *ast.File) map[string]token.Pos {
	names := map[string]token.Pos{}
	add := func(name string, pos token.Pos) {
		if name == "_" || name == "init" {
			return
		}
		if _, exists := names[name]; exists {
			log.Fatalf("duplicate generated declaration %s", name)
		}
		names[name] = pos
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			name := d.Name.Name
			if d.Recv != nil {
				r := d.Recv.List[0].Type
				if p, ok := r.(*ast.StarExpr); ok {
					r = p.X
				}
				if id, ok := r.(*ast.Ident); ok {
					name = id.Name + "." + name
				} else {
					continue
				}
			}
			add(name, d.Pos())
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					add(s.Name.Name, s.Pos())
				case *ast.ValueSpec:
					for _, n := range s.Names {
						add(n.Name, n.Pos())
					}
				}
			}
		}
	}
	return names
}
