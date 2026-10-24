package varlinktest

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rossedman/sdvarlink/varlink"
)

const (
	svcEcho   = "com.example.Svc.Echo"
	svcCount  = "com.example.Svc.Count"
	svcFail   = "com.example.Svc.Fail"
	svcBroken = "com.example.Svc.Broken"
	svcNotify = "com.example.Svc.Notify"
	svcBlob   = "com.example.Svc.Blob"
	svcBoom   = "com.example.Svc.Boom"
)

// newUpstream starts a server standing in for the real service.
func newUpstream(t *testing.T) (*Server, <-chan struct{}) {
	t.Helper()
	up := NewServer(t)
	notified := make(chan struct{}, 4)
	On(up, svcEcho, func(_ context.Context, in greetIn) (greetOut, error) { return greetOut{"echo " + in.Name}, nil })
	OnStream(up, svcCount, func(context.Context, struct{}) iter.Seq2[tick, error] { return streamOf(3, 0) })
	up.HandleRaw(svcFail, fail(&varlink.Error{Name: svcBoom, Parameters: json.RawMessage(`{"why":"because"}`)}))
	OnStream(up, svcBroken, func(context.Context, struct{}) iter.Seq2[tick, error] { return streamOf(5, 3) })
	up.HandleRaw(svcNotify, func(context.Context, *Call) iter.Seq2[*Response, error] {
		return func(yield func(*Response, error) bool) {
			notified <- struct{}{}
		}
	})
	up.HandleRaw(svcBlob, func(_ context.Context, call *Call) iter.Seq2[*Response, error] {
		return func(yield func(*Response, error) bool) {
			f := tempFileWith(t, "reply")
			yield(&Response{Parameters: tick{len(call.Files)}, Files: []*os.File{f}}, nil)
		}
	})
	return up, notified
}

// exercise drives every kind of call through c and returns what it saw, one
// line per call, so a recording and its replay can be compared.
func exercise(t *testing.T, c *varlink.Conn, notified <-chan struct{}) []string {
	t.Helper()
	ctx := t.Context()
	var seen []string
	note := func(s string) { seen = append(seen, s) }

	var echo greetOut
	err := c.Call(ctx, svcEcho, greetIn{"a"}, &echo)
	note("echo " + echo.Greeting + " " + errName(err))

	for r, err := range c.Stream(ctx, svcCount, nil) {
		if err != nil {
			note("count error " + errName(err))
			break
		}
		note("count " + string(r.Parameters))
	}

	err = c.Call(ctx, svcFail, nil, nil)
	var ve *varlink.Error
	if errors.As(err, &ve) {
		note("fail " + ve.Name + " " + string(ve.Parameters))
	} else {
		note("fail unexpected " + errName(err))
	}

	for r, err := range c.Stream(ctx, svcBroken, struct{}{}) {
		if err != nil {
			note("broken error " + errName(err))
			break
		}
		note("broken " + string(r.Parameters))
	}

	if err := c.Oneway(ctx, svcNotify, tick{9}); err != nil {
		t.Fatal(err)
	}
	if notified != nil {
		<-notified
	}

	reply, err := c.CallReply(ctx, svcBlob, nil, varlink.WithFiles(tempFileWith(t, "request")))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range reply.Files {
		f.Close()
	}
	note("blob " + string(reply.Parameters))
	return seen
}

func errName(err error) string {
	if err == nil {
		return "ok"
	}
	if name := varlink.ErrorName(err); name != "" {
		return name
	}
	return err.Error()
}

func TestRecordThenReplay(t *testing.T) {
	t.Parallel()
	up, notified := newUpstream(t)
	golden := filepath.Join(t.TempDir(), "session.json")

	tb := &fakeTB{TB: t}
	rec := Record(tb, up.Path(), golden)
	recorded := exercise(t, rec.Dial(), notified)
	tb.runCleanups() // writes the golden file
	if errs := tb.errors(); len(errs) != 0 {
		t.Fatalf("Record reported %q", errs)
	}

	wantSeen := []string{
		"echo echo a ok",
		`count {"n":1}`, `count {"n":2}`, `count {"n":3}`,
		`fail com.example.Svc.Boom {"why":"because"}`,
		`broken {"n":1}`, `broken {"n":2}`, "broken error com.example.Ticks.Stopped",
		`blob {"n":1}`,
	}
	if !slices.Equal(recorded, wantSeen) {
		t.Fatalf("through Record saw\n%q\nwant\n%q", recorded, wantSeen)
	}

	data, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), "]\n") || !strings.Contains(string(data), "\n  {") {
		t.Errorf("golden is not indented JSON with a trailing newline:\n%s", data)
	}
	var log []Exchange
	if err := json.Unmarshal(data, &log); err != nil {
		t.Fatal(err)
	}
	type shape struct {
		method  string
		replies int
		errName string
		more    bool
		oneway  bool
	}
	var got []shape
	for _, ex := range log {
		s := shape{method: ex.Call.Method, replies: len(ex.Replies), more: ex.Call.More, oneway: ex.Call.Oneway}
		if ex.Error != nil {
			s.errName = ex.Error.Name
		}
		if len(ex.Call.Files) != 0 {
			t.Errorf("%s: files were recorded", ex.Call.Method)
		}
		got = append(got, s)
	}
	wantShapes := []shape{
		{method: svcEcho, replies: 1},
		{method: svcCount, replies: 3, more: true},
		{method: svcFail, errName: svcBoom},
		{method: svcBroken, replies: 2, errName: "com.example.Ticks.Stopped", more: true},
		{method: svcNotify, oneway: true},
		{method: svcBlob, replies: 1},
	}
	if !slices.Equal(got, wantShapes) {
		t.Fatalf("recorded exchanges\n%+v\nwant\n%+v", got, wantShapes)
	}

	rtb := &fakeTB{TB: t}
	replayed := exercise(t, Replay(rtb, golden).Dial(), nil)
	rtb.runCleanups()
	if errs := rtb.errors(); len(errs) != 0 {
		t.Fatalf("Replay reported %q", errs)
	}
	if !slices.Equal(replayed, recorded) {
		t.Fatalf("replay saw\n%q\nrecorded run saw\n%q", replayed, recorded)
	}
}

func TestRecordFailures(t *testing.T) {
	t.Parallel()
	t.Run("upstream missing", func(t *testing.T) {
		t.Parallel()
		tb := &fakeTB{TB: t}
		runFatal(tb, func() { Record(tb, filepath.Join(t.TempDir(), "absent"), filepath.Join(t.TempDir(), "g.json")) })
		if msgs := tb.fatalMessages(); len(msgs) != 1 || !strings.Contains(msgs[0], "Record") {
			t.Fatalf("fatal messages = %q", msgs)
		}
	})
	t.Run("golden not writable", func(t *testing.T) {
		t.Parallel()
		up, _ := newUpstream(t)
		tb := &fakeTB{TB: t}
		Record(tb, up.Path(), filepath.Join(t.TempDir(), "no", "such", "dir", "g.json"))
		tb.runCleanups()
		if errs := tb.errors(); len(errs) != 1 || !strings.Contains(errs[0], "Record") {
			t.Fatalf("reports = %q", errs)
		}
	})
	t.Run("upstream error without name is forwarded", func(t *testing.T) {
		t.Parallel()
		up := NewServer(t)
		up.HandleRaw(svcFail, fail(errors.New("plain")))
		tb := &fakeTB{TB: t}
		golden := filepath.Join(t.TempDir(), "g.json")
		err := Record(tb, up.Path(), golden).Dial().Call(t.Context(), svcFail, nil, nil)
		tb.runCleanups()
		if varlink.ErrorName(err) != HandlerError {
			t.Fatalf("err = %v", err)
		}
		var log []Exchange
		data, rerr := os.ReadFile(golden)
		if rerr != nil || json.Unmarshal(data, &log) != nil || len(log) != 1 || log[0].Error == nil || log[0].Error.Name != HandlerError {
			t.Fatalf("golden = %s (%v)", data, rerr)
		}
	})
}

func writeGolden(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "golden.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReplayFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		golden func(t *testing.T) string
		want   string
	}{
		{name: "missing file", golden: func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent.json") }, want: "Replay"},
		{name: "invalid json", golden: func(t *testing.T) string { return writeGolden(t, "{not json") }, want: "golden.json"},
		{name: "wrong shape", golden: func(t *testing.T) string { return writeGolden(t, `{"call":{}}`) }, want: "golden.json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tb := &fakeTB{TB: t}
			path := tc.golden(t)
			runFatal(tb, func() { Replay(tb, path) })
			msgs := tb.fatalMessages()
			if len(msgs) != 1 || !strings.Contains(msgs[0], tc.want) {
				t.Fatalf("fatal messages = %q, want one containing %q", msgs, tc.want)
			}
		})
	}
}

func TestReplayMatching(t *testing.T) {
	t.Parallel()
	const golden = `[
	  {"call":{"method":"a.b.C","parameters":{"x":1,"y":[1,2]}},"replies":[{"r":1}]},
	  {"call":{"method":"a.b.C","parameters":{"x":2}},"replies":[{"r":2}]},
	  {"call":{"method":"a.b.NoParams"},"replies":[{"r":3}]},
	  {"call":{"method":"a.b.Stream","more":true},"replies":[{"n":1},{"n":2}],"error":{"name":"a.b.Done","parameters":{"k":"v"}}},
	  {"call":{"method":"a.b.Err"},"error":{"name":"a.b.Bad"}}
	]`
	type step struct {
		name        string
		method      string
		params      any
		more        bool
		wantReplies []string
		wantErr     string
		wantReport  string
	}
	steps := []step{
		{name: "first in order, reformatted equal params", method: "a.b.C", params: json.RawMessage(`{ "y": [1, 2], "x": 1 }`), wantReplies: []string{`{"r":1}`}},
		{name: "second in order", method: "a.b.C", params: map[string]int{"x": 2}, wantReplies: []string{`{"r":2}`}},
		{name: "empty params equal absent", method: "a.b.NoParams", params: nil, wantReplies: []string{`{"r":3}`}},
		{name: "stream with final error", method: "a.b.Stream", more: true, wantReplies: []string{`{"n":1}`, `{"n":2}`}, wantErr: "a.b.Done"},
		{name: "recorded error only", method: "a.b.Err", wantErr: "a.b.Bad"},
		{name: "exhausted queue", method: "a.b.C", params: map[string]int{"x": 3}, wantErr: varlink.ErrMethodNotFound.Name, wantReport: "unexpected call to a.b.C"},
		{name: "unknown method", method: "a.b.Other", wantErr: varlink.ErrMethodNotFound.Name, wantReport: "unexpected call to a.b.Other"},
	}
	tb := &fakeTB{TB: t}
	c := Replay(tb, writeGolden(t, golden)).Dial()
	t.Cleanup(tb.runCleanups)
	// Steps share one replay, so they run in order.
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			before := len(tb.errors())
			var replies []string
			var err error
			if st.more {
				for r, serr := range c.Stream(t.Context(), st.method, st.params) {
					if serr != nil {
						err = serr
						break
					}
					replies = append(replies, string(r.Parameters))
				}
			} else {
				var r *varlink.Reply
				if r, err = c.CallReply(t.Context(), st.method, st.params); err == nil {
					replies = append(replies, string(r.Parameters))
				}
			}
			if !slices.Equal(replies, st.wantReplies) {
				t.Errorf("replies = %q, want %q", replies, st.wantReplies)
			}
			if name := varlink.ErrorName(err); name != st.wantErr {
				t.Errorf("error = %v (name %q), want %q", err, name, st.wantErr)
			}
			reports := tb.errors()[before:]
			if st.wantReport == "" && len(reports) != 0 {
				t.Errorf("unexpected reports %q", reports)
			}
			if st.wantReport != "" && (len(reports) != 1 || !strings.Contains(reports[0], st.wantReport)) {
				t.Errorf("reports = %q, want one containing %q", reports, st.wantReport)
			}
		})
	}
}

func TestReplayParameterMismatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		params any
	}{
		{name: "different value", params: map[string]int{"x": 2}},
		{name: "extra field", params: map[string]int{"x": 1, "z": 0}},
		{name: "missing parameters", params: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tb := &fakeTB{TB: t}
			c := Replay(tb, writeGolden(t, `[{"call":{"method":"a.b.C","parameters":{"x":1}},"replies":[{"ok":true}]}]`)).Dial()
			t.Cleanup(tb.runCleanups)
			r, err := c.CallReply(t.Context(), "a.b.C", tc.params)
			if err != nil || string(r.Parameters) != `{"ok":true}` {
				t.Fatalf("a mismatch still replies: %v, %v", r, err)
			}
			errs := tb.errors()
			if len(errs) != 1 || !strings.Contains(errs[0], "called with") {
				t.Fatalf("reports = %q", errs)
			}
		})
	}
}

func TestJSONEqual(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "identical", a: `{"a":1}`, b: `{"a":1}`, want: true},
		{name: "whitespace", a: "{ \"a\" :\n 1 }", b: `{"a":1}`, want: true},
		{name: "key order", a: `{"a":1,"b":2}`, b: `{"b":2,"a":1}`, want: true},
		{name: "different value", a: `{"a":1}`, b: `{"a":2}`},
		{name: "different key", a: `{"a":1}`, b: `{"b":1}`},
		{name: "nested", a: `{"a":{"b":[1,{"c":2}]}}`, b: `{"a":{"b":[1,{"c":3}]}}`},
		{name: "array order", a: `[1,2]`, b: `[2,1]`},
		{name: "both empty", a: ``, b: ``, want: true},
		{name: "empty equals empty object", a: ``, b: `{}`, want: true},
		{name: "blank equals empty object", a: " \n", b: `{}`, want: true},
		{name: "empty differs from content", a: ``, b: `{"a":1}`},
		{name: "null differs from empty object", a: `null`, b: `{}`},
		{name: "same invalid", a: `{bad`, b: `{bad`, want: true},
		{name: "different invalid", a: `{bad`, b: `{worse`},
		{name: "invalid differs from valid", a: `{`, b: `{}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := jsonEqual(json.RawMessage(tc.a), json.RawMessage(tc.b)); got != tc.want {
				t.Fatalf("jsonEqual(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
			if got := jsonEqual(json.RawMessage(tc.b), json.RawMessage(tc.a)); got != tc.want {
				t.Fatalf("jsonEqual(%q, %q) = %v, want %v (reversed)", tc.b, tc.a, got, tc.want)
			}
		})
	}
}

func TestMustJSON(t *testing.T) {
	t.Parallel()
	if got := string(mustJSON(map[string]int{"a": 1})); got != `{"a":1}` {
		t.Errorf("mustJSON = %s", got)
	}
	defer func() {
		if recover() == nil {
			t.Error("mustJSON did not panic on an unencodable value")
		}
	}()
	mustJSON(make(chan int))
}
