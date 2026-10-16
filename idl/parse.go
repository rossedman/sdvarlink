package idl

import (
	"fmt"
	"strings"
	"unicode"
)

// SyntaxError reports where parsing failed.
type SyntaxError struct {
	Line, Col int
	Msg       string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("idl:%d:%d: %s", e.Line, e.Col, e.Msg)
}

const (
	requiresMore = "[Requires 'more' flag]"
	supportsMore = "[Supports 'more' flag]"
)

// Parse parses the IDL text of one interface.
func Parse(src string) (*Interface, error) {
	p := &parser{src: src, line: 1, col: 1}
	iface, err := p.parse()
	if err != nil {
		return nil, err
	}
	return iface, nil
}

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tPunct // one of ( ) , : ? [ ] ->
)

type token struct {
	kind      tokKind
	text      string
	line, col int
	comments  []string // comment lines directly before this token
}

type parser struct {
	src       string
	pos       int
	line, col int
	tok       token
	peeked    bool
}

func (p *parser) errorf(t token, format string, args ...any) error {
	return &SyntaxError{Line: t.line, Col: t.col, Msg: fmt.Sprintf(format, args...)}
}

func (p *parser) advance(n int) {
	for i := 0; i < n; i++ {
		if p.src[p.pos] == '\n' {
			p.line++
			p.col = 1
		} else {
			p.col++
		}
		p.pos++
	}
}

func (p *parser) lex() token {
	var comments []string
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch c {
		case '\n':
			// A blank line separates a comment from what follows.
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == '\n' {
				comments = nil
			}
			p.advance(1)
		case ' ', '\t', '\r':
			p.advance(1)
		case '#':
			end := strings.IndexByte(p.src[p.pos:], '\n')
			if end < 0 {
				end = len(p.src) - p.pos
			}
			text := strings.TrimPrefix(p.src[p.pos+1:p.pos+end], " ")
			comments = append(comments, strings.TrimRight(text, " \t\r"))
			p.advance(end)
		default:
			t := token{line: p.line, col: p.col, comments: comments}
			switch {
			case c == '-' && strings.HasPrefix(p.src[p.pos:], "->"):
				t.kind, t.text = tPunct, "->"
				p.advance(2)
			case strings.IndexByte("(),:?[]", c) >= 0:
				t.kind, t.text = tPunct, string(c)
				p.advance(1)
			case isIdent(rune(c)):
				start := p.pos
				for p.pos < len(p.src) && (isIdent(rune(p.src[p.pos])) || p.src[p.pos] == '.') {
					p.advance(1)
				}
				t.kind, t.text = tIdent, p.src[start:p.pos]
			default:
				t.kind, t.text = tPunct, string(c)
				p.advance(1)
			}
			return t
		}
	}
	return token{kind: tEOF, line: p.line, col: p.col, comments: comments}
}

func isIdent(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func (p *parser) next() token {
	if p.peeked {
		p.peeked = false
		return p.tok
	}
	p.tok = p.lex()
	return p.tok
}

func (p *parser) peek() token {
	if !p.peeked {
		p.tok = p.lex()
		p.peeked = true
	}
	return p.tok
}

func (p *parser) expect(text string) (token, error) {
	t := p.next()
	if t.text != text {
		return t, p.errorf(t, "expected %q, found %q", text, t.text)
	}
	return t, nil
}

func (p *parser) ident() (token, error) {
	t := p.next()
	if t.kind != tIdent {
		return t, p.errorf(t, "expected identifier, found %q", t.text)
	}
	return t, nil
}

func doc(lines []string) string { return strings.Join(lines, "\n") }

func (p *parser) parse() (*Interface, error) {
	t, err := p.expect("interface")
	if err != nil {
		return nil, err
	}
	name, err := p.ident()
	if err != nil {
		return nil, err
	}
	iface := &Interface{Name: name.text, Doc: doc(t.comments)}
	for {
		kw := p.next()
		switch kw.text {
		case "":
			if kw.kind == tEOF {
				return iface, nil
			}
			return nil, p.errorf(kw, "unexpected %q", kw.text)
		case "type":
			n, err := p.ident()
			if err != nil {
				return nil, err
			}
			typ, err := p.parseParens()
			if err != nil {
				return nil, err
			}
			iface.Types = append(iface.Types, &TypeDef{Name: n.text, Doc: doc(kw.comments), Type: typ})
		case "method":
			n, err := p.ident()
			if err != nil {
				return nil, err
			}
			m := &Method{Name: n.text}
			var lines []string
			for _, c := range kw.comments {
				switch c {
				case requiresMore:
					m.More = MoreRequired
				case supportsMore:
					m.More = MoreSupported
				default:
					lines = append(lines, c)
				}
			}
			m.Doc = doc(lines)
			if m.In, err = p.parseStruct(); err != nil {
				return nil, err
			}
			if _, err := p.expect("->"); err != nil {
				return nil, err
			}
			if m.Out, err = p.parseStruct(); err != nil {
				return nil, err
			}
			iface.Methods = append(iface.Methods, m)
		case "error":
			n, err := p.ident()
			if err != nil {
				return nil, err
			}
			params, err := p.parseStruct()
			if err != nil {
				return nil, err
			}
			iface.Errors = append(iface.Errors, &Error{Name: n.text, Doc: doc(kw.comments), Params: params})
		default:
			if kw.kind == tEOF {
				return iface, nil
			}
			return nil, p.errorf(kw, "expected type, method or error, found %q", kw.text)
		}
	}
}

func (p *parser) parseStruct() (*Type, error) {
	t := p.peek()
	typ, err := p.parseParens()
	if err != nil {
		return nil, err
	}
	if typ.Kind != Struct {
		return nil, p.errorf(t, "expected a struct, found an enum")
	}
	return typ, nil
}

// parseParens parses "( ... )", deciding between struct and enum by whether
// the first member has a ':'. An empty list is a struct.
func (p *parser) parseParens() (*Type, error) {
	if _, err := p.expect("("); err != nil {
		return nil, err
	}
	typ := &Type{Kind: Struct}
	first := true
	for {
		t := p.next()
		if t.text == ")" {
			return typ, nil
		}
		if !first {
			if t.text != "," {
				return nil, p.errorf(t, "expected ',' or ')', found %q", t.text)
			}
			t = p.next()
		}
		if t.kind != tIdent {
			return nil, p.errorf(t, "expected member name, found %q", t.text)
		}
		if p.peek().text == ":" {
			if !first && typ.Kind == Enum {
				return nil, p.errorf(t, "mixed enum values and fields")
			}
			p.next()
			ft, err := p.parseType()
			if err != nil {
				return nil, err
			}
			typ.Fields = append(typ.Fields, &Field{Name: t.text, Doc: doc(t.comments), Type: ft})
		} else {
			if !first && typ.Kind == Struct {
				return nil, p.errorf(t, "mixed enum values and fields")
			}
			typ.Kind = Enum
			typ.Values = append(typ.Values, &EnumValue{Name: t.text, Doc: doc(t.comments)})
		}
		first = false
	}
}

func (p *parser) parseType() (*Type, error) {
	t := p.peek()
	switch t.text {
	case "?":
		p.next()
		elem, err := p.parseType()
		if err != nil {
			return nil, err
		}
		return &Type{Kind: Nullable, Elem: elem}, nil
	case "[":
		p.next()
		if p.peek().text == "]" {
			p.next()
			elem, err := p.parseType()
			if err != nil {
				return nil, err
			}
			return &Type{Kind: Array, Elem: elem}, nil
		}
		if _, err := p.expect("string"); err != nil {
			return nil, err
		}
		if _, err := p.expect("]"); err != nil {
			return nil, err
		}
		elem, err := p.parseType()
		if err != nil {
			return nil, err
		}
		return &Type{Kind: Map, Elem: elem}, nil
	case "(":
		return p.parseParens()
	}
	p.next()
	if t.kind != tIdent {
		return nil, p.errorf(t, "expected type, found %q", t.text)
	}
	switch t.text {
	case "bool":
		return &Type{Kind: Bool}, nil
	case "int":
		return &Type{Kind: Int}, nil
	case "float":
		return &Type{Kind: Float}, nil
	case "string":
		return &Type{Kind: String}, nil
	case "object":
		return &Type{Kind: Object}, nil
	case "any":
		return &Type{Kind: Any}, nil
	}
	return &Type{Kind: Named, Name: t.text}, nil
}

// ParseType parses a type expression such as "?[]string" or "(a: int)".
func ParseType(src string) (*Type, error) {
	iface, err := Parse("interface x.y\ntype T(f: " + src + ")\n")
	if err != nil {
		return nil, fmt.Errorf("idl: bad type %q: %w", src, err)
	}
	return iface.Types[0].Type.Fields[0].Type, nil
}
