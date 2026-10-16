package varlink

// Ptr returns a pointer to v. Generated input structs use pointers for
// optional (nullable) fields:
//
//	in := userdatabase.GetUserRecordIn{UserName: varlink.Ptr("ross"), Service: "io.systemd.Multiplexer"}
func Ptr[T any](v T) *T { return &v }

// Deref returns *p, or the zero value of T if p is nil. Generated reply
// structs use pointers for optional fields:
//
//	fmt.Println(varlink.Deref(out.PrettyHostname))
func Deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
