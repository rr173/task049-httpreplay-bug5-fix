package replay

import (
	"errors"
	"strings"
	"testing"
)

func TestRecordReplay(t *testing.T) {
	r := New()
	if err := r.Record(Request{Method: "GET", Path: "/users"},
		Response{Status: 200, Body: []byte("ok")}); err != nil {
		t.Fatalf("record: %v", err)
	}
	resp, err := r.Replay(Request{Method: "GET", Path: "/users"})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if resp.Status != 200 || string(resp.Body) != "ok" {
		t.Fatalf("replay = %+v, want status 200 body ok", resp)
	}
}

func TestQueryOrderInsensitive(t *testing.T) {
	r := New()
	rec := Request{Method: "GET", Path: "/search", Query: map[string]string{"a": "1", "b": "2"}}
	if err := r.Record(rec, Response{Status: 200, Body: []byte("hit")}); err != nil {
		t.Fatal(err)
	}
	// Same params, different map iteration seeding is enough; flip order by
	// constructing the map fresh (Go randomizes iteration order anyway).
	for i := 0; i < 10; i++ {
		q := map[string]string{"b": "2", "a": "1"}
		resp, err := r.Replay(Request{Method: "GET", Path: "/search", Query: q})
		if err != nil {
			t.Fatalf("replay #%d: %v", i, err)
		}
		if string(resp.Body) != "hit" {
			t.Fatalf("replay #%d body = %q", i, resp.Body)
		}
	}
}

func TestMethodCaseInsensitive(t *testing.T) {
	r := New()
	if err := r.Record(Request{Method: "post", Path: "/items"},
		Response{Status: 201, Body: []byte("created")}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"POST", "Post", "post"} {
		resp, err := r.Replay(Request{Method: m, Path: "/items"})
		if err != nil {
			t.Fatalf("replay %q: %v", m, err)
		}
		if resp.Status != 201 {
			t.Fatalf("replay %q status = %d", m, resp.Status)
		}
	}
}

func TestPathCaseSensitive(t *testing.T) {
	r := New()
	if err := r.Record(Request{Method: "GET", Path: "/Users"},
		Response{Status: 200, Body: []byte("u")}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Replay(Request{Method: "GET", Path: "/users"}); !errors.Is(err, ErrNotMatched) {
		t.Fatalf("replay /users: err=%v want ErrNotMatched", err)
	}
}

func TestRoundRobin(t *testing.T) {
	r := New()
	req := Request{Method: "GET", Path: "/r"}
	for _, s := range []int{10, 11, 12} {
		if err := r.Record(req, Response{Status: s}); err != nil {
			t.Fatal(err)
		}
	}
	want := []int{10, 11, 12, 10, 11, 12}
	for i, w := range want {
		resp, err := r.Replay(req)
		if err != nil {
			t.Fatalf("replay #%d: %v", i, err)
		}
		if resp.Status != w {
			t.Fatalf("replay #%d status = %d, want %d", i, resp.Status, w)
		}
	}
}

func TestCursorIndependent(t *testing.T) {
	r := New()
	reqA := Request{Method: "GET", Path: "/a"}
	reqB := Request{Method: "GET", Path: "/b"}
	for _, s := range []int{100, 101} {
		r.Record(reqA, Response{Status: s})
	}
	for _, s := range []int{200, 201} {
		r.Record(reqB, Response{Status: s})
	}
	// Interleave hits on A and B; cursors must not cross-contaminate.
	a1, _ := r.Replay(reqA) // A cursor 0->1: 100
	b1, _ := r.Replay(reqB) // B cursor 0->1: 200
	a2, _ := r.Replay(reqA) // A cursor 1->2: 101
	b2, _ := r.Replay(reqB) // B cursor 1->2: 201
	if a1.Status != 100 || a2.Status != 101 {
		t.Fatalf("A sequence = %d,%d want 100,101", a1.Status, a2.Status)
	}
	if b1.Status != 200 || b2.Status != 201 {
		t.Fatalf("B sequence = %d,%d want 200,201", b1.Status, b2.Status)
	}
}

func TestNotMatched(t *testing.T) {
	r := New()
	r.Record(Request{Method: "GET", Path: "/x"}, Response{Status: 200})
	_, err := r.Replay(Request{Method: "GET", Path: "/y"})
	if !errors.Is(err, ErrNotMatched) {
		t.Fatalf("err=%v want ErrNotMatched", err)
	}
	if !strings.Contains(err.Error(), "/y") {
		t.Fatalf("err %q should contain path /y", err.Error())
	}
}

func TestInvalidRequest(t *testing.T) {
	r := New()
	cases := []Request{
		{Method: "GET", Path: "no-slash"},
		{Method: "GET", Path: ""},
		{Method: "  ", Path: "/x"},
		{Method: "", Path: "/x"},
	}
	for _, c := range cases {
		if err := r.Record(c, Response{Status: 200}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("record %+v: err=%v want ErrInvalidRequest", c, err)
		}
		if _, err := r.Replay(c); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("replay %+v: err=%v want ErrInvalidRequest", c, err)
		}
	}
	// InvalidRequest must not be confused with NotMatched.
	if errors.Is(ErrInvalidRequest, ErrNotMatched) || errors.Is(ErrNotMatched, ErrInvalidRequest) {
		t.Fatal("ErrInvalidRequest and ErrNotMatched must be distinct")
	}
}

func TestRecordSideIsolation(t *testing.T) {
	r := New()
	body := []byte("orig")
	hdrs := map[string]string{"X-K": "v"}
	if err := r.Record(Request{Method: "GET", Path: "/i"}, Response{Status: 200, Body: body, Headers: hdrs}); err != nil {
		t.Fatal(err)
	}
	// Mutate the caller's slice/map after recording.
	body[0] = 'X'
	hdrs["X-K"] = "mutated"
	hdrs["extra"] = "z"
	resp, err := r.Replay(Request{Method: "GET", Path: "/i"})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "orig" {
		t.Fatalf("stored body mutated via caller slice: %q", resp.Body)
	}
	if resp.Headers["X-K"] != "v" {
		t.Fatalf("stored header mutated via caller map: %q", resp.Headers["X-K"])
	}
	if _, ok := resp.Headers["extra"]; ok {
		t.Fatal("stored header map reflects caller's later insert")
	}
}

func TestReplaySideIsolation(t *testing.T) {
	r := New()
	r.Record(Request{Method: "GET", Path: "/i"}, Response{Status: 200, Body: []byte("orig"), Headers: map[string]string{"X-K": "v"}})
	resp1, _ := r.Replay(Request{Method: "GET", Path: "/i"})
	resp1.Body[0] = 'X'
	resp1.Headers["X-K"] = "mutated"
	resp1.Headers["injected"] = "z"
	resp2, _ := r.Replay(Request{Method: "GET", Path: "/i"})
	if string(resp2.Body) != "orig" {
		t.Fatalf("internal body mutated via returned slice: %q", resp2.Body)
	}
	if resp2.Headers["X-K"] != "v" {
		t.Fatalf("internal header mutated via returned map: %q", resp2.Headers["X-K"])
	}
	if _, ok := resp2.Headers["injected"]; ok {
		t.Fatal("internal header map reflects returned copy's later insert")
	}
}

func TestEmptyBodyAndHeaders(t *testing.T) {
	r := New()
	if err := r.Record(Request{Method: "GET", Path: "/e"}, Response{Status: 204}); err != nil {
		t.Fatal(err)
	}
	resp, err := r.Replay(Request{Method: "GET", Path: "/e"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 204 {
		t.Fatalf("status = %d", resp.Status)
	}
	if len(resp.Body) != 0 {
		t.Fatalf("body len = %d want 0", len(resp.Body))
	}
	if resp.Headers != nil {
		t.Fatalf("headers = %v want nil", resp.Headers)
	}
}

func TestStats(t *testing.T) {
	r := New()
	r.Record(Request{Method: "GET", Path: "/a"}, Response{Status: 200})
	r.Record(Request{Method: "GET", Path: "/a"}, Response{Status: 201})
	r.Record(Request{Method: "GET", Path: "/b"}, Response{Status: 200})
	st := r.Stats()
	if st.Entries != 3 {
		t.Fatalf("entries = %d want 3", st.Entries)
	}
	if st.MatchKeys != 2 {
		t.Fatalf("matchkeys = %d want 2", st.MatchKeys)
	}
	r.Replay(Request{Method: "GET", Path: "/a"})
	r.Replay(Request{Method: "GET", Path: "/a"})
	r.Replay(Request{Method: "GET", Path: "/missing"})
	st = r.Stats()
	if st.Hits != 2 {
		t.Fatalf("hits = %d want 2", st.Hits)
	}
	if st.Misses != 1 {
		t.Fatalf("misses = %d want 1", st.Misses)
	}
}

func TestKeysSorted(t *testing.T) {
	r := New()
	r.Record(Request{Method: "GET", Path: "/z"}, Response{Status: 200})
	r.Record(Request{Method: "GET", Path: "/a"}, Response{Status: 200})
	r.Record(Request{Method: "GET", Path: "/m"}, Response{Status: 200})
	keys := r.Keys()
	if len(keys) != 3 {
		t.Fatalf("len = %d want 3", len(keys))
	}
	for i := 1; i < len(keys); i++ {
		if keys[i-1] > keys[i] {
			t.Fatalf("not sorted: %v", keys)
		}
	}
}

func TestReset(t *testing.T) {
	r := New()
	r.Record(Request{Method: "GET", Path: "/a"}, Response{Status: 200})
	r.Replay(Request{Method: "GET", Path: "/a"})
	r.Replay(Request{Method: "GET", Path: "/missing"})
	r.Reset()
	st := r.Stats()
	if st.Entries != 0 || st.MatchKeys != 0 || st.Hits != 0 || st.Misses != 0 {
		t.Fatalf("after reset: %+v want all zero", st)
	}
	if len(r.Keys()) != 0 {
		t.Fatalf("keys after reset = %v", r.Keys())
	}
	if _, err := r.Replay(Request{Method: "GET", Path: "/a"}); !errors.Is(err, ErrNotMatched) {
		t.Fatalf("replay after reset: err=%v want ErrNotMatched", err)
	}
}
