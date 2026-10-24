package gen

import (
	"strings"
	"testing"

	"github.com/rossedman/sdvarlink/idl"
)

func TestDiffMarkdown(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		changes []Change
		want    []string
	}{
		{
			name: "no changes",
			want: []string{"## systemd v1 → v2\n\nNo changes to the Varlink API.\n\n"},
		},
		{
			name: "mixed",
			changes: []Change{
				{Interface: "x.A", Kind: "method", Name: "New", Op: "added"},
				{Interface: "x.A", Kind: "type", Name: "T", Op: "changed", Detail: "+ a | b"},
				{Interface: "x.B", Kind: "interface", Name: "x.B", Op: "removed"},
				{Interface: "x.A", Kind: "error", Name: "E", Op: "added"},
			},
			want: []string{
				"2 added · 1 changed · 1 removed\n",
				"| `x.A` | method | `New` | :material-plus-circle:{ .added } added |  |",
				"| `x.A` | type | `T` | :material-pencil-circle:{ .changed } changed | + a \\| b |",
				"| `x.B` | interface | `x.B` | :material-minus-circle:{ .removed } removed |  |",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := DiffMarkdown("1", "2", tc.changes)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in\n%s", w, got)
				}
			}
		})
	}
}

func TestDiffRemovedInterfaceAndSignatures(t *testing.T) {
	t.Parallel()
	a := mustParse(t, "interface x.A\ntype T(a: int)\nmethod M() -> ()\nmethod S() -> ()\nerror E(a: int)\n")
	b := mustParse(t, "interface x.A\ntype T(a: int, b: string)\n# [Supports 'more' flag]\nmethod M() -> ()\nmethod S() -> ()\nerror E(a: string)\n")
	gone := mustParse(t, "interface x.Gone\n")
	changes := Diff(map[string]*idl.Interface{"x.A": a, "x.Gone": gone}, map[string]*idl.Interface{"x.A": b})
	got := map[string]string{}
	for _, c := range changes {
		got[c.Kind+" "+c.Name] = c.Op + " " + c.Detail
	}
	want := map[string]string{
		"interface x.Gone": "removed ",
		"method M":         "changed streaming or nested type changed",
		"type T":           "changed + b: string",
		"error E":          "changed + a: string; − a: int",
	}
	if len(got) != len(want) {
		t.Errorf("changes = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
}

func TestFieldDelta(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, old, new, want string
	}{
		{"added", "(a: int)", "(a: int, b: int)", "+ b: int"},
		{"removed", "(a: int, b: int)", "(a: int)", "− b: int"},
		{"both", "(a: int)", "(b: int)", "+ b: int; − a: int"},
		{"output only", "(a: int) -> (x: int)", "(a: int) -> (x: int, y: int)", "+ → y: int"},
		{"identical members", "(a: int)", "(a: int)", "streaming or nested type changed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := fieldDelta(tc.old, tc.new); got != tc.want {
				t.Errorf("fieldDelta = %q, want %q", got, tc.want)
			}
		})
	}
}
