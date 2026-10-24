package gen

import (
	"strings"
	"testing"

	"github.com/rossedman/sdvarlink/idl"
)

func mustParse(t *testing.T, src string) *idl.Interface {
	t.Helper()
	iface, err := idl.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	return iface
}

func TestReferenceDocs(t *testing.T) {
	t.Parallel()
	v1 := mustParse(t, "interface io.systemd.Demo\nmethod Old() -> ()\n")
	v2 := mustParse(t, `# Demo docs.
interface io.systemd.Demo

# Kinds | of things.
type Kind(
	# The a.
	a,
	b
)

type Rec(name: string, kind: ?Kind)

# Streams.
method Watch(pathFileDescriptor: int) -> (item: string)
method Old() -> ()
method Fresh(x: int) -> ()

error Gone(why: string)
error Oops()
`)
	v2.Methods[0].More = idl.MoreRequired
	extra := mustParse(t, "interface io.systemd.Extra\nmethod Ping() -> ()\n")
	cfg := &Config{
		Module:        "example.com/m",
		Sockets:       map[string]string{"io.systemd.Demo": "/run/demo"},
		FDFieldSuffix: "FileDescriptor",
	}
	pages := ReferenceDocs(cfg, []Release{
		{Version: "1", Interfaces: map[string]*idl.Interface{"io.systemd.Demo": v1}},
		{Version: "2", Interfaces: map[string]*idl.Interface{"io.systemd.Demo": v2, "io.systemd.Extra": extra}},
	})

	byPath := map[string]Page{}
	var order []string
	for _, p := range pages {
		byPath[p.Path] = p
		order = append(order, p.Path)
	}
	if got, want := strings.Join(order, ","), "interfaces.md,interfaces/demo.md,interfaces/extra.md,api-changes.md"; got != want {
		t.Fatalf("pages = %s, want %s", got, want)
	}
	if byPath["interfaces/demo.md"].NavTitle != "io.systemd.Demo" || byPath["interfaces.md"].NavTitle != "" {
		t.Errorf("nav titles = %q / %q", byPath["interfaces/demo.md"].NavTitle, byPath["interfaces.md"].NavTitle)
	}

	tests := []struct {
		name string
		page string
		want []string
		not  []string
	}{
		{
			name: "overview",
			page: "interfaces.md",
			want: []string{
				"(v1, v2)",
				"**systemd v2**",
				"| [`io.systemd.Demo`](interfaces/demo.md) | `demo` | 3 | v1 | `/run/demo` |",
				"| [`io.systemd.Extra`](interfaces/extra.md) | `extra` | 1 | v2 | — |",
				"\"example.com/m/systemd/v1/resolve\"   // pinned",
				"\"example.com/m/systemd/latest/resolve\" // newest",
			},
		},
		{
			name: "interface header",
			page: "interfaces/demo.md",
			want: []string{
				"# io.systemd.Demo\n",
				"Demo docs.\n",
				"| Go package | [`example.com/m/systemd/latest/demo`](https://pkg.go.dev/example.com/m/systemd/latest/demo) |",
				"| Socket | `/run/demo` |",
				"| Available since | systemd v1 |",
				"| Methods · types · errors | 3 · 2 · 2 |",
			},
		},
		{
			name: "methods",
			page: "interfaces/demo.md",
			want: []string{
				"### Watch\n\n:material-waves: streaming (requires `more`) · :material-new-box: since v2 · :material-file-swap: passes file descriptors\n",
				"Streams.\n",
				"func (c *Client) Watch(ctx context.Context, in WatchIn) iter.Seq2[*WatchOut, error]",
				"**Parameters**\n\n| Field | Type | Description |",
				"| `pathFileDescriptor` | `int` |  |",
				"**Reply**\n",
				"### Fresh\n\n:material-new-box: since v2\n",
				"func (c *Client) Fresh(ctx context.Context, in FreshIn) error",
				"func (c *Client) Old(ctx context.Context) error",
			},
		},
		{
			name: "types",
			page: "interfaces/demo.md",
			want: []string{
				"### Kind\n\nKinds | of things.\n",
				"| Value | Description |",
				"| `a` | The a. |",
				"| `b` |  |",
				"### Rec\n",
				"| `kind` | `?Kind` |  |",
			},
		},
		{
			name: "errors",
			page: "interfaces/demo.md",
			want: []string{
				"| Error | Go type | Description |",
				"| `Gone` | `GoneError` |  |",
				"| `Oops` | `OopsError` |  |",
			},
		},
		{
			name: "interface without socket or sections",
			page: "interfaces/extra.md",
			want: []string{"| Available since | systemd v2 |", "| Methods · types · errors | 1 · 0 · 0 |"},
			not:  []string{"| Socket |", "## Types", "## Errors", "passes file descriptors", ":material-new-box:"},
		},
		{
			name: "api changes",
			page: "api-changes.md",
			want: []string{
				"# API changes",
				"## systemd v1 → v2",
				"| `io.systemd.Demo` | method | `Fresh` | :material-plus-circle:{ .added } added |",
				"| `io.systemd.Extra` | interface | `io.systemd.Extra` | :material-plus-circle:{ .added } added | 1 methods |",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			content := byPath[tc.page].Content
			if content == "" {
				t.Fatalf("page %s missing", tc.page)
			}
			for _, w := range tc.want {
				if !strings.Contains(content, w) {
					t.Errorf("%s lacks %q\n%s", tc.page, w, content)
				}
			}
			for _, n := range tc.not {
				if strings.Contains(content, n) {
					t.Errorf("%s unexpectedly contains %q", tc.page, n)
				}
			}
		})
	}
}

func TestReferenceDocsSingleRelease(t *testing.T) {
	t.Parallel()
	iface := mustParse(t, "interface io.systemd.Solo\nmethod Ping() -> ()\n")
	pages := ReferenceDocs(&Config{Module: "m"}, []Release{{Version: "9", Interfaces: map[string]*idl.Interface{"io.systemd.Solo": iface}}})
	if len(pages) != 3 {
		t.Fatalf("got %d pages, want 3", len(pages))
	}
	changes := pages[2]
	if changes.Path != "api-changes.md" || strings.Contains(changes.Content, "## systemd") {
		t.Errorf("api-changes = %+v", changes)
	}
	if !strings.Contains(pages[0].Content, "(v9)") {
		t.Errorf("overview lacks version list:\n%s", pages[0].Content)
	}
}

func TestHasFD(t *testing.T) {
	t.Parallel()
	cfg := &Config{FDFieldSuffix: "FileDescriptor"}
	tests := []struct {
		name string
		typ  string
		want bool
	}{
		{name: "int fd", typ: "(imageFileDescriptor: int)", want: true},
		{name: "nullable int fd", typ: "(imageFileDescriptor: ?int)", want: true},
		{name: "case insensitive", typ: "(imagefiledescriptor: int)", want: true},
		{name: "wrong type", typ: "(imageFileDescriptor: string)", want: false},
		{name: "wrong name", typ: "(image: int)", want: false},
		{name: "no fields", typ: "()", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			typ, err := idl.ParseType(tc.typ)
			if err != nil {
				t.Fatal(err)
			}
			if got := hasFD(cfg, typ); got != tc.want {
				t.Errorf("hasFD(%s) = %v, want %v", tc.typ, got, tc.want)
			}
		})
	}
}

func TestGoSignature(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "single with in and out",
			src:  "method Get(a: int) -> (b: int)",
			want: "func (c *Client) Get(ctx context.Context, in GetIn) (*GetOut, error)",
		},
		{
			name: "single without params",
			src:  "method Ping() -> ()",
			want: "func (c *Client) Ping(ctx context.Context) error",
		},
		{
			name: "requires more",
			src:  "# [Requires 'more' flag]\nmethod Watch() -> (x: int)",
			want: "func (c *Client) Watch(ctx context.Context) iter.Seq2[*WatchOut, error]",
		},
		{
			name: "requires more without output",
			src:  "# [Requires 'more' flag]\nmethod Watch(a: int) -> ()",
			want: "func (c *Client) Watch(ctx context.Context, in WatchIn) iter.Seq2[*struct{}, error]",
		},
		{
			name: "supports more",
			src:  "# [Supports 'more' flag]\nmethod List(f: int) -> (x: int)",
			want: "func (c *Client) List(ctx context.Context, in ListIn) (*ListOut, error)\n" +
				"func (c *Client) ListMore(ctx context.Context, in ListIn) iter.Seq2[*ListOut, error]",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			iface := mustParse(t, "interface x.A\n"+tc.src+"\n")
			if got := goSignature(iface.Methods[0]); got != tc.want {
				t.Errorf("goSignature = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCell(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"plain", "abc", "abc"},
		{"pipe", "a|b", "a\\|b"},
		{"newline", "a\nb", "a b"},
		{"both", "|\n|", "\\| \\|"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := cell(tc.in); got != tc.want {
				t.Errorf("cell(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
