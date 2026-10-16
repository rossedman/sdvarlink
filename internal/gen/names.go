package gen

import (
	"go/token"
	"strings"
	"unicode"
)

var initialisms = map[string]bool{
	"ACL": true, "API": true, "CPU": true, "DNS": true, "EFI": true, "GID": true,
	"HTTP": true, "ID": true, "IO": true, "IP": true, "JSON": true, "MAC": true,
	"MTU": true, "OS": true, "PCR": true, "PID": true, "SSH": true, "TLS": true,
	"TPM": true, "TTL": true, "TTY": true, "UID": true, "URI": true, "URL": true,
	"UUID": true,
}

// GoName converts a Varlink identifier (camelCase, PascalCase or
// snake_case) into an exported Go identifier, upper-casing common
// initialisms: "uid" -> "UID", "machineId" -> "MachineID",
// "horiz_pre" -> "HorizPre".
func GoName(s string) string {
	var b strings.Builder
	for _, w := range splitWords(s) {
		if up := strings.ToUpper(w); initialisms[up] {
			b.WriteString(up)
			continue
		}
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		b.WriteString(string(r))
	}
	out := b.String()
	if out == "" || !unicode.IsLetter([]rune(out)[0]) {
		out = "X" + out
	}
	return out
}

// splitWords splits on '_', '-', '.' and lower-to-upper transitions.
func splitWords(s string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = nil
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == '.':
			flush()
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1])):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return words
}

// PackageName derives a Go package name from an interface name:
// "io.systemd.Resolve.Monitor" -> "resolvemonitor", "io.systemd" -> "systemd".
// Names that are Go keywords get a suffix: "io.systemd.Import" -> "importd".
func PackageName(iface string) string {
	rest := strings.TrimPrefix(iface, "io.systemd.")
	if rest == iface && iface == "io.systemd" {
		return "systemd"
	}
	name := strings.ToLower(strings.ReplaceAll(rest, ".", ""))
	if token.IsKeyword(name) {
		// io.systemd.Import is served by systemd-importd.
		if name == "import" {
			return "importd"
		}
		return name + "pkg"
	}
	return name
}
