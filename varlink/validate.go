package varlink

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/rossedman/sdvarlink/idl"
)

// validateParams checks call parameters against a method's input the way
// sd-varlink does: required fields present, values of the declared type,
// enum values known, and (unless allowUnknown) no fields the IDL does not
// define. It returns the name of the offending top-level field, or "".
func validateParams(iface *idl.Interface, in *idl.Type, params []byte, allowUnknown bool) (string, error) {
	if len(bytes.TrimSpace(params)) == 0 || bytes.Equal(bytes.TrimSpace(params), []byte("null")) {
		params = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "parameters", err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return "parameters", fmt.Errorf("parameters are not an object")
	}
	vc := validator{iface: iface, allowUnknown: allowUnknown}
	for _, f := range in.Fields {
		if err := vc.field(f, obj[f.Name], hasKey(obj, f.Name)); err != nil {
			return f.Name, err
		}
	}
	if !allowUnknown {
		for k := range obj {
			if !hasField(in, k) {
				return k, fmt.Errorf("field %q not defined", k)
			}
		}
	}
	return "", nil
}

// hasKey reports whether m has key k.
func hasKey(m map[string]any, k string) bool { _, ok := m[k]; return ok }

// hasField reports whether the struct type t defines a field called name.
func hasField(t *idl.Type, name string) bool {
	for _, f := range t.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

type validator struct {
	iface        *idl.Interface
	allowUnknown bool
	depth        int
}

// field checks one struct field: a missing or null value is allowed only for
// nullable types.
func (vc validator) field(f *idl.Field, v any, present bool) error {
	if !present || v == nil {
		if f.Type.Kind == idl.Nullable {
			return nil
		}
		return fmt.Errorf("missing required field %q", f.Name)
	}
	return vc.value(f.Type, v)
}

// value checks v, a decoded JSON value, against the IDL type t.
func (vc validator) value(t *idl.Type, v any) error {
	vc.depth++
	if vc.depth > 64 {
		return fmt.Errorf("nesting too deep")
	}
	switch t.Kind {
	case idl.Nullable:
		if v == nil {
			return nil
		}
		return vc.value(t.Elem, v)
	case idl.Bool:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("expected bool")
		}
	case idl.Int:
		n, ok := v.(json.Number)
		if !ok {
			return fmt.Errorf("expected int")
		}
		if _, err := n.Int64(); err != nil {
			// Unsigned values up to 2^64-1 are allowed, as in sd-varlink;
			// fractions, exponents and anything larger are not ints.
			if _, uerr := strconv.ParseUint(n.String(), 10, 64); uerr != nil {
				return fmt.Errorf("expected int, got %s", n)
			}
		}
	case idl.Float:
		if _, ok := v.(json.Number); !ok {
			return fmt.Errorf("expected float")
		}
	case idl.String:
		if _, ok := v.(string); !ok {
			return fmt.Errorf("expected string")
		}
	case idl.Object:
		if _, ok := v.(map[string]any); !ok {
			return fmt.Errorf("expected object")
		}
	case idl.Any:
	case idl.Array:
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("expected array")
		}
		for _, e := range arr {
			if err := vc.value(t.Elem, e); err != nil {
				return err
			}
		}
	case idl.Map:
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("expected object (map)")
		}
		for _, e := range m {
			if err := vc.value(t.Elem, e); err != nil {
				return err
			}
		}
	case idl.Named:
		td := vc.iface.Type(t.Name)
		if td == nil {
			return nil // unknown name: cannot check further
		}
		return vc.value(td.Type, v)
	case idl.Enum:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("expected enum string")
		}
		for _, e := range t.Values {
			if e.Name == s {
				return nil
			}
		}
		return fmt.Errorf("unknown enum value %q", s)
	case idl.Struct:
		obj, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("expected object")
		}
		for _, f := range t.Fields {
			if err := vc.field(f, obj[f.Name], hasKey(obj, f.Name)); err != nil {
				return err
			}
		}
		if !vc.allowUnknown {
			for k := range obj {
				if !hasField(t, k) {
					return fmt.Errorf("field %q not defined", k)
				}
			}
		}
	}
	return nil
}

// Registration-time type checks: a handler's Go types must agree with the
// method's IDL, so mismatches fail at startup instead of reaching clients.

var (
	jsonNumberType  = reflect.TypeOf(json.Number(""))
	rawMessageType  = reflect.TypeOf(json.RawMessage(nil))
	marshalerType   = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	unmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
)

type typeChecker struct {
	iface *idl.Interface
	// output is true when checking a reply type: then every required IDL
	// field must exist in Go, or replies would be invalid.
	output bool
	seen   map[[2]any]bool
}

// checkStruct compares a Go type with a method's input or output struct.
func checkStruct(iface *idl.Interface, goType reflect.Type, t *idl.Type, output bool) error {
	tc := typeChecker{iface: iface, output: output, seen: map[[2]any]bool{}}
	return tc.check(goType, t, "")
}

// check compares Go type g with IDL type t and reports the first mismatch,
// naming it by its path.
func (tc typeChecker) check(g reflect.Type, t *idl.Type, path string) error {
	where := func(format string, args ...any) error {
		p := path
		if p == "" {
			p = "(top level)"
		}
		return fmt.Errorf("%s: %s", p, fmt.Sprintf(format, args...))
	}
	key := [2]any{g, t}
	if tc.seen[key] {
		return nil
	}
	tc.seen[key] = true

	if t.Kind == idl.Nullable {
		t = t.Elem
	}
	for g.Kind() == reflect.Pointer {
		g = g.Elem()
	}
	if t.Kind == idl.Named {
		td := tc.iface.Type(t.Name)
		if td == nil {
			return nil
		}
		t = td.Type
	}
	if g == rawMessageType || g.Kind() == reflect.Interface {
		return nil // raw JSON accepts anything
	}
	if g == jsonNumberType && (t.Kind == idl.Int || t.Kind == idl.Float) {
		return nil
	}
	// A type with its own JSON encoding (time.Time, *big.Int, …) is trusted
	// for anything but an IDL struct, whose fields are checked below.
	if t.Kind != idl.Struct && (g.Implements(marshalerType) || reflect.PointerTo(g).Implements(unmarshalerType)) {
		return nil
	}
	switch t.Kind {
	case idl.Bool:
		if g.Kind() != reflect.Bool {
			return where("IDL bool, Go %s", g)
		}
	case idl.Int:
		switch g.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		default:
			return where("IDL int, Go %s", g)
		}
	case idl.Float:
		switch g.Kind() {
		case reflect.Float32, reflect.Float64:
		default:
			return where("IDL float, Go %s", g)
		}
	case idl.String, idl.Enum:
		if g.Kind() != reflect.String {
			return where("IDL %s, Go %s", t.Kind, g)
		}
	case idl.Object, idl.Any:
		if g.Kind() != reflect.Map && g.Kind() != reflect.Struct {
			return where("IDL %s, Go %s", t.Kind, g)
		}
	case idl.Array:
		if g.Kind() != reflect.Slice && g.Kind() != reflect.Array {
			return where("IDL array, Go %s", g)
		}
		return tc.check(g.Elem(), t.Elem, path+"[]")
	case idl.Map:
		if g.Kind() != reflect.Map || g.Key().Kind() != reflect.String {
			return where("IDL [string] map, Go %s", g)
		}
		return tc.check(g.Elem(), t.Elem, path+"[string]")
	case idl.Struct:
		if g.Kind() == reflect.Map && g.Key().Kind() == reflect.String {
			return nil // a map can hold any fields; nothing to check
		}
		if g.Kind() != reflect.Struct {
			return where("IDL struct, Go %s", g)
		}
		goFields := map[string]fieldMeta{}
		collectFields(g, goFields)
		for name, meta := range goFields {
			ft := meta.typ
			var def *idl.Field
			for _, f := range t.Fields {
				if f.Name == name {
					def = f
				}
			}
			if def == nil {
				return where("Go field %q is not in the IDL", name)
			}
			if ft == fileType {
				if base := nonNull(def.Type); base.Kind != idl.Int {
					return where("fd field %q must be an int in the IDL", name)
				}
				continue
			}
			if tc.output && def.Type.Kind != idl.Nullable && (meta.omitempty || (ft.Kind() == reflect.Pointer && !customJSON(ft))) {
				// A zero value would be omitted, or a nil pointer sent as
				// null, and clients reject a required field that is missing.
				return where("required IDL field %q must not be a pointer or omitempty in a reply type", name)
			}
			if err := tc.check(ft, def.Type, join(path, name)); err != nil {
				return err
			}
		}
		if tc.output {
			for _, f := range t.Fields {
				if _, ok := goFields[f.Name]; !ok && f.Type.Kind != idl.Nullable {
					return where("required IDL field %q is missing from Go type %s", f.Name, g)
				}
			}
		}
	}
	return nil
}

// customJSON reports whether t encodes itself (time.Time, *big.Int, …);
// such pointers are the type's natural form, so they are trusted.
func customJSON(t reflect.Type) bool {
	return t.Implements(marshalerType) || reflect.PointerTo(t).Implements(unmarshalerType)
}

// nonNull returns the element of a nullable type, or t itself.
func nonNull(t *idl.Type) *idl.Type {
	if t.Kind == idl.Nullable {
		return t.Elem
	}
	return t
}

// join appends name to a dotted field path.
func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

type fieldMeta struct {
	typ       reflect.Type
	omitempty bool
}

// collectFields maps JSON field names to Go types, flattening embedded
// structs. fd fields map to *os.File.
func collectFields(g reflect.Type, out map[string]fieldMeta) {
	for i := 0; i < g.NumField(); i++ {
		sf := g.Field(i)
		if tag, ok := sf.Tag.Lookup("varlink"); ok {
			if name, opts, _ := strings.Cut(tag, ","); opts == "fd" && sf.Type == fileType {
				out[name] = fieldMeta{typ: fileType}
				continue
			}
		}
		jt := sf.Tag.Get("json")
		name, opts, _ := strings.Cut(jt, ",")
		if name == "-" {
			continue
		}
		if sf.Anonymous && name == "" {
			et := sf.Type
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				collectFields(et, out)
				continue
			}
		}
		if !sf.IsExported() {
			continue
		}
		if name == "" {
			name = sf.Name
		}
		out[name] = fieldMeta{typ: sf.Type, omitempty: strings.Contains(","+opts+",", ",omitempty,")}
	}
}
