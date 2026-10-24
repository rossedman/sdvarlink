package idl

import (
	"errors"
	"strings"
	"testing"
)

func TestKindString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind Kind
		want string
	}{
		{Bool, "bool"},
		{Named, "named"},
		{Nullable, "nullable"},
		{Kind(99), "Kind(99)"},
		{Kind(-1), "Kind(-1)"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := tc.kind.String(); got != tc.want {
				t.Errorf("Kind(%d).String() = %q, want %q", int(tc.kind), got, tc.want)
			}
		})
	}
}

func TestMoreString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		more More
		want string
	}{
		{MoreNone, "single"},
		{MoreSupported, "supports-more"},
		{MoreRequired, "requires-more"},
		{More(42), "single"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := tc.more.String(); got != tc.want {
				t.Errorf("More(%d).String() = %q, want %q", int(tc.more), got, tc.want)
			}
		})
	}
}

func TestInterfaceLookups(t *testing.T) {
	t.Parallel()
	iface, err := Parse("interface x.A\ntype T(a: int)\nmethod M() -> ()\nerror E()\n")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		found bool
		got   any
	}{
		{"type hit", true, iface.Type("T")},
		{"type miss", false, iface.Type("Nope")},
		{"method hit", true, iface.Method("M")},
		{"method miss", false, iface.Method("Nope")},
		{"error hit", true, iface.Error("E")},
		{"error miss", false, iface.Error("Nope")},
		{"empty name", false, iface.Type("")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var isNil bool
			switch v := tc.got.(type) {
			case *TypeDef:
				isNil = v == nil
			case *Method:
				isNil = v == nil
			case *Error:
				isNil = v == nil
			}
			if isNil == tc.found {
				t.Errorf("found = %v, want %v", !isNil, tc.found)
			}
		})
	}
}

func TestSyntaxError(t *testing.T) {
	t.Parallel()
	e := &SyntaxError{Line: 3, Col: 7, Msg: "boom"}
	if got, want := e.Error(), "idl:3:7: boom"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	_, err := Parse("interface a.b\nbogus X()")
	var se *SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *SyntaxError", err)
	}
	if se.Line != 2 || se.Col != 1 {
		t.Errorf("position = %d:%d, want 2:1", se.Line, se.Col)
	}
}

func TestParseType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		src     string
		want    string
		kind    Kind
		wantErr string
	}{
		{name: "scalar", src: "string", want: "string", kind: String},
		{name: "nullable array", src: "?[]string", want: "?[]string", kind: Nullable},
		{name: "map", src: "[string]int", want: "[string]int", kind: Map},
		{name: "struct", src: "(a: int, b: ?bool)", want: "(a: int, b: ?bool)", kind: Struct},
		{name: "enum", src: "(on, off)", want: "(on, off)", kind: Enum},
		{name: "named", src: "Other", want: "Other", kind: Named},
		{name: "object", src: "object", want: "object", kind: Object},
		{name: "any", src: "any", want: "any", kind: Any},
		{name: "float", src: "float", want: "float", kind: Float},
		{name: "empty", src: "", wantErr: "bad type"},
		{name: "unclosed", src: "(a: int", wantErr: "bad type"},
		{name: "dangling nullable", src: "?", wantErr: "bad type"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseType(tc.src)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != tc.kind || got.String() != tc.want {
				t.Errorf("ParseType(%q) = %v %q, want %v %q", tc.src, got.Kind, got.String(), tc.kind, tc.want)
			}
		})
	}
}

func TestParseVariants(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		src     string
		wantErr string
		check   func(t *testing.T, i *Interface)
	}{
		{
			name: "only interface",
			src:  "interface a.b",
			check: func(t *testing.T, i *Interface) {
				if i.Name != "a.b" || len(i.Methods)+len(i.Types)+len(i.Errors) != 0 {
					t.Errorf("iface = %+v", i)
				}
			},
		},
		{
			name: "multi-line docs and CRLF-free comments",
			src:  "# one\n# two\ninterface a.b\n",
			check: func(t *testing.T, i *Interface) {
				if i.Doc != "one\ntwo" {
					t.Errorf("Doc = %q", i.Doc)
				}
			},
		},
		{
			name: "streaming markers set More",
			src:  "interface a.b\n# [Requires 'more' flag]\nmethod A() -> ()\n# [Supports 'more' flag]\nmethod B() -> ()\nmethod C() -> ()\n",
			check: func(t *testing.T, i *Interface) {
				got := i.Method("A").More.String() + "," + i.Method("B").More.String() + "," + i.Method("C").More.String()
				if got != "requires-more,supports-more,single" {
					t.Errorf("more = %s", got)
				}
			},
		},
		{
			name:    "enum where struct required",
			src:     "interface a.b\nmethod X(a, b) -> ()",
			wantErr: "expected",
		},
		{
			name:    "missing arrow",
			src:     "interface a.b\nmethod X() ()",
			wantErr: "expected",
		},
		{
			name:    "type without parens",
			src:     "interface a.b\ntype T int",
			wantErr: "expected",
		},
		{
			name:    "stray punctuation",
			src:     "interface a.b\n)",
			wantErr: "idl:2:",
		},
		{
			name:    "missing interface name",
			src:     "interface (",
			wantErr: "expected identifier",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			iface, err := Parse(tc.src)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				if iface != nil {
					t.Errorf("iface = %+v, want nil", iface)
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
