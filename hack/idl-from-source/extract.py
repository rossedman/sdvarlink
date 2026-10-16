#!/usr/bin/env python3
"""Extract Varlink IDL from a systemd source tree.

systemd defines its Varlink interfaces in C with the SD_VARLINK_DEFINE_*
macros (src/shared/varlink-io.systemd.*.c). This script runs those files
through the C preprocessor with stub macros (prelude.h) that expand into
Python expressions, evaluates them, and prints each interface in the same
IDL text format that `varlinkctl introspect` emits, including systemd's
"# [Requires 'more' flag]" annotations.

It exists so that IDL snapshots can be produced for any systemd release
without booting it. The canonical capture path is still
hack/capture-idl.sh, which asks a running systemd over its sockets.

Usage:
    extract.py --src /path/to/systemd --version 260 --out idl/v260
"""

import argparse
import glob
import os
import re
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))

NULLABLE, ARRAY, MAP, SUPPORTS_MORE, REQUIRES_MORE = 1, 2, 4, 8, 16


class Comment:
    def __init__(self, text):
        self.text = text


class SymbolComment(Comment):
    pass


class InterfaceComment(Comment):
    pass


class Named:
    def __init__(self, name):
        self.name = name


class EnumValue:
    def __init__(self, name):
        self.name = name


class Field:
    def __init__(self, name, typ, flags, direction):
        self.name, self.typ, self.flags, self.direction = name, typ, flags, direction


class Symbol:
    kind = ""

    def __init__(self, name, *items):
        self.name = name
        self.items = list(items)


class Struct(Symbol):
    kind = "type"


class Enum(Symbol):
    kind = "enum"


class Error(Symbol):
    kind = "error"


class Method(Symbol):
    kind = "method"

    def __init__(self, name, flags, *items):
        super().__init__(name, *items)
        self.flags = flags


class Interface:
    def __init__(self, name, *items):
        self.name = name
        self.items = list(items)


DEFINE = re.compile(r"^#\s*define\s+(\w+)(?:\\\n|.)*?(?<!\\)$", re.M)


def header_macros(root):
    """Collect #define macros from systemd headers that build Varlink fields,
    e.g. VARLINK_DEFINE_POLKIT_INPUT from bus-polkit.h."""
    out = []
    for path in sorted(glob.glob(os.path.join(root, "src/shared/*.h"))
                       + glob.glob(os.path.join(root, "src/libsystemd/sd-varlink/*.h"))):
        with open(path, encoding="utf-8") as f:
            text = f.read()
        for m in DEFINE.finditer(text):
            if "SD_VARLINK_" in m.group(0) and not m.group(1).startswith("SD_VARLINK_"):
                out.append(m.group(0))
    return "\n".join(out) + "\n"


def preprocess(path, version, macros):
    """Run cpp over one source file with includes stripped."""
    with open(path, encoding="utf-8") as f:
        src = f.read()
    src = macros + re.sub(r"^\s*#\s*include.*$", "", src, flags=re.M)
    out = subprocess.run(
        [
            "cpp", "-P", "-w",
            "-include", os.path.join(HERE, "prelude.h"),
            f'-DPROJECT_VERSION_STR="{version}"',
            "-",
        ],
        input=src, capture_output=True, text=True, check=True,
    )
    return out.stdout


STMT = re.compile(r"__SDV_(SYM|IFACE)__\s+(\w+)\s*=\s*")


def statements(text):
    """Yield (kind, var, expr) for each macro expansion, balancing parens."""
    for m in STMT.finditer(text):
        i = m.end()
        start = i
        depth = 0
        in_str = False
        while i < len(text):
            c = text[i]
            if in_str:
                if c == "\\":
                    i += 1
                elif c == '"':
                    in_str = False
            elif c == '"':
                in_str = True
            elif c == "(":
                depth += 1
            elif c == ")":
                depth -= 1
                if depth == 0:
                    i += 1
                    break
            i += 1
        expr = text[start:i].replace("&", "")
        yield m.group(1), m.group(2), expr


class Scope(dict):
    """Name lookup: this file's symbols first, then any file's."""

    def __init__(self, local, shared):
        super().__init__()
        self.local, self.shared = local, shared

    def __getitem__(self, key):
        if key in self.local:
            return self.local[key]
        return self.shared[key]


def type_str(f):
    s = "?" if f.flags & NULLABLE else ""
    if f.flags & MAP:
        s += "[string]"
    elif f.flags & ARRAY:
        s += "[]"
    return s + (f.typ.name if isinstance(f.typ, Named) else f.typ)


def comments(lines, indent):
    out = []
    for c in lines:
        for line in c.text.split("\n"):
            out.append(f"{indent}# {line}".rstrip())
    return out


def fmt_fields(items, direction, indent=""):
    inner = indent + "\t"
    entries = []
    pending = []
    for it in items:
        if isinstance(it, Comment):
            pending.append(it)
            continue
        if isinstance(it, EnumValue):
            entries.append(comments(pending, inner) + [inner + it.name])
        elif isinstance(it, Field) and it.direction == direction:
            entries.append(comments(pending, inner) + [f"{inner}{it.name}: {type_str(it)}"])
        pending = []
    if not entries:
        return "()"
    return "(\n" + ",\n".join("\n".join(e) for e in entries) + "\n" + indent + ")"


def fmt_symbol(sym):
    if isinstance(sym, Enum):
        return f"type {sym.name}" + fmt_fields(sym.items, None)
    if isinstance(sym, Struct):
        return f"type {sym.name}" + fmt_fields(sym.items, "R")
    if isinstance(sym, Error):
        return f"error {sym.name}" + fmt_fields(sym.items, "R")
    head = ""
    if sym.flags & REQUIRES_MORE:
        head = "# [Requires 'more' flag]\n"
    elif sym.flags & SUPPORTS_MORE:
        head = "# [Supports 'more' flag]\n"
    return (head + f"method {sym.name}" + fmt_fields(sym.items, "I")
            + " -> " + fmt_fields(sym.items, "O"))


ORDER = [Enum, Struct, Method, Error]


def fmt_interface(iface):
    out = comments([c for c in iface.items if isinstance(c, InterfaceComment)], "")
    out.append(f"interface {iface.name}")
    for kind in ORDER:
        pending = []
        for it in iface.items:
            if isinstance(it, SymbolComment):
                pending.append(it)
                continue
            if isinstance(it, InterfaceComment):
                continue
            if type(it) is kind:
                out.append("")
                out.extend(comments(pending, ""))
                out.append(fmt_symbol(it))
            pending = []
    return "\n".join(out) + "\n"


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--src", required=True, help="systemd source checkout")
    ap.add_argument("--version", required=True, help="systemd version, e.g. 260")
    ap.add_argument("--out", required=True, help="output directory")
    args = ap.parse_args()

    files = [os.path.join(args.src, "src/shared/varlink-idl-common.c")]
    files += sorted(glob.glob(os.path.join(args.src, "src/shared/varlink-io.systemd.*.c")))
    files.append(os.path.join(args.src, "src/libsystemd/sd-varlink/varlink-io.systemd.c"))
    macros = header_macros(args.src)

    builtins = {k: globals()[k] for k in (
        "Interface", "Method", "Error", "Struct", "Enum", "EnumValue",
        "Field", "Named", "Comment", "SymbolComment", "InterfaceComment")}
    shared = {}  # first definition of each symbol across files, for cross-file references
    units = []   # (statements, file-local symbols)
    for path in files:
        if os.path.exists(path):
            units.append((list(statements(preprocess(path, args.version, macros))), {}))

    interfaces = []
    # Files may use types defined in other files, so evaluate until a fixed point.
    progress = True
    while progress:
        progress = False
        for stmts, local in units:
            while stmts:
                kind, var, expr = stmts[0]
                try:
                    val = eval(expr, builtins, Scope(local, shared))  # noqa: S307
                except NameError:
                    break
                stmts.pop(0)
                progress = True
                local[var] = val
                shared.setdefault(var, val)
                if kind == "IFACE":
                    interfaces.append(val)
    failed = False
    for stmts, local in units:
        if stmts:
            failed = True
            kind, var, expr = stmts[0]
            try:
                eval(expr, builtins, Scope(local, shared))
            except NameError as e:
                print(f"unresolved {var}: {e}", file=sys.stderr)
    if failed:
        sys.exit(1)

    os.makedirs(args.out, exist_ok=True)
    for iface in sorted(interfaces, key=lambda i: i.name):
        with open(os.path.join(args.out, iface.name + ".varlink"), "w", encoding="utf-8") as f:
            f.write(fmt_interface(iface))
        print(iface.name)


if __name__ == "__main__":
    main()
