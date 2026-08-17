// Package selfcheck runs an end-to-end verification of the replay recorder. It
// is invoked by the --smoke-test flag and exits the process on completion.
package selfcheck

import (
	"errors"
	"fmt"

	"task049-httpreplay/internal/replay"
)

// Run exercises the record-and-replay recorder across isolated scenarios,
// returning nil if every behavior matches the specification.
func Run() error {
	scenarios := []struct {
		name string
		fn   func() error
	}{
		{"录制与重放", scenarioRecordReplay},
		{"查询参数顺序无关", scenarioQueryOrderInsensitive},
		{"方法大小写不敏感", scenarioMethodCaseInsensitive},
		{"路径大小写敏感", scenarioPathCaseSensitive},
		{"轮询回绕", scenarioRoundRobin},
		{"游标相互独立", scenarioCursorIndependent},
		{"追加录制后游标正确映射", scenarioAppendAfterCursor},
		{"未匹配返回结构化错误", scenarioNotMatched},
		{"命中与未命中计数", scenarioHitMissCounters},
		{"统计条目与键数", scenarioStats},
		{"列举排序", scenarioKeysSorted},
		{"录制侧隔离", scenarioRecordSideIsolation},
		{"重放侧隔离", scenarioReplaySideIsolation},
		{"空响应体与空响应头合法", scenarioEmptyBodyHeaders},
		{"请求不合法与未匹配区分", scenarioErrorSemantics},
		{"重置回到初始空状态", scenarioReset},
	}
	for _, sc := range scenarios {
		if err := sc.fn(); err != nil {
			return fmt.Errorf("%s: %w", sc.name, err)
		}
	}
	return nil
}

func scenarioRecordReplay() error {
	r := replay.New()
	if err := r.Record(replay.Request{Method: "GET", Path: "/users"},
		replay.Response{Status: 200, Headers: map[string]string{"Content-Type": "text/plain"}, Body: []byte("ok")}); err != nil {
		return fmt.Errorf("record: %w", err)
	}
	resp, err := r.Replay(replay.Request{Method: "GET", Path: "/users"})
	if err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	if resp.Status != 200 {
		return fmt.Errorf("status = %d want 200", resp.Status)
	}
	if string(resp.Body) != "ok" {
		return fmt.Errorf("body = %q want ok", resp.Body)
	}
	if resp.Headers["Content-Type"] != "text/plain" {
		return fmt.Errorf("header = %q", resp.Headers["Content-Type"])
	}
	return nil
}

func scenarioQueryOrderInsensitive() error {
	r := replay.New()
	rec := replay.Request{Method: "GET", Path: "/search", Query: map[string]string{"a": "1", "b": "2"}}
	if err := r.Record(rec, replay.Response{Status: 200, Body: []byte("hit")}); err != nil {
		return err
	}
	// Repeatedly replay with a fresh map whose construction order differs;
	// order-insensitive matching must always hit.
	for i := 0; i < 5; i++ {
		resp, err := r.Replay(replay.Request{Method: "GET", Path: "/search", Query: map[string]string{"b": "2", "a": "1"}})
		if err != nil {
			return fmt.Errorf("replay #%d: %w", i, err)
		}
		if string(resp.Body) != "hit" {
			return fmt.Errorf("replay #%d body = %q", i, resp.Body)
		}
	}
	// A different set of query params must NOT match.
	if _, err := r.Replay(replay.Request{Method: "GET", Path: "/search", Query: map[string]string{"a": "1"}}); !errors.Is(err, replay.ErrNotMatched) {
		return fmt.Errorf("partial query: err=%v want ErrNotMatched", err)
	}
	return nil
}

func scenarioMethodCaseInsensitive() error {
	r := replay.New()
	if err := r.Record(replay.Request{Method: "post", Path: "/items"},
		replay.Response{Status: 201, Body: []byte("created")}); err != nil {
		return err
	}
	for _, m := range []string{"POST", "Post", "post"} {
		resp, err := r.Replay(replay.Request{Method: m, Path: "/items"})
		if err != nil {
			return fmt.Errorf("replay %q: %w", m, err)
		}
		if resp.Status != 201 {
			return fmt.Errorf("replay %q status = %d want 201", m, resp.Status)
		}
	}
	// A different method must NOT match.
	if _, err := r.Replay(replay.Request{Method: "GET", Path: "/items"}); !errors.Is(err, replay.ErrNotMatched) {
		return fmt.Errorf("GET vs POST: err=%v want ErrNotMatched", err)
	}
	return nil
}

func scenarioPathCaseSensitive() error {
	r := replay.New()
	if err := r.Record(replay.Request{Method: "GET", Path: "/Users"},
		replay.Response{Status: 200, Body: []byte("u")}); err != nil {
		return err
	}
	resp, err := r.Replay(replay.Request{Method: "GET", Path: "/Users"})
	if err != nil {
		return fmt.Errorf("exact path: %w", err)
	}
	if string(resp.Body) != "u" {
		return fmt.Errorf("body = %q", resp.Body)
	}
	if _, err := r.Replay(replay.Request{Method: "GET", Path: "/users"}); !errors.Is(err, replay.ErrNotMatched) {
		return fmt.Errorf("case-differing path: err=%v want ErrNotMatched", err)
	}
	return nil
}

func scenarioRoundRobin() error {
	r := replay.New()
	req := replay.Request{Method: "GET", Path: "/r"}
	for _, s := range []int{10, 11, 12} {
		if err := r.Record(req, replay.Response{Status: s}); err != nil {
			return err
		}
	}
	want := []int{10, 11, 12, 10, 11, 12}
	for i, w := range want {
		resp, err := r.Replay(req)
		if err != nil {
			return fmt.Errorf("replay #%d: %w", i, err)
		}
		if resp.Status != w {
			return fmt.Errorf("replay #%d status = %d want %d", i, resp.Status, w)
		}
	}
	st := r.Stats()
	if st.Hits != int64(len(want)) {
		return fmt.Errorf("hits = %d want %d", st.Hits, len(want))
	}
	if st.Misses != 0 {
		return fmt.Errorf("misses = %d want 0", st.Misses)
	}
	return nil
}

func scenarioCursorIndependent() error {
	r := replay.New()
	reqA := replay.Request{Method: "GET", Path: "/a"}
	reqB := replay.Request{Method: "GET", Path: "/b"}
	for _, s := range []int{100, 101} {
		if err := r.Record(reqA, replay.Response{Status: s}); err != nil {
			return err
		}
	}
	for _, s := range []int{200, 201} {
		if err := r.Record(reqB, replay.Response{Status: s}); err != nil {
			return err
		}
	}
	a1, _ := r.Replay(reqA) // A: 100
	b1, _ := r.Replay(reqB) // B: 200
	a2, _ := r.Replay(reqA) // A: 101
	b2, _ := r.Replay(reqB) // B: 201
	if a1.Status != 100 || a2.Status != 101 {
		return fmt.Errorf("A sequence = %d,%d want 100,101", a1.Status, a2.Status)
	}
	if b1.Status != 200 || b2.Status != 201 {
		return fmt.Errorf("B sequence = %d,%d want 200,201", b1.Status, b2.Status)
	}
	return nil
}

func scenarioAppendAfterCursor() error {
	r := replay.New()
	req := replay.Request{Method: "GET", Path: "/x"}
	// Two recordings; advance cursor twice so the next hit would wrap.
	if err := r.Record(req, replay.Response{Status: 1}); err != nil {
		return err
	}
	if err := r.Record(req, replay.Response{Status: 2}); err != nil {
		return err
	}
	r.Replay(req) // cursor 0->1, returned 1
	r.Replay(req) // cursor 1->2, returned 2
	// Append a third response. The cursor is now 2; modulo 3 maps to index 2
	// (the newly appended entry). Subsequent replays must continue the
	// round-robin over the expanded set without skipping or repeating out of
	// order.
	if err := r.Record(req, replay.Response{Status: 3}); err != nil {
		return err
	}
	resp, err := r.Replay(req) // cursor 2->3, returns responses[2%3]=3
	if err != nil {
		return err
	}
	if resp.Status != 3 {
		return fmt.Errorf("post-append replay status = %d want 3", resp.Status)
	}
	resp2, err := r.Replay(req) // cursor 3->4, returns responses[3%3]=1
	if err != nil {
		return err
	}
	if resp2.Status != 1 {
		return fmt.Errorf("wrap replay status = %d want 1", resp2.Status)
	}
	return nil
}

func scenarioNotMatched() error {
	r := replay.New()
	r.Record(replay.Request{Method: "GET", Path: "/x"}, replay.Response{Status: 200})
	_, err := r.Replay(replay.Request{Method: "GET", Path: "/y"})
	if !errors.Is(err, replay.ErrNotMatched) {
		return fmt.Errorf("err=%v want ErrNotMatched", err)
	}
	// The error must carry the request method and path for diagnostics.
	if !contains(err.Error(), "/y") {
		return fmt.Errorf("err %q missing path /y", err.Error())
	}
	if !contains(err.Error(), "GET") {
		return fmt.Errorf("err %q missing method GET", err.Error())
	}
	// A miss must not advance any cursor or be counted as a hit.
	st := r.Stats()
	if st.Hits != 0 {
		return fmt.Errorf("hits = %d want 0", st.Hits)
	}
	if st.Misses != 1 {
		return fmt.Errorf("misses = %d want 1", st.Misses)
	}
	return nil
}

func scenarioHitMissCounters() error {
	r := replay.New()
	r.Record(replay.Request{Method: "GET", Path: "/a"}, replay.Response{Status: 200})
	r.Replay(replay.Request{Method: "GET", Path: "/a"}) // hit
	r.Replay(replay.Request{Method: "GET", Path: "/a"}) // hit
	r.Replay(replay.Request{Method: "GET", Path: "/b"}) // miss
	r.Replay(replay.Request{Method: "GET", Path: "/c"}) // miss
	st := r.Stats()
	if st.Hits != 2 {
		return fmt.Errorf("hits = %d want 2", st.Hits)
	}
	if st.Misses != 2 {
		return fmt.Errorf("misses = %d want 2", st.Misses)
	}
	return nil
}

func scenarioStats() error {
	r := replay.New()
	r.Record(replay.Request{Method: "GET", Path: "/a"}, replay.Response{Status: 200})
	r.Record(replay.Request{Method: "GET", Path: "/a"}, replay.Response{Status: 201})
	r.Record(replay.Request{Method: "GET", Path: "/b"}, replay.Response{Status: 200})
	r.Record(replay.Request{Method: "POST", Path: "/a"}, replay.Response{Status: 202})
	st := r.Stats()
	if st.Entries != 4 {
		return fmt.Errorf("entries = %d want 4", st.Entries)
	}
	// Three distinct keys: GET/a, GET/b, POST/a (method participates in key).
	if st.MatchKeys != 3 {
		return fmt.Errorf("matchkeys = %d want 3", st.MatchKeys)
	}
	return nil
}

func scenarioKeysSorted() error {
	r := replay.New()
	r.Record(replay.Request{Method: "GET", Path: "/z"}, replay.Response{Status: 200})
	r.Record(replay.Request{Method: "GET", Path: "/a"}, replay.Response{Status: 200})
	r.Record(replay.Request{Method: "POST", Path: "/a"}, replay.Response{Status: 200})
	r.Record(replay.Request{Method: "GET", Path: "/a"}, replay.Response{Status: 201}) // dup key, must not re-add
	keys := r.Keys()
	if len(keys) != 3 {
		return fmt.Errorf("len = %d want 3", len(keys))
	}
	for i := 1; i < len(keys); i++ {
		if keys[i-1] > keys[i] {
			return fmt.Errorf("not sorted: %v", keys)
		}
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			return fmt.Errorf("duplicate key: %s", k)
		}
		seen[k] = true
	}
	return nil
}

func scenarioRecordSideIsolation() error {
	r := replay.New()
	body := []byte("orig")
	hdrs := map[string]string{"X-K": "v"}
	if err := r.Record(replay.Request{Method: "GET", Path: "/i"},
		replay.Response{Status: 200, Body: body, Headers: hdrs}); err != nil {
		return err
	}
	// Mutate the caller's slice and map after recording.
	body[0] = 'X'
	hdrs["X-K"] = "mutated"
	hdrs["injected"] = "z"
	resp, err := r.Replay(replay.Request{Method: "GET", Path: "/i"})
	if err != nil {
		return err
	}
	if string(resp.Body) != "orig" {
		return fmt.Errorf("stored body mutated via caller slice: %q", resp.Body)
	}
	if resp.Headers["X-K"] != "v" {
		return fmt.Errorf("stored header mutated via caller map: %q", resp.Headers["X-K"])
	}
	if _, ok := resp.Headers["injected"]; ok {
		return fmt.Errorf("stored header map reflects caller's later insert")
	}
	return nil
}

func scenarioReplaySideIsolation() error {
	r := replay.New()
	r.Record(replay.Request{Method: "GET", Path: "/i"},
		replay.Response{Status: 200, Body: []byte("orig"), Headers: map[string]string{"X-K": "v"}})
	resp1, err := r.Replay(replay.Request{Method: "GET", Path: "/i"})
	if err != nil {
		return err
	}
	resp1.Body[0] = 'X'
	resp1.Headers["X-K"] = "mutated"
	resp1.Headers["injected"] = "z"
	resp2, err := r.Replay(replay.Request{Method: "GET", Path: "/i"})
	if err != nil {
		return err
	}
	if string(resp2.Body) != "orig" {
		return fmt.Errorf("internal body mutated via returned slice: %q", resp2.Body)
	}
	if resp2.Headers["X-K"] != "v" {
		return fmt.Errorf("internal header mutated via returned map: %q", resp2.Headers["X-K"])
	}
	if _, ok := resp2.Headers["injected"]; ok {
		return fmt.Errorf("internal header map reflects returned copy's later insert")
	}
	return nil
}

func scenarioEmptyBodyHeaders() error {
	r := replay.New()
	// Empty body, nil headers: a legal response.
	if err := r.Record(replay.Request{Method: "GET", Path: "/e"}, replay.Response{Status: 204}); err != nil {
		return err
	}
	resp, err := r.Replay(replay.Request{Method: "GET", Path: "/e"})
	if err != nil {
		return err
	}
	if resp.Status != 204 {
		return fmt.Errorf("status = %d want 204", resp.Status)
	}
	if len(resp.Body) != 0 {
		return fmt.Errorf("body len = %d want 0", len(resp.Body))
	}
	if resp.Headers != nil {
		return fmt.Errorf("headers = %v want nil", resp.Headers)
	}
	// Empty body must not be confused with a miss.
	if _, err := r.Replay(replay.Request{Method: "GET", Path: "/e"}); err != nil {
		return fmt.Errorf("second replay of empty-body response: %w", err)
	}
	return nil
}

func scenarioErrorSemantics() error {
	r := replay.New()
	r.Record(replay.Request{Method: "GET", Path: "/x"}, replay.Response{Status: 200})
	// Invalid requests: path not starting with "/", empty path, blank method.
	for _, bad := range []replay.Request{
		{Method: "GET", Path: "no-slash"},
		{Method: "GET", Path: ""},
		{Method: "  ", Path: "/x"},
		{Method: "", Path: "/x"},
	} {
		if err := r.Record(bad, replay.Response{Status: 200}); !errors.Is(err, replay.ErrInvalidRequest) {
			return fmt.Errorf("record %+v: err=%v want ErrInvalidRequest", bad, err)
		}
		if _, err := r.Replay(bad); !errors.Is(err, replay.ErrInvalidRequest) {
			return fmt.Errorf("replay %+v: err=%v want ErrInvalidRequest", bad, err)
		}
	}
	// A valid-but-absent request yields NotMatched, never InvalidRequest.
	if _, err := r.Replay(replay.Request{Method: "GET", Path: "/absent"}); !errors.Is(err, replay.ErrNotMatched) {
		return fmt.Errorf("absent: err=%v want ErrNotMatched", err)
	}
	// The two error sentinels are distinct.
	if errors.Is(replay.ErrInvalidRequest, replay.ErrNotMatched) || errors.Is(replay.ErrNotMatched, replay.ErrInvalidRequest) {
		return fmt.Errorf("error sentinels are not distinct")
	}
	return nil
}

func scenarioReset() error {
	r := replay.New()
	r.Record(replay.Request{Method: "GET", Path: "/a"}, replay.Response{Status: 200})
	r.Replay(replay.Request{Method: "GET", Path: "/a"})
	r.Replay(replay.Request{Method: "GET", Path: "/missing"})
	r.Reset()
	st := r.Stats()
	if st.Entries != 0 || st.MatchKeys != 0 || st.Hits != 0 || st.Misses != 0 {
		return fmt.Errorf("after reset: %+v want all zero", st)
	}
	if len(r.Keys()) != 0 {
		return fmt.Errorf("keys after reset = %v", r.Keys())
	}
	if _, err := r.Replay(replay.Request{Method: "GET", Path: "/a"}); !errors.Is(err, replay.ErrNotMatched) {
		return fmt.Errorf("replay after reset: err=%v want ErrNotMatched", err)
	}
	return nil
}

// contains is a tiny local helper to avoid importing strings (keeps the
// selfcheck import list focused on the package under test).
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
