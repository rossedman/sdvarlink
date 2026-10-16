// Package gen turns parsed Varlink interfaces into Go packages. It backs
// the sdvarlink-gen command.
package gen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/rossedman/sdvarlink/idl"
)

// Config is read from idl/generate.json.
type Config struct {
	// Module is the Go module path of the generated packages.
	Module string `json:"module"`
	// Sockets maps interface names to systemd's default socket path.
	Sockets map[string]string `json:"sockets"`
	// Unstable lists interfaces generated under x/ with no compatibility
	// promise.
	Unstable []string `json:"unstable"`
	// FDFieldSuffix marks int fields that carry file descriptor indexes,
	// following systemd's naming convention ("imageFileDescriptor").
	FDFieldSuffix string `json:"fdFieldSuffix"`
	// FieldTypes corrects known mistakes in a release's upstream IDL. It
	// maps a systemd version to field paths and their corrected types.
	// A path is "Interface.Method.field" (input or output) or
	// "Interface.Type.field".
	FieldTypes map[string]map[string]string `json:"fieldTypes"`
}

// ApplyFieldTypes rewrites field types in iface according to the
// FieldTypes entries for version. It reports paths that matched nothing.
func (c *Config) ApplyFieldTypes(iface *idl.Interface, version string) error {
	for path, typ := range c.FieldTypes[version] {
		if !strings.HasPrefix(path, iface.Name+".") {
			continue
		}
		// "io.systemd" is a prefix of every interface: the rest must be
		// exactly Owner.field.
		rest := strings.TrimPrefix(path, iface.Name+".")
		if strings.Count(rest, ".") != 1 {
			continue
		}
		owner, field, _ := strings.Cut(rest, ".")
		t, err := idl.ParseType(typ)
		if err != nil {
			return fmt.Errorf("fieldTypes %q: %w", path, err)
		}
		var structs []*idl.Type
		if m := iface.Method(owner); m != nil {
			structs = append(structs, m.In, m.Out)
		}
		if td := iface.Type(owner); td != nil {
			structs = append(structs, td.Type)
		}
		found := false
		for _, st := range structs {
			for _, f := range st.Fields {
				if f.Name == field {
					f.Type = t
					found = true
				}
			}
		}
		if !found {
			return fmt.Errorf("fieldTypes: %s matches no field in systemd v%s", path, version)
		}
	}
	return nil
}

// LoadConfig reads a Config from path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.FDFieldSuffix == "" {
		c.FDFieldSuffix = "FileDescriptor"
	}
	return &c, nil
}

// IsUnstable reports whether iface is generated under x/.
func (c *Config) IsUnstable(iface string) bool {
	for _, u := range c.Unstable {
		if u == iface {
			return true
		}
	}
	return false
}

// File is one generated output file.
type File struct {
	Name    string
	Content []byte
}

// Package generates the Go package for one interface. idlFile is the
// basename of the IDL file, embedded into the package. version is the
// systemd release, e.g. "260".
func Package(cfg *Config, iface *idl.Interface, idlFile, version string) ([]File, error) {
	g := &pkgGen{
		cfg:     cfg,
		iface:   iface,
		pkg:     PackageName(iface.Name),
		version: version,
		taken:   map[string]bool{},
	}
	src, err := g.generate(idlFile)
	if err != nil {
		return nil, err
	}
	formatted, err := format.Source(src)
	if err != nil {
		return nil, fmt.Errorf("%s: gofmt: %w\n%s", iface.Name, err, numbered(src))
	}
	return []File{{Name: g.pkg + ".go", Content: formatted}}, nil
}

func numbered(src []byte) string {
	var b strings.Builder
	for i, l := range strings.Split(string(src), "\n") {
		fmt.Fprintf(&b, "%4d %s\n", i+1, l)
	}
	return b.String()
}

type pkgGen struct {
	cfg     *Config
	iface   *idl.Interface
	pkg     string
	version string
	taken   map[string]bool // Go identifiers already declared

	typeNames map[string]string // IDL type name -> Go name
	decls     bytes.Buffer      // types, emitted in order of discovery
	usesOS    bool
	usesJSON  bool
}

// reserved are identifiers the generated package declares itself.
var reserved = []string{
	"Client", "New", "Dial", "DialPath", "DefaultSocket", "InterfaceName",
	"InterfaceDescription", "SystemdVersion", "convertError",
}

// clientMethods are methods of Client that systemd method names must not shadow.
var clientMethods = map[string]bool{"Close": true, "Caller": true, "Supports": true}

func (g *pkgGen) claim(name string) string {
	for g.taken[name] {
		name += "_"
	}
	g.taken[name] = true
	return name
}

func (g *pkgGen) p(format string, args ...any) { fmt.Fprintf(&g.decls, format, args...) }

func (g *pkgGen) generate(idlFile string) ([]byte, error) {
	for _, r := range reserved {
		g.taken[r] = true
	}
	// Reserve the Go names of all named types up front so inline types and
	// method structs cannot take them.
	typeNames := map[string]string{}
	for _, td := range g.iface.Types {
		typeNames[td.Name] = g.claim(GoName(td.Name))
	}
	g.typeNames = typeNames

	for _, td := range g.iface.Types {
		g.namedType(td)
	}
	type methodInfo struct {
		m       *idl.Method
		goName  string
		in, out string
		hasIn   bool
		hasOut  bool
		more    string // name of the streaming variant of a supports-more method
	}
	var methods []methodInfo
	methodNames := map[string]bool{}
	for n := range clientMethods {
		methodNames[n] = true
	}
	for _, m := range g.iface.Methods {
		name := GoName(m.Name)
		if clientMethods[name] {
			name += "Method"
		}
		for methodNames[name] {
			name += "_"
		}
		methodNames[name] = true
		mi := methodInfo{m: m, goName: name, hasIn: len(m.In.Fields) > 0, hasOut: len(m.Out.Fields) > 0}
		qual := g.iface.Name + "." + m.Name
		if mi.hasIn {
			mi.in = g.claim(name + "In")
			g.structType(mi.in, fmt.Sprintf("%s holds the parameters of %s.", mi.in, qual), "", m.In)
		}
		if mi.hasOut {
			mi.out = g.claim(name + "Out")
			g.structType(mi.out, fmt.Sprintf("%s holds the reply of %s.", mi.out, qual), "", m.Out)
		}
		methods = append(methods, mi)
	}
	// Streaming variants are named after every plain method has its name,
	// so a real method called FooMore keeps it.
	for i := range methods {
		if methods[i].m.More == idl.MoreSupported {
			more := methods[i].goName + "More"
			for methodNames[more] {
				more += "_"
			}
			methodNames[more] = true
			methods[i].more = more
		}
	}
	type errInfo struct {
		e             *idl.Error
		typ, sentinel string
	}
	var errs []errInfo
	for _, e := range g.iface.Errors {
		typ := GoName(e.Name)
		if !strings.HasSuffix(typ, "Error") {
			typ += "Error" // UnitError stays UnitError, not UnitErrorError
		}
		ei := errInfo{e: e, typ: g.claim(typ), sentinel: g.claim("Err" + GoName(e.Name))}
		g.errorType(ei.typ, ei.sentinel, e)
		errs = append(errs, ei)
	}

	var out, body bytes.Buffer
	w := func(format string, args ...any) { fmt.Fprintf(&out, format, args...) }
	bw := func(format string, args ...any) { fmt.Fprintf(&body, format, args...) }
	usesIter := false
	w("// Code generated by sdvarlink-gen from systemd v%s. DO NOT EDIT.\n\n", g.version)
	w("// Package %s is a typed client for the %s Varlink interface,\n", g.pkg, g.iface.Name)
	w("// generated from systemd v%s.\n", g.version)
	if g.iface.Doc != "" {
		w("//\n")
		writeDoc(&out, "", g.iface.Doc)
	}
	socket := g.cfg.Sockets[g.iface.Name]
	w("//\n")
	if socket != "" {
		w("// systemd serves this interface at %s. Connect with Dial, or with\n", socket)
		w("// DialPath for another socket such as a varlinktest fake.\n")
	} else {
		w("// systemd has no single well-known socket for this interface; connect\n")
		w("// with DialPath, or wrap an existing connection with New.\n")
	}
	w("package %s\n\n", g.pkg)

	importsAt := out.Len()

	w("const (\n")
	w("\t// InterfaceName is the Varlink interface this package implements.\n")
	w("\tInterfaceName = %q\n", g.iface.Name)
	w("\t// SystemdVersion is the systemd release the bindings were generated from.\n")
	w("\tSystemdVersion = %d\n", mustAtoi(g.version))
	if socket != "" {
		w("\t// DefaultSocket is where systemd serves %s.\n", g.iface.Name)
		w("\tDefaultSocket = %q\n", socket)
	}
	w(")\n\n")
	w("// InterfaceDescription is the IDL these bindings were generated from. Pass\n")
	w("// it to varlinktest.Server.AddInterface to make a fake answer introspection.\n")
	w("//\n//go:embed %s\nvar InterfaceDescription string\n\n", idlFile)

	bw("%s", clientCode(socket != ""))

	for _, mi := range methods {
		qual := g.iface.Name + "." + mi.m.Name
		inParam, inArg := "", "nil"
		if mi.hasIn {
			inParam, inArg = ", in "+mi.in, "in"
		}
		emit := func(name string, stream bool) {
			bw("// %s calls %s", name, qual)
			if stream {
				bw(" with the \"more\" flag and yields each reply")
			}
			bw(".\n")
			if mi.m.Doc != "" {
				bw("//\n")
				writeDoc(&body, "", mi.m.Doc)
			}
			if stream {
				usesIter = true
				bw("//\n// Breaking out of the loop early closes the underlying connection.\n")
				outType := mi.out
				if !mi.hasOut {
					outType = "struct{}"
				}
				bw("func (c *Client) %s(ctx context.Context%s) iter.Seq2[*%s, error] {\n", name, inParam, outType)
				bw("\treturn func(yield func(*%s, error) bool) {\n", outType)
				bw("\t\tfor r, err := range varlink.StreamAs[%s](c.c.Stream(ctx, %q, %s)) {\n", outType, qual, inArg)
				bw("\t\t\tif !yield(r, convertError(err)) || err != nil {\n\t\t\t\treturn\n\t\t\t}\n\t\t}\n\t}\n}\n\n")
				return
			}
			if mi.hasOut {
				bw("func (c *Client) %s(ctx context.Context%s) (*%s, error) {\n", name, inParam, mi.out)
				bw("\tout := new(%s)\n", mi.out)
				bw("\tif err := c.c.Call(ctx, %q, %s, out); err != nil {\n\t\treturn nil, convertError(err)\n\t}\n", qual, inArg)
				bw("\treturn out, nil\n}\n\n")
			} else {
				bw("func (c *Client) %s(ctx context.Context%s) error {\n", name, inParam)
				bw("\treturn convertError(c.c.Call(ctx, %q, %s, nil))\n}\n\n", qual, inArg)
			}
		}
		switch mi.m.More {
		case idl.MoreRequired:
			emit(mi.goName, true)
		case idl.MoreSupported:
			emit(mi.goName, false)
			emit(mi.more, true)
		default:
			emit(mi.goName, false)
		}
	}

	bw("// convertError turns error replies named by this interface into typed\n// errors.\n")
	bw("func convertError(err error) error {\n")
	if len(errs) == 0 {
		bw("\treturn err\n}\n\n")
	} else {
		bw("\tvar ve *varlink.Error\n\tif !errors.As(err, &ve) {\n\t\treturn err\n\t}\n")
		bw("\tvar typed varlink.NamedError\n\tswitch ve.Name {\n")
		for _, ei := range errs {
			bw("\tcase %q:\n\t\ttyped = new(%s)\n", g.iface.Name+"."+ei.e.Name, ei.typ)
		}
		bw("\tdefault:\n\t\treturn err\n\t}\n")
		bw("\tif len(ve.Parameters) > 0 {\n\t\tif jerr := json.Unmarshal(ve.Parameters, typed); jerr != nil {\n\t\t\treturn err\n\t\t}\n\t}\n")
		bw("\treturn typed\n}\n\n")
	}
	var imp bytes.Buffer
	imp.WriteString("import (\n\t\"context\"\n\t_ \"embed\"\n")
	if g.usesJSON || len(errs) > 0 {
		imp.WriteString("\t\"encoding/json\"\n")
	}
	if len(errs) > 0 {
		imp.WriteString("\t\"errors\"\n")
	}
	if usesIter {
		imp.WriteString("\t\"iter\"\n")
	}
	if g.usesOS {
		imp.WriteString("\t\"os\"\n")
	}
	fmt.Fprintf(&imp, "\t\"strings\"\n\n\t\"%s/varlink\"\n)\n\n", g.cfg.Module)
	head := append([]byte(nil), out.Bytes()[:importsAt]...)
	tail := append([]byte(nil), out.Bytes()[importsAt:]...)
	out.Reset()
	out.Write(head)
	out.Write(imp.Bytes())
	out.Write(tail)
	out.Write(body.Bytes())
	out.Write(g.decls.Bytes())
	return out.Bytes(), nil
}

func mustAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(fmt.Sprintf("bad version %q", s))
	}
	return n
}

func clientCode(hasSocket bool) string {
	var b strings.Builder
	b.WriteString(`// Client calls the interface's methods. It is safe for concurrent use.
type Client struct {
	c    varlink.Caller
	pool *varlink.Pool // owned pool, closed by Close
}

// New returns a client that sends calls through c, for example a
// *varlink.Conn, a *varlink.Pool or a test double.
func New(c varlink.Caller) *Client { return &Client{c: c} }

`)
	if hasSocket {
		b.WriteString(`// Dial connects to systemd at DefaultSocket. If the socket does not exist
// (older systemd, or the service is not running) or the platform is not
// Linux, the error matches varlink.ErrUnsupported.
func Dial(ctx context.Context, opts ...varlink.Option) (*Client, error) {
	return dial(ctx, DefaultSocket, varlink.DialService, opts)
}

`)
	}
	b.WriteString(`// DialPath connects to a service implementing the interface at path. The
// client keeps a pool of connections, so concurrent calls do not wait for
// each other.
func DialPath(ctx context.Context, path string, opts ...varlink.Option) (*Client, error) {
	return dial(ctx, path, varlink.Dial, opts)
}

func dial(ctx context.Context, path string, d func(context.Context, string, ...varlink.Option) (*varlink.Conn, error), opts []varlink.Option) (*Client, error) {
	// Dial once up front so a missing service is reported here.
	first, err := d(ctx, path, opts...)
	if err != nil {
		return nil, err
	}
	pool := varlink.NewPool(func(ctx context.Context) (*varlink.Conn, error) {
		return d(ctx, path, opts...)
	})
	pool.Put(first)
	return &Client{c: pool, pool: pool}, nil
}

// Close closes the client's connections if it opened them.
func (c *Client) Close() error {
	if c.pool != nil {
		return c.pool.Close()
	}
	return nil
}

// Caller returns the underlying Caller, for raw calls to methods that are
// not in these bindings.
func (c *Client) Caller() varlink.Caller { return c.c }

// Supports reports whether the connected service implements method, given
// as a bare name ("ResolveHostname") or fully qualified. Use it to
// feature-detect methods added after the oldest systemd you support.
func (c *Client) Supports(ctx context.Context, method string) (bool, error) {
	if !strings.Contains(method, ".") {
		method = InterfaceName + "." + method
	}
	return varlink.Supports(ctx, c.c, method)
}

`)
	return b.String()
}

// fdField reports whether f carries a file descriptor index.
func (g *pkgGen) fdField(f *idl.Field) bool {
	t := f.Type
	if t.Kind == idl.Nullable {
		t = t.Elem
	}
	return t.Kind == idl.Int && (strings.HasSuffix(f.Name, g.cfg.FDFieldSuffix) ||
		f.Name == strings.ToLower(g.cfg.FDFieldSuffix[:1])+g.cfg.FDFieldSuffix[1:])
}

func (g *pkgGen) namedType(td *idl.TypeDef) {
	name := g.typeNames[td.Name]
	qual := g.iface.Name + "." + td.Name
	if td.Type.Kind == idl.Enum {
		g.enumType(name, fmt.Sprintf("%s is the Varlink enum %s.", name, qual), td.Doc, td.Type)
		return
	}
	g.structType(name, fmt.Sprintf("%s is the Varlink type %s.", name, qual), td.Doc, td.Type)
}

func (g *pkgGen) enumType(name, summary, doc string, t *idl.Type) {
	g.p("// %s\n", summary)
	if doc != "" {
		g.p("//\n")
		writeDoc(&g.decls, "", doc)
	}
	g.p("type %s string\n\n", name)
	if len(t.Values) > 0 {
		g.p("// Values of %s.\nconst (\n", name)
		for _, v := range t.Values {
			c := g.claim(name + GoName(v.Name))
			if v.Doc != "" {
				writeDoc(&g.decls, "\t", v.Doc)
			}
			g.p("\t%s %s = %q\n", c, name, v.Name)
		}
		g.p(")\n\n")
	}
	g.p("// String returns the wire value.\nfunc (v %s) String() string { return string(v) }\n\n", name)
}

type pendingType struct {
	name string
	doc  string
	t    *idl.Type
}

// structType emits a struct plus an UnmarshalJSON that keeps unknown fields
// in Extra. Inline struct and enum types of its fields are emitted after it.
func (g *pkgGen) structType(name, summary, doc string, t *idl.Type) {
	var nested []pendingType
	type fieldOut struct {
		doc, goName, goType, tag string
	}
	var fields []fieldOut
	used := map[string]bool{}
	var known []string
	for _, f := range t.Fields {
		fname := GoName(f.Name)
		for used[fname] || fname == "Extra" || fname == "UnmarshalJSON" || fname == "MarshalJSON" {
			fname += "_"
		}
		used[fname] = true
		known = append(known, f.Name)
		if g.fdField(f) {
			g.usesOS = true
			fields = append(fields, fieldOut{f.Doc, fname, "*os.File", fmt.Sprintf("`json:\"-\" varlink:\"%s,fd\"`", f.Name)})
			continue
		}
		gt := g.goType(f.Type, name+fname, &nested)
		tag := f.Name
		if f.Type.Kind == idl.Nullable {
			tag += ",omitempty"
		}
		fields = append(fields, fieldOut{f.Doc, fname, gt, fmt.Sprintf("`json:%q`", tag)})
	}
	g.usesJSON = true
	g.p("// %s\n", summary)
	if doc != "" {
		g.p("//\n")
		writeDoc(&g.decls, "", doc)
	}
	g.p("type %s struct {\n", name)
	for i, f := range fields {
		if f.doc != "" {
			if i > 0 {
				g.p("\n")
			}
			writeDoc(&g.decls, "\t", f.doc)
		}
		g.p("\t%s %s %s\n", f.goName, f.goType, f.tag)
	}
	if len(fields) > 0 {
		g.p("\n")
	}
	g.p("\t// Extra holds fields sent by the service that these bindings do not\n")
	g.p("\t// know, such as fields added in newer systemd releases.\n")
	g.p("\tExtra map[string]json.RawMessage `json:\"-\"`\n}\n\n")

	g.p("// UnmarshalJSON decodes known fields and keeps the rest in Extra.\n")
	g.p("func (x *%s) UnmarshalJSON(data []byte) error {\n", name)
	g.p("\ttype plain %s\n\tif err := json.Unmarshal(data, (*plain)(x)); err != nil {\n\t\treturn err\n\t}\n", name)
	g.p("\textra, err := varlink.UnknownFields(data")
	for _, k := range known {
		g.p(", %q", k)
	}
	g.p(")\n\tx.Extra = extra\n\treturn err\n}\n\n")
	g.p("// MarshalJSON encodes the struct, including fields kept in Extra.\n")
	g.p("func (x %s) MarshalJSON() ([]byte, error) {\n", name)
	g.p("\ttype plain %s\n\treturn varlink.MarshalWithExtra(plain(x), x.Extra)\n}\n\n", name)

	for _, n := range nested {
		if n.t.Kind == idl.Enum {
			g.enumType(n.name, n.doc, "", n.t)
		} else {
			g.structType(n.name, n.doc, "", n.t)
		}
	}
}

func (g *pkgGen) goType(t *idl.Type, inlineName string, nested *[]pendingType) string {
	switch t.Kind {
	case idl.Bool:
		return "bool"
	case idl.Int:
		return "int64"
	case idl.Float:
		return "float64"
	case idl.String:
		return "string"
	case idl.Object, idl.Any:
		g.usesJSON = true
		return "json.RawMessage"
	case idl.Named:
		if n, ok := g.typeNames[t.Name]; ok {
			return n
		}
		// Unknown names cannot be typed; keep the raw JSON.
		g.usesJSON = true
		return "json.RawMessage"
	case idl.Array:
		return "[]" + g.goType(t.Elem, inlineName, nested)
	case idl.Map:
		return "map[string]" + g.goType(t.Elem, inlineName, nested)
	case idl.Nullable:
		inner := g.goType(t.Elem, inlineName, nested)
		switch t.Elem.Kind {
		case idl.Array, idl.Map, idl.Object, idl.Any:
			return inner
		}
		if t.Elem.Kind == idl.Named && inner == "json.RawMessage" {
			return inner
		}
		return "*" + inner
	case idl.Struct, idl.Enum:
		name := g.claim(inlineName)
		kind := "struct"
		if t.Kind == idl.Enum {
			kind = "enum"
		}
		*nested = append(*nested, pendingType{name: name, doc: fmt.Sprintf("%s is an inline %s type.", name, kind), t: t})
		return name
	}
	panic(fmt.Sprintf("unhandled kind %v", t.Kind))
}

func (g *pkgGen) errorType(typ, sentinel string, e *idl.Error) {
	qual := g.iface.Name + "." + e.Name
	g.p("// %s is the Varlink error %s. Match it with errors.As, or with\n// errors.Is(err, %s).\n", typ, qual, sentinel)
	if e.Doc != "" {
		g.p("//\n")
		writeDoc(&g.decls, "", e.Doc)
	}
	g.p("type %s struct {\n", typ)
	var nested []pendingType
	used := map[string]bool{"Error": true, "Is": true, "VarlinkError": true}
	for _, f := range e.Params.Fields {
		fname := GoName(f.Name)
		for used[fname] {
			fname += "_"
		}
		used[fname] = true
		if f.Doc != "" {
			writeDoc(&g.decls, "\t", f.Doc)
		}
		tag := f.Name
		if f.Type.Kind == idl.Nullable {
			tag += ",omitempty"
		}
		g.p("\t%s %s `json:%q`\n", fname, g.goType(f.Type, typ+fname, &nested), tag)
	}
	g.p("}\n\n")
	g.p("// %s matches any %s reply.\nvar %s = &varlink.Error{Name: %q}\n\n", sentinel, e.Name, sentinel, qual)
	g.p("// VarlinkError returns the qualified error name.\nfunc (*%s) VarlinkError() string { return %q }\n\n", typ, qual)
	if len(e.Params.Fields) == 0 {
		g.p("func (*%s) Error() string { return %q }\n\n", typ, "varlink: "+qual)
	} else {
		g.p("func (e *%s) Error() string {\n\tparams, _ := json.Marshal(e)\n\treturn %q + string(params)\n}\n\n", typ, "varlink: "+qual+" ")
	}
	g.p("// Is reports whether target names the same error, so errors.Is(err, %s)\n// works.\n", sentinel)
	g.p("func (e *%s) Is(target error) bool { return varlink.ErrorName(target) == e.VarlinkError() }\n\n", typ)
	for _, n := range nested {
		if n.t.Kind == idl.Enum {
			g.enumType(n.name, n.doc, "", n.t)
		} else {
			g.structType(n.name, n.doc, "", n.t)
		}
	}
}

func writeDoc(b *bytes.Buffer, indent, doc string) {
	for _, line := range strings.Split(doc, "\n") {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			fmt.Fprintf(b, "%s//\n", indent)
			continue
		}
		fmt.Fprintf(b, "%s// %s\n", indent, line)
	}
}

// sortedKeys returns m's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
