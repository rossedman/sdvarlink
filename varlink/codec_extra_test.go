package varlink

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type fdEmbedBase struct {
	E *os.File `json:"-" varlink:"e,fd"`
}

type fdWithEmbed struct {
	fdEmbedBase
	Arr    [2]fdInner         `json:"arr"`
	Map    map[string]fdInner `json:"map"`
	Hidden *fdInner           `json:"-"`
	Name   string             `json:"name"`
}

func TestMarshalEdgeCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		v       any
		want    string
		wantErr bool
	}{
		{name: "nil", v: nil, want: `{}`},
		{name: "empty raw", v: json.RawMessage(nil), want: `{}`},
		{name: "raw passes through", v: json.RawMessage(`{"a":1}`), want: `{"a":1}`},
		{name: "plain struct", v: struct {
			A int `json:"a"`
		}{3}, want: `{"a":3}`},
		{name: "unmarshalable", v: make(chan int), wantErr: true},
		{name: "fd struct without files", v: fdOuter{Plain: "x"}, want: `{"list":null,"plain":"x"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data, fs, err := Marshal(tc.v)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v", err)
			}
			if tc.wantErr {
				return
			}
			if string(data) != tc.want || len(fs) != 0 {
				t.Errorf("Marshal = %s, %v; want %s", data, fs, tc.want)
			}
		})
	}
}

func TestMarshalEmbeddedArrayAndMap(t *testing.T) {
	t.Parallel()
	fs := files(t, 4)
	v := fdWithEmbed{
		fdEmbedBase: fdEmbedBase{E: fs[0]},
		Arr:         [2]fdInner{{"a", fs[1]}, {"b", fs[2]}},
		Map:         map[string]fdInner{"y": {"y", fs[3]}},
		Name:        "n",
	}
	data, got, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0] != fs[0] || got[1] != fs[1] || got[2] != fs[2] || got[3] != fs[3] {
		t.Fatalf("files = %v", got)
	}
	var tree map[string]any
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatal(err)
	}
	if tree["e"] != 0.0 || tree["name"] != "n" {
		t.Errorf("json = %s", data)
	}
	if _, ok := tree["Hidden"]; ok {
		t.Errorf("hidden field encoded: %s", data)
	}
	arr := tree["arr"].([]any)
	if arr[0].(map[string]any)["fd"] != 1.0 || arr[1].(map[string]any)["fd"] != 2.0 {
		t.Errorf("array fds: %s", data)
	}
	if tree["map"].(map[string]any)["y"].(map[string]any)["fd"] != 3.0 {
		t.Errorf("map fds: %s", data)
	}

	// Round trip through Unmarshal with duplicates of the files.
	recv := make([]*os.File, len(got))
	for i, f := range got {
		d, err := os.Open(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		recv[i] = d
	}
	var out fdWithEmbed
	if err := Unmarshal(data, recv, &out); err != nil {
		t.Fatal(err)
	}
	if out.E != recv[0] || out.Arr[0].File != recv[1] || out.Arr[1].File != recv[2] || out.Map["y"].File != recv[3] || out.Name != "n" {
		t.Fatalf("out = %+v", out)
	}
}

func TestUnmarshalEdgeCases(t *testing.T) {
	t.Parallel()
	type plain struct {
		A int `json:"a"`
	}
	t.Run("nil target", func(t *testing.T) {
		t.Parallel()
		if err := Unmarshal([]byte(`{"a":1}`), nil, nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("raw message", func(t *testing.T) {
		t.Parallel()
		raw := json.RawMessage(`old old old`)
		if err := Unmarshal([]byte(`{"a":1}`), nil, &raw); err != nil || string(raw) != `{"a":1}` {
			t.Fatalf("raw = %s, %v", raw, err)
		}
	})
	for _, data := range []string{"", "null"} {
		t.Run("empty data "+data, func(t *testing.T) {
			t.Parallel()
			p := plain{A: 5}
			if err := Unmarshal([]byte(data), nil, &p); err != nil || p.A != 5 {
				t.Fatalf("p = %+v, %v", p, err)
			}
		})
	}
	t.Run("invalid json", func(t *testing.T) {
		t.Parallel()
		if err := Unmarshal([]byte(`{`), nil, &plain{}); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestUnmarshalFdErrors(t *testing.T) {
	t.Parallel()
	type withFd struct {
		F *os.File `json:"-" varlink:"f,fd"`
		G *os.File `json:"-" varlink:"g,fd"`
	}
	tests := []struct {
		name    string
		data    string
		nfiles  int
		wantErr string
		wantF   int // index of the file F should hold, -1 for nil
		wantG   int
	}{
		{name: "absent", data: `{}`, nfiles: 1, wantF: -1, wantG: -1},
		{name: "null", data: `{"f":null}`, nfiles: 1, wantF: -1, wantG: -1},
		{name: "valid", data: `{"f":0,"g":1}`, nfiles: 2, wantF: 0, wantG: 1},
		{name: "last index", data: `{"f":1}`, nfiles: 2, wantF: 1, wantG: -1},
		{name: "not a number", data: `{"f":"x"}`, nfiles: 1, wantErr: "is not an integer"},
		{name: "fraction", data: `{"f":0.5}`, nfiles: 1, wantErr: "references index 0.5"},
		{name: "negative", data: `{"f":-1}`, nfiles: 1, wantErr: "references index -1"},
		{name: "equal to count", data: `{"f":2}`, nfiles: 2, wantErr: "references index 2, but 2 files"},
		{name: "second field bad", data: `{"f":0,"g":9}`, nfiles: 1, wantErr: "references index 9"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			orig := files(t, tc.nfiles)
			recv := make([]*os.File, len(orig))
			for i, f := range orig {
				d, err := os.Open(f.Name())
				if err != nil {
					t.Fatal(err)
				}
				recv[i] = d
				t.Cleanup(func() { d.Close() })
			}
			var out withFd
			err := Unmarshal([]byte(tc.data), recv, &out)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				if out.F != nil || out.G != nil {
					t.Errorf("files left in a failed decode: %+v", out)
				}
				for i, f := range recv {
					if _, serr := f.Stat(); serr == nil {
						t.Errorf("received file %d left open after failure", i)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			check := func(name string, got *os.File, idx int) {
				switch {
				case idx < 0 && got != nil:
					t.Errorf("%s = %v, want nil", name, got)
				case idx >= 0 && got != recv[idx]:
					t.Errorf("%s holds the wrong file", name)
				}
			}
			check("F", out.F, tc.wantF)
			check("G", out.G, tc.wantG)
		})
	}
}

func TestUnmarshalSharedIndexDuplicates(t *testing.T) {
	t.Parallel()
	type twoFd struct {
		A *os.File `json:"-" varlink:"a,fd"`
		B *os.File `json:"-" varlink:"b,fd"`
	}
	orig := files(t, 1)
	recv, err := os.Open(orig[0].Name())
	if err != nil {
		t.Fatal(err)
	}
	var out twoFd
	if err := Unmarshal([]byte(`{"a":0,"b":0}`), []*os.File{recv}, &out); err != nil {
		t.Fatal(err)
	}
	defer out.A.Close()
	defer out.B.Close()
	if out.A == nil || out.B == nil || out.A == out.B || out.A.Fd() == out.B.Fd() {
		t.Fatalf("shared index did not produce two descriptors: %+v", out)
	}
	if out.A != recv {
		t.Error("first claim should keep the original file")
	}
}
