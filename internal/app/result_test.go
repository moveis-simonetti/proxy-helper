package app

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportErrNamesEveryFailure(t *testing.T) {
	var r Report
	r.Add(Result{Target: "git", Outcome: OutcomeApplied})
	r.Add(Result{Target: "apt", Outcome: OutcomeFailed, Err: errors.New("boom")})
	r.Add(Result{Target: "kde", Outcome: OutcomeSkipped, Detail: "kwriteconfig not found"})
	r.Add(Result{Target: "snap", Outcome: OutcomeFailed, Err: errors.New("nope")})

	err := r.Err()
	if err == nil {
		t.Fatal("expected an error when targets failed")
	}
	if !strings.Contains(err.Error(), "apt") || !strings.Contains(err.Error(), "snap") {
		t.Errorf("the error must name every failed target, got: %v", err)
	}
	if strings.Contains(err.Error(), "kde") {
		t.Error("a skipped target is not a failure")
	}
}

func TestReportErrIsNilWhenNothingFailed(t *testing.T) {
	var r Report
	r.Add(Result{Target: "git", Outcome: OutcomeApplied})
	r.Add(Result{Target: "kde", Outcome: OutcomeSkipped})

	if err := r.Err(); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// TestPackageDoesNotFormatUserFacingText locks in the rule that keeps the
// CLI English and the GUI Portuguese: internal/app must never write
// formatted, human-readable prose to a writer — it returns values (Outcome,
// NoticeKind, Args) and lets each front end word them in its own language.
// It statically scans every non-test .go file in the package for the fmt
// printers listed in forbiddenPrinters, which are exactly the calls that
// would put prose into this layer. fmt.Errorf and fmt.Sprintf are allowed:
// an error's text stays in English everywhere (documented on Result.Err),
// and Sprintf building a Detail string is still just data until a front end
// prints it.
func TestPackageDoesNotFormatUserFacingText(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}

	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		violations, err := scanForUserFacingOutput(path, src)
		if err != nil {
			t.Fatalf("scanning %s: %v", path, err)
		}
		for _, v := range violations {
			t.Errorf("%s writes user-facing prose; internal/app must return values instead", v)
		}
	}
}

// forbiddenPrinters are the fmt calls that put text on a writer. fmt.Errorf
// and fmt.Sprintf are deliberately absent: an error's text stays English
// everywhere, and Sprintf building a Detail string is still data until a
// front end prints it.
var forbiddenPrinters = map[string]bool{
	"Fprint":   true,
	"Fprintf":  true,
	"Fprintln": true,
	"Print":    true,
	"Printf":   true,
	"Println":  true,
}

// scanForUserFacingOutput returns one description per forbidden fmt call in
// src. It resolves the local name fmt was imported under, so an aliased
// import cannot slip past, and rejects a dot-import of fmt outright, since
// that would make every printer call unqualified and unscannable.
func scanForUserFacingOutput(path string, src []byte) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil, err
	}

	local := ""
	for _, imp := range f.Imports {
		if imp.Path.Value != `"fmt"` {
			continue
		}
		if imp.Name == nil {
			local = "fmt"
			break
		}
		if imp.Name.Name == "." {
			return []string{fmt.Sprintf("%s:%v dot-imports fmt", path, fset.Position(imp.Pos()))}, nil
		}
		local = imp.Name.Name
	}
	if local == "" {
		return nil, nil
	}

	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != local {
			return true
		}
		if forbiddenPrinters[sel.Sel.Name] {
			out = append(out, fmt.Sprintf("%s:%v calls fmt.%s", path, fset.Position(call.Pos()), sel.Sel.Name))
		}
		return true
	})
	return out, nil
}

// TestScannerCatchesWhatItClaims is the guard's own guard. A static check
// nobody has seen fail is indistinguishable from one that never fires, and
// this one has two edges worth pinning: the printers beyond Printf/Println,
// and an aliased fmt import.
func TestScannerCatchesWhatItClaims(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"plain Fprintf", "package app\nimport \"fmt\"\nimport \"os\"\nfunc f() { fmt.Fprintf(os.Stdout, \"hi\") }\n", true},
		{"bare Print", "package app\nimport \"fmt\"\nfunc f() { fmt.Print(\"hi\") }\n", true},
		{"bare Fprint", "package app\nimport \"fmt\"\nimport \"os\"\nfunc f() { fmt.Fprint(os.Stdout, \"hi\") }\n", true},
		{"aliased import", "package app\nimport p \"fmt\"\nfunc f() { p.Println(\"hi\") }\n", true},
		{"dot import", "package app\nimport . \"fmt\"\nfunc f() { Println(\"hi\") }\n", true},
		{"Errorf is allowed", "package app\nimport \"fmt\"\nfunc f() error { return fmt.Errorf(\"boom\") }\n", false},
		{"Sprintf is allowed", "package app\nimport \"fmt\"\nfunc f() string { return fmt.Sprintf(\"%d\", 1) }\n", false},
		{"no fmt at all", "package app\nfunc f() int { return 1 }\n", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := scanForUserFacingOutput("synthetic.go", []byte(tc.src))
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if (len(got) > 0) != tc.want {
				t.Errorf("caught=%v, want %v (violations: %v)", len(got) > 0, tc.want, got)
			}
		})
	}
}
