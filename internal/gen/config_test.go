package gen

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rossedman/sdvarlink/idl"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tests := []struct {
		name       string
		path       string
		wantErr    string
		wantSuffix string
		wantMod    string
	}{
		{name: "defaults suffix", path: write("a.json", `{"module":"example.com/m"}`), wantSuffix: "FileDescriptor", wantMod: "example.com/m"},
		{name: "keeps suffix", path: write("b.json", `{"module":"m","fdFieldSuffix":"Fd"}`), wantSuffix: "Fd", wantMod: "m"},
		{name: "empty object", path: write("c.json", `{}`), wantSuffix: "FileDescriptor"},
		{name: "missing file", path: filepath.Join(dir, "nope.json"), wantErr: "no such file"},
		{name: "bad json", path: write("d.json", `{"module":`), wantErr: "d.json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := LoadConfig(tc.path)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				if cfg != nil {
					t.Errorf("cfg = %+v, want nil", cfg)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.FDFieldSuffix != tc.wantSuffix || cfg.Module != tc.wantMod {
				t.Errorf("cfg = %+v, want suffix %q module %q", cfg, tc.wantSuffix, tc.wantMod)
			}
		})
	}
}

func TestIsUnstable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		unstable []string
		iface    string
		want     bool
	}{
		{name: "listed", unstable: []string{"a.B", "c.D"}, iface: "c.D", want: true},
		{name: "not listed", unstable: []string{"a.B"}, iface: "c.D", want: false},
		{name: "empty list", iface: "a.B", want: false},
		{name: "empty name", unstable: []string{"a.B"}, iface: "", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := &Config{Unstable: tc.unstable}
			if got := c.IsUnstable(tc.iface); got != tc.want {
				t.Errorf("IsUnstable(%q) = %v, want %v", tc.iface, got, tc.want)
			}
		})
	}
}

func TestApplyFieldTypes(t *testing.T) {
	t.Parallel()
	const src = "interface x.A\ntype T(f: int)\nmethod M(in: int) -> (out: int)\n"
	tests := []struct {
		name    string
		types   map[string]map[string]string
		version string
		wantErr string
		check   func(t *testing.T, i *idl.Interface)
	}{
		{
			name:    "no entries for version",
			types:   map[string]map[string]string{"1": {"x.A.T.f": "string"}},
			version: "2",
			check: func(t *testing.T, i *idl.Interface) {
				if got := i.Type("T").Type.String(); got != "(f: int)" {
					t.Errorf("T = %s", got)
				}
			},
		},
		{
			name:    "retypes type field",
			types:   map[string]map[string]string{"1": {"x.A.T.f": "?string"}},
			version: "1",
			check: func(t *testing.T, i *idl.Interface) {
				if got := i.Type("T").Type.String(); got != "(f: ?string)" {
					t.Errorf("T = %s", got)
				}
			},
		},
		{
			name:    "retypes method input and output",
			types:   map[string]map[string]string{"1": {"x.A.M.in": "bool", "x.A.M.out": "[]string"}},
			version: "1",
			check: func(t *testing.T, i *idl.Interface) {
				m := i.Method("M")
				if m.In.String() != "(in: bool)" || m.Out.String() != "(out: []string)" {
					t.Errorf("M = %s -> %s", m.In, m.Out)
				}
			},
		},
		{
			name:    "other interface is ignored",
			types:   map[string]map[string]string{"1": {"y.B.T.f": "string", "x.AB.T.f": "string"}},
			version: "1",
			check: func(t *testing.T, i *idl.Interface) {
				if got := i.Type("T").Type.String(); got != "(f: int)" {
					t.Errorf("T = %s", got)
				}
			},
		},
		{
			name:    "path with wrong depth is ignored",
			types:   map[string]map[string]string{"1": {"x.A.f": "string", "x.A.T.f.g": "string"}},
			version: "1",
			check: func(t *testing.T, i *idl.Interface) {
				if got := i.Type("T").Type.String(); got != "(f: int)" {
					t.Errorf("T = %s", got)
				}
			},
		},
		{
			name:    "bad type",
			types:   map[string]map[string]string{"1": {"x.A.T.f": "(("}},
			version: "1",
			wantErr: "fieldTypes \"x.A.T.f\"",
		},
		{
			name:    "unknown field",
			types:   map[string]map[string]string{"1": {"x.A.T.nope": "string"}},
			version: "1",
			wantErr: "matches no field in systemd v1",
		},
		{
			name:    "unknown owner",
			types:   map[string]map[string]string{"1": {"x.A.Nope.f": "string"}},
			version: "1",
			wantErr: "matches no field",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			iface, err := idl.Parse(src)
			if err != nil {
				t.Fatal(err)
			}
			cfg := &Config{FieldTypes: tc.types}
			err = cfg.ApplyFieldTypes(iface, tc.version)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, iface)
		})
	}
}

func TestPackageBadVersion(t *testing.T) {
	t.Parallel()
	iface, err := idl.Parse("interface x.A\n")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "bad version") {
			t.Errorf("recover = %v, want bad version panic", r)
		}
	}()
	_, _ = Package(testConfig, iface, "x.A.varlink", "abc")
	t.Error("Package did not panic")
}

func TestNumbered(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: "   1 \n"},
		{name: "two lines", in: "a\nb", want: "   1 a\n   2 b\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := numbered([]byte(tc.in)); got != tc.want {
				t.Errorf("numbered(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestWriteDoc(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		indent string
		doc    string
		want   string
	}{
		{name: "single line", doc: "Hello.", want: "// Hello.\n"},
		{name: "blank line and trailing space", indent: "\t", doc: "A  \n\nB", want: "\t// A\n\t//\n\t// B\n"},
		{name: "empty doc", doc: "", want: "//\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var b bytes.Buffer
			writeDoc(&b, tc.indent, tc.doc)
			if got := b.String(); got != tc.want {
				t.Errorf("writeDoc = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSortedKeys(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   map[string]int
		want string
	}{
		{name: "nil", in: nil, want: ""},
		{name: "sorted", in: map[string]int{"b": 1, "a": 2, "c": 3}, want: "a,b,c"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := strings.Join(sortedKeys(tc.in), ","); got != tc.want {
				t.Errorf("sortedKeys = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClaim(t *testing.T) {
	t.Parallel()
	g := &pkgGen{taken: map[string]bool{"Foo": true}}
	for _, tc := range []struct{ in, want string }{
		{"Foo", "Foo_"}, {"Foo", "Foo__"}, {"Bar", "Bar"}, {"Bar", "Bar_"},
	} {
		if got := g.claim(tc.in); got != tc.want {
			t.Errorf("claim(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
