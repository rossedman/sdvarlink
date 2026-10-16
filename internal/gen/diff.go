package gen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rossedman/sdvarlink/idl"
)

// Change is one difference between two releases of an interface.
type Change struct {
	Interface string
	Kind      string // "interface", "method", "type", "error"
	Name      string
	Op        string // "added", "removed", "changed"
	Detail    string
}

// Diff compares two sets of interfaces, keyed by name.
func Diff(old, new map[string]*idl.Interface) []Change {
	var out []Change
	for _, name := range union(old, new) {
		o, n := old[name], new[name]
		switch {
		case o == nil:
			out = append(out, Change{Interface: name, Kind: "interface", Name: name, Op: "added",
				Detail: fmt.Sprintf("%d methods", len(n.Methods))})
			continue
		case n == nil:
			out = append(out, Change{Interface: name, Kind: "interface", Name: name, Op: "removed"})
			continue
		}
		om, nm := map[string]string{}, map[string]string{}
		for _, m := range o.Methods {
			om[m.Name] = methodSig(m)
		}
		for _, m := range n.Methods {
			nm[m.Name] = methodSig(m)
		}
		out = append(out, diffMap(name, "method", om, nm)...)
		ot, nt := map[string]string{}, map[string]string{}
		for _, t := range o.Types {
			ot[t.Name] = t.Type.String()
		}
		for _, t := range n.Types {
			nt[t.Name] = t.Type.String()
		}
		out = append(out, diffMap(name, "type", ot, nt)...)
		oe, ne := map[string]string{}, map[string]string{}
		for _, e := range o.Errors {
			oe[e.Name] = e.Params.String()
		}
		for _, e := range n.Errors {
			ne[e.Name] = e.Params.String()
		}
		out = append(out, diffMap(name, "error", oe, ne)...)
	}
	return out
}

func methodSig(m *idl.Method) string {
	s := m.In.String() + " -> " + m.Out.String()
	if m.More != idl.MoreNone {
		s += " [" + m.More.String() + "]"
	}
	return s
}

func union[V any](a, b map[string]V) []string {
	set := map[string]bool{}
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func diffMap(iface, kind string, o, n map[string]string) []Change {
	var out []Change
	for _, k := range union(o, n) {
		ov, ook := o[k]
		nv, nok := n[k]
		switch {
		case !ook:
			out = append(out, Change{iface, kind, k, "added", ""})
		case !nok:
			out = append(out, Change{iface, kind, k, "removed", ""})
		case ov != nv:
			out = append(out, Change{iface, kind, k, "changed", fieldDelta(ov, nv)})
		}
	}
	return out
}

// fieldDelta summarizes which top-level fields changed between two
// signatures, keeping the report readable for large structs.
func fieldDelta(o, n string) string {
	of, nf := fieldSet(o), fieldSet(n)
	var add, del []string
	for f := range nf {
		if !of[f] {
			add = append(add, f)
		}
	}
	for f := range of {
		if !nf[f] {
			del = append(del, f)
		}
	}
	sort.Strings(add)
	sort.Strings(del)
	var parts []string
	if len(add) > 0 {
		parts = append(parts, "+ "+strings.Join(add, ", "))
	}
	if len(del) > 0 {
		parts = append(parts, "− "+strings.Join(del, ", "))
	}
	if len(parts) == 0 {
		return "streaming or nested type changed"
	}
	return strings.Join(parts, "; ")
}

// fieldSet splits "(a: int, b: ?string) -> (c: bool)" into its top-level
// members, tagging output members with "->".
func fieldSet(sig string) map[string]bool {
	set := map[string]bool{}
	depth := 0
	prefix := ""
	start := 0
	for i := 0; i < len(sig); i++ {
		switch sig[i] {
		case '(':
			depth++
			if depth == 1 {
				start = i + 1
			}
		case ')', ',':
			if depth == 1 {
				if m := strings.TrimSpace(sig[start:i]); m != "" {
					set[prefix+m] = true
				}
				start = i + 1
			}
			if sig[i] == ')' {
				depth--
			}
		case '>':
			if depth == 0 {
				prefix = "→ "
			}
		}
	}
	return set
}

// DiffMarkdown renders changes as a Markdown section.
func DiffMarkdown(from, to string, changes []Change) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## systemd v%s → v%s\n\n", from, to)
	if len(changes) == 0 {
		b.WriteString("No changes to the Varlink API.\n\n")
		return b.String()
	}
	counts := map[string]int{}
	for _, c := range changes {
		counts[c.Op]++
	}
	fmt.Fprintf(&b, "%d added · %d changed · %d removed\n\n", counts["added"], counts["changed"], counts["removed"])
	b.WriteString("| Interface | Kind | Name | Change | Detail |\n| --- | --- | --- | --- | --- |\n")
	for _, c := range changes {
		icon := map[string]string{"added": ":material-plus-circle:{ .added } added", "removed": ":material-minus-circle:{ .removed } removed", "changed": ":material-pencil-circle:{ .changed } changed"}[c.Op]
		fmt.Fprintf(&b, "| `%s` | %s | `%s` | %s | %s |\n", c.Interface, c.Kind, c.Name, icon, escapePipes(c.Detail))
	}
	b.WriteString("\n")
	return b.String()
}

func escapePipes(s string) string { return strings.ReplaceAll(s, "|", "\\|") }
