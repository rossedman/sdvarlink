package varlink

import (
	"encoding/json"
	"testing"

	"github.com/rossedman/sdvarlink/idl"
)

func FuzzValidateParams(f *testing.F) {
	iface, err := idl.Parse(validateIDL)
	if err != nil {
		f.Fatal(err)
	}
	in := iface.Method("M").In

	valid := []string{
		`{"b":true,"i":1,"f":1.5,"s":"x","o":{},"a":"v","arr":[1,2],"m":{"k":1},"c":"red","p":{"x":1},"ghost":1}`,
		`{"b":false,"i":18446744073709551615,"f":-1e3,"s":"","o":{"z":[1]},"a":[1],"arr":[],"m":{},"c":"green","p":{"x":0,"y":null},"n":"s","ghost":"anything"}`,
	}
	for _, s := range valid {
		f.Add([]byte(s), false)
		f.Add([]byte(s), true)
	}
	for _, s := range []string{
		``, `null`, `{}`, `[]`, `"x"`, `{`, `{"b":1}`,
		`{"b":true,"i":1.5,"f":1,"s":"x","o":{},"a":1,"arr":[],"m":{},"c":"red","p":{"x":1},"ghost":1}`,
		`{"b":true,"i":1,"f":1,"s":"x","o":{},"a":1,"arr":["a"],"m":{},"c":"blue","p":{"x":1},"ghost":1}`,
		`{"b":true,"i":1,"f":1,"s":"x","o":{},"a":1,"arr":[],"m":{},"c":"red","p":{"x":1,"extra":1},"ghost":1,"zzz":1}`,
		`{"b":true,"i":99999999999999999999,"f":1,"s":"x","o":{},"a":1,"arr":[],"m":{},"c":"red","p":{},"ghost":1}`,
	} {
		f.Add([]byte(s), false)
		f.Add([]byte(s), true)
	}

	// The valid seeds must pass, so the fuzzer starts from accepted inputs.
	for _, s := range valid {
		if field, err := validateParams(iface, in, []byte(s), false); err != nil {
			f.Fatalf("valid seed rejected: field %q: %v: %s", field, err, s)
		}
	}

	f.Fuzz(func(t *testing.T, params []byte, allowUnknown bool) {
		field, err := validateParams(iface, in, params, allowUnknown)
		if err == nil {
			if field != "" {
				t.Fatalf("nil error with field %q", field)
			}
			return
		}
		if field == "" {
			// The only nameless field is an unknown key spelled "", which
			// the strict check rejects by its (empty) name.
			var top map[string]json.RawMessage
			var hasEmptyKey bool
			if json.Unmarshal(params, &top) == nil {
				_, hasEmptyKey = top[""]
			}
			if allowUnknown || !hasEmptyKey {
				t.Fatalf("error %v without a field name", err)
			}
		}
		// Whatever validateParams returns, the client is told which
		// parameter was wrong.
		var reply map[string]string
		if json.Unmarshal(invalidParameter(field).Parameters, &reply) != nil || reply["parameter"] == "" {
			t.Fatalf("InvalidParameter for field %q names no parameter", field)
		}
		// The field is "parameters", a declared top-level field, or (only
		// when unknown fields are rejected) an undefined key.
		if field != "parameters" && !hasField(in, field) && allowUnknown {
			t.Fatalf("field %q is not declared but unknown fields are allowed", field)
		}
		// Accepting is monotone: if the strict check fails only on unknown
		// fields, the lenient check must pass; and strict-valid implies
		// lenient-valid.
		if !allowUnknown {
			return
		}
		if _, serr := validateParams(iface, in, params, false); serr == nil {
			t.Fatalf("strict accepted but lenient rejected: %v", err)
		}
	})
}
