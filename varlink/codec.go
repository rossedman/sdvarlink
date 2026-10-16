package varlink

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/rossedman/sdvarlink/internal/wire"
)

// File descriptor fields
//
// Varlink carries a file descriptor out of band (SCM_RIGHTS) and puts its
// index into the message's fd array in the JSON. In Go, such a field is an
// *os.File tagged like this:
//
//	Image *os.File `json:"-" varlink:"imageFileDescriptor,fd"`
//
// Marshal collects tagged files in field order (depth first), assigns them
// indexes 0..n-1 and writes the indexes into the JSON. Unmarshal does the
// reverse. Types without fd fields take the plain encoding/json path.

var fileType = reflect.TypeOf((*os.File)(nil))

// Marshal encodes v as Varlink parameters and returns the files that must be
// sent with the message. v may be nil, which encodes as {}.
func Marshal(v any) ([]byte, []*os.File, error) {
	if v == nil {
		return []byte("{}"), nil, nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		if len(raw) == 0 {
			return []byte("{}"), nil, nil
		}
		return raw, nil, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, nil, err
	}
	rv := reflect.ValueOf(v)
	if !hasFiles(rv.Type()) {
		return data, nil, nil
	}
	tree, err := decodeTree(data)
	if err != nil {
		return nil, nil, err
	}
	var files []*os.File
	tree = injectFiles(rv, tree, &files)
	data, err = json.Marshal(tree)
	if err != nil {
		return nil, nil, err
	}
	return data, files, nil
}

// Unmarshal decodes Varlink parameters into v, which must be a non-nil
// pointer, and attaches files to the fd fields that reference them. Files
// that no field claims are closed, so a schema mismatch cannot leak
// descriptors. Claimed files belong to the caller.
func Unmarshal(data []byte, files []*os.File, v any) error {
	claimed := make([]bool, len(files))
	defer func() {
		for i, f := range files {
			if !claimed[i] && f != nil {
				f.Close()
			}
		}
	}()
	if v == nil {
		return nil
	}
	if raw, ok := v.(*json.RawMessage); ok {
		*raw = append((*raw)[:0], data...)
		return nil
	}
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		data = []byte("{}")
	}
	if err := json.Unmarshal(data, v); err != nil {
		return err
	}
	rv := reflect.ValueOf(v)
	if !hasFiles(rv.Type()) {
		return nil
	}
	tree, err := decodeTree(data)
	if err != nil {
		return err
	}
	var set []reflect.Value
	if err := extractFiles(rv, tree, files, claimed, &set); err != nil {
		// Do not leave files in a value the caller will discard. Clear the
		// fields, close duplicates made for shared indexes, and let the
		// deferred cleanup close every received file.
		received := make(map[*os.File]bool, len(files))
		for _, f := range files {
			received[f] = true
		}
		for _, fv := range set {
			if f, _ := fv.Interface().(*os.File); f != nil && !received[f] {
				f.Close()
			}
			fv.Set(reflect.Zero(fileType))
		}
		clear(claimed)
		return err
	}
	return nil
}

func decodeTree(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, err
	}
	return tree, nil
}

// fieldInfo describes how a struct field maps to JSON.
type fieldInfo struct {
	index  int
	name   string // JSON name; "" for embedded structs flattened into the parent
	fd     bool
	nested bool // type (transitively) contains fd fields
}

var structCache sync.Map // reflect.Type -> []fieldInfo

func structFields(t reflect.Type) []fieldInfo {
	if v, ok := structCache.Load(t); ok {
		return v.([]fieldInfo)
	}
	var out []fieldInfo
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() && !sf.Anonymous {
			continue
		}
		if tag, ok := sf.Tag.Lookup("varlink"); ok {
			name, opts, _ := strings.Cut(tag, ",")
			if opts == "fd" && sf.Type == fileType {
				out = append(out, fieldInfo{index: i, name: name, fd: true})
				continue
			}
		}
		if !hasFiles(sf.Type) {
			continue
		}
		name := sf.Name
		if tag := sf.Tag.Get("json"); tag != "" {
			n, _, _ := strings.Cut(tag, ",")
			if n == "-" {
				continue
			}
			if n != "" {
				name = n
			}
		} else if sf.Anonymous {
			name = ""
		}
		out = append(out, fieldInfo{index: i, name: name, nested: true})
	}
	structCache.Store(t, out)
	return out
}

var filesCache sync.Map // reflect.Type -> bool

// hasFiles reports whether values of type t can carry fd fields.
func hasFiles(t reflect.Type) bool {
	if v, ok := filesCache.Load(t); ok {
		return v.(bool)
	}
	// Only the final answer is cached: a placeholder would be visible to
	// concurrent callers, and answers computed inside a recursive cycle can
	// be incomplete.
	res := computeHasFiles(t, map[reflect.Type]bool{})
	filesCache.Store(t, res)
	return res
}

func computeHasFiles(t reflect.Type, visiting map[reflect.Type]bool) bool {
	if v, ok := filesCache.Load(t); ok {
		return v.(bool)
	}
	if visiting[t] {
		return false // a cycle adds nothing the outer call will not see
	}
	visiting[t] = true
	defer delete(visiting, t)
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return computeHasFiles(t.Elem(), visiting)
	case reflect.Map:
		return t.Key().Kind() == reflect.String && computeHasFiles(t.Elem(), visiting)
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			if tag, ok := sf.Tag.Lookup("varlink"); ok && strings.HasSuffix(tag, ",fd") && sf.Type == fileType {
				return true
			}
			if (sf.IsExported() || sf.Anonymous) && sf.Tag.Get("json") != "-" && computeHasFiles(sf.Type, visiting) {
				return true
			}
		}
	}
	return false
}

func injectFiles(v reflect.Value, node any, files *[]*os.File) any {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return node
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		m, ok := node.(map[string]any)
		if !ok {
			return node
		}
		for _, fi := range structFields(v.Type()) {
			fv := v.Field(fi.index)
			switch {
			case fi.fd:
				if f, _ := fv.Interface().(*os.File); f != nil {
					m[fi.name] = len(*files)
					*files = append(*files, f)
				}
			case fi.name == "":
				injectFiles(fv, m, files)
			default:
				if child, ok := m[fi.name]; ok {
					m[fi.name] = injectFiles(fv, child, files)
				}
			}
		}
		return m
	case reflect.Slice, reflect.Array:
		arr, ok := node.([]any)
		if !ok {
			return node
		}
		for i := 0; i < v.Len() && i < len(arr); i++ {
			arr[i] = injectFiles(v.Index(i), arr[i], files)
		}
		return arr
	case reflect.Map:
		m, ok := node.(map[string]any)
		if !ok {
			return node
		}
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		for _, k := range keys {
			if child, ok := m[k.String()]; ok {
				m[k.String()] = injectFiles(v.MapIndex(k), child, files)
			}
		}
		return m
	}
	return node
}

func extractFiles(v reflect.Value, node any, files []*os.File, claimed []bool, set *[]reflect.Value) error {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		m, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		for _, fi := range structFields(v.Type()) {
			fv := v.Field(fi.index)
			switch {
			case fi.fd:
				raw, ok := m[fi.name]
				if !ok || raw == nil {
					continue
				}
				num, ok := raw.(json.Number)
				if !ok {
					return fmt.Errorf("varlink: fd field %q is not an integer", fi.name)
				}
				idx, err := num.Int64()
				if err != nil || idx < 0 || idx >= int64(len(files)) {
					return fmt.Errorf("varlink: fd field %q references index %s, but %d files were received", fi.name, num, len(files))
				}
				if claimed[idx] {
					// Two fields share one descriptor: give each its own.
					dup, err := wire.DupFile(files[idx])
					if err != nil {
						return err
					}
					fv.Set(reflect.ValueOf(dup))
					*set = append(*set, fv)
					continue
				}
				claimed[idx] = true
				fv.Set(reflect.ValueOf(files[idx]))
				*set = append(*set, fv)
			case fi.name == "":
				if err := extractFiles(fv.Addr(), m, files, claimed, set); err != nil {
					return err
				}
			default:
				if err := extractFiles(fv.Addr(), m[fi.name], files, claimed, set); err != nil {
					return err
				}
			}
		}
	case reflect.Slice, reflect.Array:
		arr, ok := node.([]any)
		if !ok {
			return nil
		}
		for i := 0; i < v.Len() && i < len(arr); i++ {
			if err := extractFiles(v.Index(i).Addr(), arr[i], files, claimed, set); err != nil {
				return err
			}
		}
	case reflect.Map:
		m, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		for _, k := range v.MapKeys() {
			// Map values are not addressable: decode into a copy and store it back.
			elem := reflect.New(v.Type().Elem())
			elem.Elem().Set(v.MapIndex(k))
			if err := extractFiles(elem, m[k.String()], files, claimed, set); err != nil {
				return err
			}
			v.SetMapIndex(k, elem.Elem())
		}
	}
	return nil
}

// UnknownFields returns the members of the JSON object data whose names are
// not in known, or nil if there are none. Generated types use it to keep
// fields added by newer systemd releases in their Extra map instead of
// dropping them.
func UnknownFields(data []byte, known ...string) (map[string]json.RawMessage, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	for _, k := range known {
		delete(all, k)
	}
	if len(all) == 0 {
		return nil, nil
	}
	return all, nil
}

// MarshalWithExtra encodes v and adds the members of extra that v does not
// already set. Generated types use it so that fields kept in Extra survive
// a round trip, for example when a test fake echoes a record.
func MarshalWithExtra(v any, extra map[string]json.RawMessage) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil || len(extra) == 0 {
		return data, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	for k, val := range extra {
		if _, ok := obj[k]; !ok {
			obj[k] = val
		}
	}
	return json.Marshal(obj)
}
