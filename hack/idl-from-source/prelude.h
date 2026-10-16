/* Stub definitions that turn systemd's SD_VARLINK_* C macros into Python
 * expressions. See extract.py; this file is passed to cpp with -include. */
#define static
#define const
#define _unused_

#define SD_VARLINK_BOOL "bool"
#define SD_VARLINK_INT "int"
#define SD_VARLINK_FLOAT "float"
#define SD_VARLINK_STRING "string"
#define SD_VARLINK_OBJECT "object"
#define SD_VARLINK_ANY "any"

#define SD_VARLINK_NULLABLE 1
#define SD_VARLINK_ARRAY 2
#define SD_VARLINK_MAP 4
#define SD_VARLINK_SUPPORTS_MORE 8
#define SD_VARLINK_REQUIRES_MORE 16

#define SD_VARLINK_DEFINE_INTERFACE(sym, name, ...) __SDV_IFACE__ sym = Interface(name, ##__VA_ARGS__)
#define SD_VARLINK_DEFINE_METHOD(n, ...) __SDV_SYM__ vl_method_##n = Method(#n, 0, ##__VA_ARGS__)
#define SD_VARLINK_DEFINE_METHOD_FULL(n, fl, ...) __SDV_SYM__ vl_method_##n = Method(#n, fl, ##__VA_ARGS__)
#define SD_VARLINK_DEFINE_ERROR(n, ...) __SDV_SYM__ vl_error_##n = Error(#n, ##__VA_ARGS__)
#define SD_VARLINK_DEFINE_STRUCT_TYPE(n, ...) __SDV_SYM__ vl_type_##n = Struct(#n, ##__VA_ARGS__)
#define SD_VARLINK_DEFINE_ENUM_TYPE(n, ...) __SDV_SYM__ vl_type_##n = Enum(#n, ##__VA_ARGS__)
#define SD_VARLINK_DEFINE_ENUM_VALUE(v) EnumValue(#v)

#define SD_VARLINK_DEFINE_FIELD(n, t, f) Field(#n, t, f, "R")
#define SD_VARLINK_DEFINE_INPUT(n, t, f) Field(#n, t, f, "I")
#define SD_VARLINK_DEFINE_OUTPUT(n, t, f) Field(#n, t, f, "O")
#define SD_VARLINK_DEFINE_FIELD_BY_TYPE(n, t, f) Field(#n, Named(#t), f, "R")
#define SD_VARLINK_DEFINE_INPUT_BY_TYPE(n, t, f) Field(#n, Named(#t), f, "I")
#define SD_VARLINK_DEFINE_OUTPUT_BY_TYPE(n, t, f) Field(#n, Named(#t), f, "O")

#define SD_VARLINK_FIELD_COMMENT(s) Comment(s)
#define SD_VARLINK_SYMBOL_COMMENT(s) SymbolComment(s)
#define SD_VARLINK_INTERFACE_COMMENT(s) InterfaceComment(s)

/* String constants spliced into comments upstream. */
#define PRIVATE_UPLINK_RESOLV_CONF "/run/systemd/resolve/resolv.conf"
#define PRIVATE_STUB_RESOLV_CONF "/run/systemd/resolve/stub-resolv.conf"
#define PRIVATE_STATIC_RESOLV_CONF "/usr/lib/systemd/resolv.conf"
