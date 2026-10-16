package gen

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rossedman/sdvarlink/idl"
)

var update = flag.Bool("update", false, "rewrite golden files")

var testConfig = &Config{
	Module:        "github.com/rossedman/sdvarlink",
	Sockets:       map[string]string{"com.example.Demo": "/run/demo"},
	FDFieldSuffix: "FileDescriptor",
}

func TestGoName(t *testing.T) {
	for in, want := range map[string]string{
		"uid":                 "UID",
		"machineId":           "MachineID",
		"horiz_pre":           "HorizPre",
		"ifindex":             "Ifindex",
		"DNSSECValidation":    "DNSSECValidation",
		"imageFileDescriptor": "ImageFileDescriptor",
		"sshAddress":          "SSHAddress",
		"invocationID":        "InvocationID",
		"9lives":              "X9lives",
		"StaticHostname":      "StaticHostname",
		"io.systemd.Resolve":  "IOSystemdResolve",
	} {
		if got := GoName(in); got != want {
			t.Errorf("GoName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPackageName(t *testing.T) {
	for in, want := range map[string]string{
		"io.systemd.Resolve":         "resolve",
		"io.systemd.Resolve.Monitor": "resolvemonitor",
		"io.systemd":                 "systemd",
		"io.systemd.Import":          "importd",
		"io.systemd.oom.Prekill":     "oomprekill",
	} {
		if got := PackageName(in); got != want {
			t.Errorf("PackageName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGolden generates the demo interface and compares with the golden file.
func TestGolden(t *testing.T) {
	src, err := os.ReadFile("testdata/com.example.Demo.varlink")
	if err != nil {
		t.Fatal(err)
	}
	iface, err := idl.Parse(string(src))
	if err != nil {
		t.Fatal(err)
	}
	files, err := Package(testConfig, iface, "com.example.Demo.varlink", "999")
	if err != nil {
		t.Fatal(err)
	}
	got := files[0].Content
	golden := filepath.Join("testdata", "demo.go.golden")
	if *update {
		os.WriteFile(golden, got, 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run go test -update)", err)
	}
	if string(got) != string(want) {
		t.Fatalf("generated code differs from %s; run go test ./internal/gen -update and review the diff", golden)
	}
	for _, s := range []string{
		"func (c *Client) Get(ctx context.Context, in GetIn) (*GetOut, error)",
		"func (c *Client) Watch(ctx context.Context) iter.Seq2[*WatchOut, error]",
		"func (c *Client) List(ctx context.Context, in ListIn) (*ListOut, error)",
		"func (c *Client) ListMore(ctx context.Context, in ListIn) iter.Seq2[*ListOut, error]",
		"func (c *Client) Ping(ctx context.Context) error",
		"func (c *Client) CloseMethod(ctx context.Context) error",
		"PathFileDescriptor *os.File `json:\"-\" varlink:\"pathFileDescriptor,fd\"`",
		"FileDescriptor *os.File `json:\"-\" varlink:\"fileDescriptor,fd\"`",
		"type ItemNested struct",
		"type ItemNestedMode string",
		"VolumeQuiet Volume = \"quiet\"",
		"var ErrNoSuchItem = &varlink.Error{Name: \"com.example.Demo.NoSuchItem\"}",
		"Attrs map[string]string `json:\"attrs,omitempty\"`",
		"Blob json.RawMessage `json:\"blob,omitempty\"`",
		"Volume *Volume `json:\"volume,omitempty\"`",
		"UID *int64 `json:\"uid,omitempty\"`",
		"Name string `json:\"name\"`",
		"DefaultSocket = \"/run/demo\"",
	} {
		if !strings.Contains(strings.Join(strings.Fields(string(got)), " "), strings.Join(strings.Fields(s), " ")) {
			t.Errorf("generated code lacks %q", s)
		}
	}
}

func TestDiff(t *testing.T) {
	a, _ := idl.Parse("interface x.A\nmethod Keep(a: int) -> ()\nmethod Gone() -> ()\nmethod Change(a: int) -> ()\n")
	b, _ := idl.Parse("interface x.A\nmethod Keep(a: int) -> ()\nmethod Change(a: int, b: ?string) -> ()\nmethod New() -> ()\nerror Oops()\n")
	c, _ := idl.Parse("interface x.B\n")
	changes := Diff(map[string]*idl.Interface{"x.A": a}, map[string]*idl.Interface{"x.A": b, "x.B": c})
	got := map[string]string{}
	for _, ch := range changes {
		got[ch.Kind+" "+ch.Name] = ch.Op + " " + ch.Detail
	}
	want := map[string]string{
		"method Gone":   "removed ",
		"method New":    "added ",
		"method Change": "changed + b: ?string",
		"error Oops":    "added ",
		"interface x.B": "added 0 methods",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("changes = %v", got)
	}
}

// TestCollisionsCompile generates an interface whose names collide with
// generated ones and type-checks the result.
func TestCollisionsCompile(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go vet")
	}
	src, err := os.ReadFile("testdata/collisions.varlink")
	if err != nil {
		t.Fatal(err)
	}
	iface, err := idl.Parse(string(src))
	if err != nil {
		t.Fatal(err)
	}
	files, err := Package(testConfig, iface, "collisions.varlink", "1")
	if err != nil {
		t.Fatal(err)
	}
	// The package must live inside the module to import varlink.
	dir, err := os.MkdirTemp(".", "zz-collisions-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	os.WriteFile(filepath.Join(dir, files[0].Name), files[0].Content, 0o644)
	os.WriteFile(filepath.Join(dir, "collisions.varlink"), src, 0o644)
	out, err := exec.Command("go", "vet", "./"+dir).CombinedOutput()
	if err != nil {
		t.Fatalf("generated code does not compile: %v\n%s", err, out)
	}
	for _, s := range []string{"func (c *Client) FooMore(", "func (c *Client) FooMore_(", "func (c *Client) CloseMethod(", "func (c *Client) CloseMethod_("} {
		if !strings.Contains(string(files[0].Content), s) {
			t.Errorf("missing %q", s)
		}
	}
}
