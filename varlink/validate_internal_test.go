package varlink

import (
	"encoding/json"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rossedman/sdvarlink/idl"
)

const validateIDL = `interface com.example.V

type Color(red, green)

type Point(x: int, y: ?int)

method M(
	b: bool,
	i: int,
	f: float,
	s: string,
	o: object,
	a: any,
	arr: []int,
	m: [string]int,
	c: Color,
	p: Point,
	n: ?string,
	ghost: Ghost
) -> ()
`

func mustIface(t *testing.T) (*idl.Interface, *idl.Type) {
	t.Helper()
	iface, err := idl.Parse(validateIDL)
	if err != nil {
		t.Fatal(err)
	}
	return iface, iface.Method("M").In
}

const goodParams = `{"b":true,"i":1,"f":1.5,"s":"x","o":{},"a":[1],"arr":[1,2],"m":{"k":1},"c":"red","p":{"x":1},"ghost":7}`

func TestValidateParams(t *testing.T) {
	t.Parallel()
	iface, in := mustIface(t)
	// with returns goodParams with the key replaced by raw (or removed when raw is "").
	with := func(key, raw string) string {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(goodParams), &m); err != nil {
			t.Fatal(err)
		}
		if raw == "" {
			delete(m, key)
		} else {
			m[key] = json.RawMessage(raw)
		}
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	tests := []struct {
		name         string
		params       string
		allowUnknown bool
		wantField    string
		wantErr      string
	}{
		{name: "valid", params: goodParams},
		{name: "valid with nullable set", params: with("n", `"s"`)},
		{name: "valid with nullable null", params: with("n", `null`)},
		{name: "max uint64 int", params: with("i", `18446744073709551615`)},
		{name: "min int64", params: with("i", `-9223372036854775808`)},
		{name: "float exponent", params: with("f", `1e3`)},
		{name: "bad json", params: `{`, wantField: "parameters", wantErr: "EOF"},
		{name: "not an object", params: `[1]`, wantField: "parameters", wantErr: "not an object"},
		{name: "missing required", params: with("s", ""), wantField: "s", wantErr: `missing required field "s"`},
		{name: "null required", params: with("s", "null"), wantField: "s", wantErr: "missing required"},
		{name: "bool wrong", params: with("b", `1`), wantField: "b", wantErr: "expected bool"},
		{name: "int is string", params: with("i", `"1"`), wantField: "i", wantErr: "expected int"},
		{name: "int fraction", params: with("i", `1.5`), wantField: "i", wantErr: "expected int, got 1.5"},
		{name: "int exponent", params: with("i", `1e2`), wantField: "i", wantErr: "expected int, got 1e2"},
		{name: "int overflow", params: with("i", `18446744073709551616`), wantField: "i", wantErr: "expected int"},
		{name: "int below int64", params: with("i", `-9223372036854775809`), wantField: "i", wantErr: "expected int"},
		{name: "float is string", params: with("f", `"x"`), wantField: "f", wantErr: "expected float"},
		{name: "string wrong", params: with("s", `1`), wantField: "s", wantErr: "expected string"},
		{name: "object wrong", params: with("o", `[]`), wantField: "o", wantErr: "expected object"},
		{name: "array wrong", params: with("arr", `{}`), wantField: "arr", wantErr: "expected array"},
		{name: "array element wrong", params: with("arr", `[1,"x"]`), wantField: "arr", wantErr: "expected int"},
		{name: "map wrong", params: with("m", `[]`), wantField: "m", wantErr: "expected object (map)"},
		{name: "map element wrong", params: with("m", `{"k":"v"}`), wantField: "m", wantErr: "expected int"},
		{name: "enum not string", params: with("c", `1`), wantField: "c", wantErr: "expected enum string"},
		{name: "enum unknown", params: with("c", `"blue"`), wantField: "c", wantErr: `unknown enum value "blue"`},
		{name: "struct not object", params: with("p", `1`), wantField: "p", wantErr: "expected object"},
		{name: "struct missing field", params: with("p", `{}`), wantField: "p", wantErr: `missing required field "x"`},
		{name: "struct nested wrong", params: with("p", `{"x":"a"}`), wantField: "p", wantErr: "expected int"},
		{name: "struct unknown field", params: with("p", `{"x":1,"z":2}`), wantField: "p", wantErr: `field "z" not defined`},
		{name: "struct unknown field allowed", params: with("p", `{"x":1,"z":2}`), allowUnknown: true},
		{name: "unknown top-level", params: with("zzz", `1`), wantField: "zzz", wantErr: `field "zzz" not defined`},
		{name: "unknown top-level allowed", params: with("zzz", `1`), allowUnknown: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			field, err := validateParams(iface, in, []byte(tc.params), tc.allowUnknown)
			if tc.wantErr == "" {
				if err != nil || field != "" {
					t.Fatalf("got (%q, %v), want success", field, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
			if field != tc.wantField {
				t.Errorf("field = %q, want %q", field, tc.wantField)
			}
		})
	}
}

func TestValidateParamsEmptyInput(t *testing.T) {
	t.Parallel()
	iface, err := idl.Parse("interface com.example.E\nmethod A() -> ()\nmethod B(x: int) -> ()\n")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		method    string
		params    string
		wantField string
		wantErr   bool
	}{
		{name: "empty no fields", method: "A", params: ""},
		{name: "whitespace no fields", method: "A", params: "  \n"},
		{name: "null no fields", method: "A", params: " null "},
		{name: "empty required field", method: "B", params: "", wantField: "x", wantErr: true},
		{name: "null required field", method: "B", params: "null", wantField: "x", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			field, err := validateParams(iface, iface.Method(tc.method).In, []byte(tc.params), false)
			if (err != nil) != tc.wantErr || field != tc.wantField {
				t.Fatalf("got (%q, %v), want field %q err=%v", field, err, tc.wantField, tc.wantErr)
			}
		})
	}
}

func TestValidatorNestingLimit(t *testing.T) {
	t.Parallel()
	nest := func(depth int) (*idl.Type, any) {
		typ := &idl.Type{Kind: idl.Int}
		var val any = json.Number("1")
		for i := 0; i < depth; i++ {
			typ = &idl.Type{Kind: idl.Array, Elem: typ}
			val = []any{val}
		}
		return typ, val
	}
	tests := []struct {
		name    string
		depth   int
		wantErr bool
	}{
		{name: "at limit", depth: 63},
		{name: "over limit", depth: 64, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			typ, val := nest(tc.depth)
			err := validator{iface: &idl.Interface{}}.value(typ, val)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "nesting too deep") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestValidatorNullable(t *testing.T) {
	t.Parallel()
	nullableInt := &idl.Type{Kind: idl.Nullable, Elem: &idl.Type{Kind: idl.Int}}
	tests := []struct {
		name    string
		v       any
		wantErr bool
	}{
		{name: "nil", v: nil},
		{name: "value", v: json.Number("3")},
		{name: "wrong", v: "x", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := (validator{}).value(nullableInt, tc.v); (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

type checkStructVal struct {
	Name string `json:"name"`
	N    int64  `json:"n"`
}

type embeddedBase struct {
	Name string `json:"name"`
}

type checkEmbedded struct {
	embeddedBase
	N       int64  `json:"n"`
	Skipped string `json:"-"`
	private int
}

type checkEmbeddedPtr struct {
	*embeddedBase
	N int64 `json:"n"`
}

type checkOmit struct {
	Name string `json:"name,omitempty"`
}

type checkPtr struct {
	Name *string `json:"name"`
}

type checkTime struct {
	Name *big.Int `json:"name"`
}

type checkFd struct {
	F *os.File `varlink:"f,fd"`
}

type checkNoTag struct {
	Name string
}

type checkRecursive struct {
	Next *checkRecursive `json:"next"`
}

func TestCheckStruct(t *testing.T) {
	t.Parallel()
	const src = `interface com.example.C
type Rec(next: ?Rec)
method Struct(name: string, n: int) -> ()
method Opt(name: ?string) -> ()
method Req(name: string) -> ()
method Name(name: Color) -> ()
type Color(red, green)
method Fd(f: int) -> ()
method FdBad(f: string) -> ()
method Types(b: bool, i: int, f: float, s: string, o: object, a: any, arr: []int, m: [string]int, e: Color, st: Rec, u: Unknown) -> ()
method Recur() -> (r: Rec)
`
	iface, err := idl.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	in := func(m string) *idl.Type { return iface.Method(m).In }
	typeOf := func(v any) reflect.Type { return reflect.TypeOf(v) }
	tests := []struct {
		name    string
		goType  reflect.Type
		idl     *idl.Type
		output  bool
		wantErr string
	}{
		{name: "match", goType: typeOf(checkStructVal{}), idl: in("Struct")},
		{name: "pointer to struct", goType: typeOf(&checkStructVal{}), idl: in("Struct")},
		{name: "embedded", goType: typeOf(checkEmbedded{private: 1}) /* unexported fields are skipped */, idl: in("Struct")},
		{name: "embedded pointer", goType: typeOf(checkEmbeddedPtr{}), idl: in("Struct")},
		{name: "map for struct", goType: typeOf(map[string]any{}), idl: in("Struct")},
		{name: "raw message", goType: typeOf(json.RawMessage(nil)), idl: in("Struct")},
		{name: "interface", goType: typeOf((*any)(nil)).Elem(), idl: in("Struct")},
		{name: "not a struct", goType: typeOf(1), idl: in("Struct"), wantErr: "IDL struct, Go int"},
		{name: "map with int key", goType: typeOf(map[int]string{}), idl: in("Struct"), wantErr: "IDL struct, Go map"},
		{name: "extra go field", goType: typeOf(checkNoTag{}), idl: in("Struct"), wantErr: `Go field "Name" is not in the IDL`},
		{name: "output missing field", goType: typeOf(checkOmit{}), idl: in("Req"), output: false},
		{name: "output missing required", goType: typeOf(struct{}{}), idl: in("Req"), output: true, wantErr: `required IDL field "name" is missing`},
		{name: "output missing nullable ok", goType: typeOf(struct{}{}), idl: in("Opt"), output: true},
		{name: "output omitempty", goType: typeOf(checkOmit{}), idl: in("Req"), output: true, wantErr: "must not be a pointer or omitempty"},
		{name: "output pointer", goType: typeOf(checkPtr{}), idl: in("Req"), output: true, wantErr: "must not be a pointer or omitempty"},
		{name: "input omitempty ok", goType: typeOf(checkOmit{}), idl: in("Req"), output: false},
		{name: "output nullable pointer ok", goType: typeOf(checkPtr{}), idl: in("Opt"), output: true},
		{name: "custom marshaler pointer ok", goType: typeOf(checkTime{}), idl: in("Req"), output: true},
		{name: "fd int", goType: typeOf(checkFd{}), idl: in("Fd"), output: true},
		{name: "fd non-int", goType: typeOf(checkFd{}), idl: in("FdBad"), wantErr: `fd field "f" must be an int`},
		{name: "named enum string", goType: typeOf(checkStructVal{}), idl: in("Name"), wantErr: "is not in the IDL"},
		{name: "recursive", goType: typeOf(checkRecursive{}), idl: iface.Method("Recur").Out.Fields[0].Type},
		{name: "wrong field type", goType: typeOf(struct {
			Name int `json:"name"`
		}{}), idl: in("Req"), wantErr: "name: IDL string, Go int"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := checkStruct(iface, tc.goType, tc.idl, tc.output)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestCheckKinds(t *testing.T) {
	t.Parallel()
	iface := &idl.Interface{Types: []*idl.TypeDef{{Name: "Color", Type: &idl.Type{Kind: idl.Enum}}}}
	k := func(kind idl.Kind) *idl.Type { return &idl.Type{Kind: kind} }
	arr := &idl.Type{Kind: idl.Array, Elem: k(idl.Int)}
	mp := &idl.Type{Kind: idl.Map, Elem: k(idl.Int)}
	nullable := &idl.Type{Kind: idl.Nullable, Elem: k(idl.String)}
	named := &idl.Type{Kind: idl.Named, Name: "Color"}
	unknown := &idl.Type{Kind: idl.Named, Name: "Nope"}
	tests := []struct {
		name    string
		goType  reflect.Type
		t       *idl.Type
		wantErr string
	}{
		{name: "bool ok", goType: reflect.TypeOf(true), t: k(idl.Bool)},
		{name: "bool bad", goType: reflect.TypeOf(""), t: k(idl.Bool), wantErr: "IDL bool, Go string"},
		{name: "int ok", goType: reflect.TypeOf(uint16(0)), t: k(idl.Int)},
		{name: "int bad", goType: reflect.TypeOf(1.5), t: k(idl.Int), wantErr: "IDL int, Go float64"},
		{name: "int json.Number", goType: jsonNumberType, t: k(idl.Int)},
		{name: "float ok", goType: reflect.TypeOf(float32(0)), t: k(idl.Float)},
		{name: "float bad", goType: reflect.TypeOf(1), t: k(idl.Float), wantErr: "IDL float, Go int"},
		{name: "float json.Number", goType: jsonNumberType, t: k(idl.Float)},
		{name: "string ok", goType: reflect.TypeOf(""), t: k(idl.String)},
		{name: "string bad", goType: reflect.TypeOf(1), t: k(idl.String), wantErr: "IDL string, Go int"},
		{name: "enum bad", goType: reflect.TypeOf(1), t: k(idl.Enum), wantErr: "IDL enum, Go int"},
		{name: "object map", goType: reflect.TypeOf(map[string]int{}), t: k(idl.Object)},
		{name: "object bad", goType: reflect.TypeOf(1), t: k(idl.Object), wantErr: "IDL object, Go int"},
		{name: "any bad", goType: reflect.TypeOf(""), t: k(idl.Any), wantErr: "IDL any, Go string"},
		{name: "array ok", goType: reflect.TypeOf([]int{}), t: arr},
		{name: "array fixed ok", goType: reflect.TypeOf([2]int{}), t: arr},
		{name: "array not slice", goType: reflect.TypeOf(1), t: arr, wantErr: "IDL array, Go int"},
		{name: "array elem bad", goType: reflect.TypeOf([]string{}), t: arr, wantErr: "[]: IDL int, Go string"},
		{name: "map ok", goType: reflect.TypeOf(map[string]int{}), t: mp},
		{name: "map not map", goType: reflect.TypeOf([]int{}), t: mp, wantErr: "IDL [string] map"},
		{name: "map bad key", goType: reflect.TypeOf(map[int]int{}), t: mp, wantErr: "IDL [string] map"},
		{name: "map elem bad", goType: reflect.TypeOf(map[string]string{}), t: mp, wantErr: "[string]: IDL int, Go string"},
		{name: "nullable pointer", goType: reflect.TypeOf((*string)(nil)), t: nullable},
		{name: "nullable bad", goType: reflect.TypeOf((*int)(nil)), t: nullable, wantErr: "IDL string, Go int"},
		{name: "named enum", goType: reflect.TypeOf(""), t: named},
		{name: "named enum bad", goType: reflect.TypeOf(1), t: named, wantErr: "IDL enum, Go int"},
		{name: "unknown named trusted", goType: reflect.TypeOf(1), t: unknown},
		{name: "time trusted for string", goType: reflect.TypeOf(time.Time{}), t: k(idl.String)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := typeChecker{iface: iface, seen: map[[2]any]bool{}}.check(tc.goType, tc.t, "")
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestJoinAndNonNull(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ path, name, want string }{
		{"", "a", "a"},
		{"a", "b", "a.b"},
	} {
		if got := join(tc.path, tc.name); got != tc.want {
			t.Errorf("join(%q, %q) = %q, want %q", tc.path, tc.name, got, tc.want)
		}
	}
	inner := &idl.Type{Kind: idl.Int}
	if got := nonNull(&idl.Type{Kind: idl.Nullable, Elem: inner}); got != inner {
		t.Error("nonNull did not unwrap nullable")
	}
	if got := nonNull(inner); got != inner {
		t.Error("nonNull changed a non-nullable type")
	}
}
