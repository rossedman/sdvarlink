// Package idl parses the Varlink interface definition language.
//
// It understands the IDL that systemd emits for `varlinkctl introspect` and
// org.varlink.service.GetInterfaceDescription, including doc comments and
// systemd's "# [Requires 'more' flag]" / "# [Supports 'more' flag]"
// annotations on streaming methods.
//
//	iface, err := idl.Parse(text)
//	if err != nil {
//		return err
//	}
//	for _, m := range iface.Methods {
//		fmt.Println(iface.Name+"."+m.Name, m.More)
//	}
package idl

import (
	"fmt"
	"strings"
)

// Kind identifies the shape of a Type.
type Kind int

const (
	Bool Kind = iota
	Int
	Float
	String
	Object // a JSON object with unspecified content
	Any    // any JSON value
	Named  // a reference to a type defined in the interface
	Struct
	Enum
	Array    // []Elem
	Map      // [string]Elem
	Nullable // ?Elem
)

var kindNames = [...]string{"bool", "int", "float", "string", "object", "any", "named", "struct", "enum", "array", "map", "nullable"}

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Type is a Varlink type expression.
type Type struct {
	Kind   Kind
	Elem   *Type        // Array, Map, Nullable
	Name   string       // Named
	Fields []*Field     // Struct
	Values []*EnumValue // Enum
}

// Field is one member of a struct type or a method's parameters.
type Field struct {
	Name string
	Doc  string
	Type *Type
}

// EnumValue is one value of an enum type.
type EnumValue struct {
	Name string
	Doc  string
}

// TypeDef is a named type: `type Name (...)`.
type TypeDef struct {
	Name string
	Doc  string
	Type *Type // Kind is Struct or Enum
}

// More describes whether a method streams replies.
type More int

const (
	// MoreNone: the method returns exactly one reply.
	MoreNone More = iota
	// MoreSupported: the method returns several replies if the call sets "more".
	MoreSupported
	// MoreRequired: the method must be called with "more".
	MoreRequired
)

func (m More) String() string {
	switch m {
	case MoreSupported:
		return "supports-more"
	case MoreRequired:
		return "requires-more"
	}
	return "single"
}

// Method is `method Name (In) -> (Out)`.
type Method struct {
	Name string
	Doc  string
	In   *Type // Kind Struct
	Out  *Type // Kind Struct
	More More
}

// Error is `error Name (Params)`.
type Error struct {
	Name   string
	Doc    string
	Params *Type // Kind Struct
}

// Interface is one parsed interface.
type Interface struct {
	Name    string
	Doc     string
	Types   []*TypeDef
	Methods []*Method
	Errors  []*Error
}

// Type returns the named type definition, or nil.
func (i *Interface) Type(name string) *TypeDef {
	for _, t := range i.Types {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// Method returns the named method, or nil.
func (i *Interface) Method(name string) *Method {
	for _, m := range i.Methods {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// Error returns the named error, or nil.
func (i *Interface) Error(name string) *Error {
	for _, e := range i.Errors {
		if e.Name == name {
			return e
		}
	}
	return nil
}

// String formats t in IDL syntax on one line.
func (t *Type) String() string {
	var b strings.Builder
	t.write(&b)
	return b.String()
}

func (t *Type) write(b *strings.Builder) {
	switch t.Kind {
	case Nullable:
		b.WriteByte('?')
		t.Elem.write(b)
	case Array:
		b.WriteString("[]")
		t.Elem.write(b)
	case Map:
		b.WriteString("[string]")
		t.Elem.write(b)
	case Named:
		b.WriteString(t.Name)
	case Struct:
		b.WriteByte('(')
		for i, f := range t.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(f.Name)
			b.WriteString(": ")
			f.Type.write(b)
		}
		b.WriteByte(')')
	case Enum:
		b.WriteByte('(')
		for i, v := range t.Values {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(v.Name)
		}
		b.WriteByte(')')
	default:
		b.WriteString(t.Kind.String())
	}
}
