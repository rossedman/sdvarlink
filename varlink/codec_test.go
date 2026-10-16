package varlink

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type fdInner struct {
	Name string   `json:"name"`
	File *os.File `json:"-" varlink:"fd,fd"`
}

type fdOuter struct {
	Top    *os.File           `json:"-" varlink:"topFileDescriptor,fd"`
	List   []fdInner          `json:"list"`
	ByName map[string]fdInner `json:"byName,omitempty"`
	Ptr    *fdInner           `json:"ptr,omitempty"`
	Plain  string             `json:"plain"`
}

func files(t *testing.T, n int) []*os.File {
	t.Helper()
	out := make([]*os.File, n)
	for i := range out {
		f, err := os.CreateTemp(t.TempDir(), "f")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		out[i] = f
	}
	return out
}

func TestMarshalFiles(t *testing.T) {
	fs := files(t, 4)
	v := fdOuter{
		Top:    fs[0],
		List:   []fdInner{{"a", fs[1]}, {"b", nil}},
		ByName: map[string]fdInner{"z": {"z", fs[3]}},
		Ptr:    &fdInner{"p", fs[2]},
		Plain:  "x",
	}
	data, got, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	// Depth-first field order: Top, List[0], ByName["z"], Ptr.
	want := []*os.File{fs[0], fs[1], fs[3], fs[2]}
	if len(got) != len(want) {
		t.Fatalf("files = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("file %d mismatch", i)
		}
	}
	var tree map[string]any
	json.Unmarshal(data, &tree)
	if tree["topFileDescriptor"] != 0.0 || tree["list"].([]any)[0].(map[string]any)["fd"] != 1.0 {
		t.Fatalf("json = %s", data)
	}
	if _, ok := tree["list"].([]any)[1].(map[string]any)["fd"]; ok {
		t.Fatalf("nil file encoded: %s", data)
	}
}

func TestUnmarshalFilesRoundTrip(t *testing.T) {
	fs := files(t, 3)
	in := fdOuter{Top: fs[0], List: []fdInner{{"a", fs[1]}}, Ptr: &fdInner{"p", fs[2]}}
	data, sent, err := Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the receiver getting duplicates of the sent files.
	recv := make([]*os.File, len(sent))
	for i, f := range sent {
		d, err := os.Open(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		recv[i] = d
	}
	var out fdOuter
	if err := Unmarshal(data, recv, &out); err != nil {
		t.Fatal(err)
	}
	if out.Top != recv[0] || out.List[0].File != recv[1] || out.Ptr.File != recv[2] {
		t.Fatal("files not attached to the right fields")
	}
	for _, f := range recv {
		f.Close()
	}
}

func TestUnmarshalClosesUnclaimed(t *testing.T) {
	fs := files(t, 2)
	recv := make([]*os.File, 2)
	for i, f := range fs {
		recv[i], _ = os.Open(f.Name())
	}
	var out struct {
		F *os.File `json:"-" varlink:"f,fd"`
	}
	if err := Unmarshal([]byte(`{"f":1}`), recv, &out); err != nil {
		t.Fatal(err)
	}
	if out.F != recv[1] {
		t.Fatal("wrong file claimed")
	}
	if _, err := recv[0].Stat(); err == nil {
		t.Fatal("unclaimed file still open")
	}
	out.F.Close()
	// Decoding into a type without fd fields closes everything.
	recv[0], _ = os.Open(fs[0].Name())
	if err := Unmarshal([]byte(`{}`), recv[:1], &struct{}{}); err != nil {
		t.Fatal(err)
	}
	if _, err := recv[0].Stat(); err == nil {
		t.Fatal("file still open after decode into fd-less type")
	}
}

func TestUnmarshalBadIndex(t *testing.T) {
	var out struct {
		F *os.File `json:"-" varlink:"f,fd"`
	}
	err := Unmarshal([]byte(`{"f":3}`), nil, &out)
	if err == nil || !strings.Contains(err.Error(), "index 3") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnknownFields(t *testing.T) {
	extra, err := UnknownFields([]byte(`{"a":1,"b":2,"c":{"d":3}}`), "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(extra) != 2 || string(extra["c"]) != `{"d":3}` {
		t.Fatalf("extra = %v", extra)
	}
	if extra, _ := UnknownFields([]byte(`{"a":1}`), "a"); extra != nil {
		t.Fatal("expected nil")
	}
}

func TestPtrDeref(t *testing.T) {
	if *Ptr(3) != 3 || Deref[string](nil) != "" || Deref(Ptr("x")) != "x" {
		t.Fatal("Ptr/Deref")
	}
}

func FuzzUnmarshal(f *testing.F) {
	f.Add([]byte(`{"topFileDescriptor":0,"list":[{"name":"a","fd":1}],"ptr":{"fd":2}}`), uint8(3))
	f.Add([]byte(`{"list":[{"fd":-1}]}`), uint8(1))
	f.Add([]byte(`null`), uint8(0))
	f.Fuzz(func(t *testing.T, data []byte, n uint8) {
		n %= 8
		fs := make([]*os.File, n)
		for i := range fs {
			r, w, err := os.Pipe()
			if err != nil {
				t.Skip(err)
			}
			w.Close()
			fs[i] = r
		}
		var out fdOuter
		Unmarshal(data, fs, &out)
		// Whatever happened, every file is either claimed or closed.
		claimed := map[*os.File]bool{}
		mark := func(f *os.File) {
			if f != nil {
				claimed[f] = true
			}
		}
		mark(out.Top)
		for _, x := range out.List {
			mark(x.File)
		}
		for _, x := range out.ByName {
			mark(x.File)
		}
		if out.Ptr != nil {
			mark(out.Ptr.File)
		}
		for _, f := range fs {
			if !claimed[f] {
				if _, err := f.Stat(); err == nil {
					t.Fatalf("unclaimed file left open for input %q", data)
				}
			}
		}
		for f := range claimed {
			f.Close()
		}
	})
}
