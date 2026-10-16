package idl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const resolveSnippet = `# Resolves names.
interface io.systemd.Example

# The protocol.
type Protocol(
	# DNS
	dns,
	mdns
)

type Address(
	ifindex: ?int,
	family: int,
	address: []int,
	labels: ?[string]string,
	inline: (a: bool, b: (x, y))
)

# [Requires 'more' flag]
# Lists things.
method List(
	# Which.
	name: ?string
) -> (
	addresses: []Address
)

# [Supports 'more' flag]
method Maybe() -> ()

error NoSuchThing(
	name: string
)
`

func TestParseSnippet(t *testing.T) {
	iface, err := Parse(resolveSnippet)
	if err != nil {
		t.Fatal(err)
	}
	if iface.Name != "io.systemd.Example" || iface.Doc != "Resolves names." {
		t.Fatalf("interface = %q doc %q", iface.Name, iface.Doc)
	}
	p := iface.Type("Protocol")
	if p == nil || p.Type.Kind != Enum || len(p.Type.Values) != 2 || p.Type.Values[0].Doc != "DNS" || p.Doc != "The protocol." {
		t.Fatalf("Protocol = %+v", p)
	}
	a := iface.Type("Address").Type
	want := "(ifindex: ?int, family: int, address: []int, labels: ?[string]string, inline: (a: bool, b: (x, y)))"
	if got := a.String(); got != want {
		t.Fatalf("Address = %s\nwant      %s", got, want)
	}
	l := iface.Method("List")
	if l.More != MoreRequired || l.Doc != "Lists things." || l.In.Fields[0].Doc != "Which." {
		t.Fatalf("List = %+v", l)
	}
	if iface.Method("Maybe").More != MoreSupported {
		t.Fatal("Maybe should support more")
	}
	if e := iface.Error("NoSuchThing"); e == nil || len(e.Params.Fields) != 1 {
		t.Fatalf("error = %+v", e)
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{
		"",
		"interface",
		"interface a.b\nmethod X(",
		"interface a.b\nmethod X() ->",
		"interface a.b\ntype T(a: int, b)",
		"interface a.b\nmethod X(a, b) -> ()",
		"interface a.b\nbogus X()",
		"interface a.b\ntype T(a: [int]string)",
	} {
		if _, err := Parse(src); err == nil {
			t.Errorf("Parse(%q) succeeded", src)
		}
	}
}

// TestParseSnapshots parses every committed systemd IDL snapshot and checks
// that all named types resolve.
func TestParseSnapshots(t *testing.T) {
	files, _ := filepath.Glob("v*/*.varlink")
	if len(files) == 0 {
		t.Skip("no snapshots")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		iface, err := Parse(string(data))
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if !strings.HasSuffix(f, iface.Name+".varlink") {
			t.Errorf("%s declares %s", f, iface.Name)
		}
		var check func(*Type)
		check = func(ty *Type) {
			switch ty.Kind {
			case Named:
				if iface.Type(ty.Name) == nil {
					t.Errorf("%s: unresolved type %s", f, ty.Name)
				}
			case Array, Map, Nullable:
				check(ty.Elem)
			case Struct:
				for _, fl := range ty.Fields {
					check(fl.Type)
				}
			}
		}
		for _, td := range iface.Types {
			check(td.Type)
		}
		for _, m := range iface.Methods {
			check(m.In)
			check(m.Out)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add(resolveSnippet)
	f.Fuzz(func(t *testing.T, src string) {
		iface, err := Parse(src)
		if err == nil && iface == nil {
			t.Fatal("nil interface without error")
		}
	})
}
