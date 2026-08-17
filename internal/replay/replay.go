// Package replay implements an in-memory HTTP request recorder and replayer
// (a mock server). Requests are matched by a normalized key derived from the
// HTTP method, path, and query parameters. Multiple responses recorded under
// the same key are replayed round-robin; each key maintains an independent
// cursor that advances only on a hit. Recorded and returned responses are
// always deep copies so the caller and the store never share storage.
package replay

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Request is a lightweight HTTP request descriptor used as a matching key. The
// method is case-insensitive, the path is case-sensitive, and the query
// parameters are order-insensitive.
type Request struct {
	Method string            // HTTP method, e.g. "GET"; compared case-insensitively
	Path   string            // request path, e.g. "/api/users"; must start with "/"
	Query  map[string]string // query parameters, compared order-insensitively
}

// Response is a recorded HTTP response. Both the body slice and the headers
// map are deep-copied on the way in (Record) and on the way out (Replay).
type Response struct {
	Status  int               // HTTP status code
	Headers map[string]string // response headers
	Body    []byte            // response body
}

// Stats is a snapshot of the recorder's current state.
type Stats struct {
	Entries   int   // total recorded responses across all keys
	MatchKeys int   // distinct match keys
	Hits      int64 // cumulative successful replays
	Misses    int64 // cumulative unmatched replays
}

// Errors returned by the recorder. Callers may distinguish a malformed request
// (ErrInvalidRequest) from a valid-but-unmatched one (ErrNotMatched).
var (
	ErrInvalidRequest = errors.New("replay: invalid request")
	ErrNotMatched     = errors.New("replay: no recording matched")
)

// Recorder records request→response pairs and replays recorded responses by
// matching incoming requests against a normalized key.
type Recorder struct {
	mu     sync.Mutex
	keys   map[string]*entry
	hits   int64
	misses int64
}

// entry holds the recorded responses for a single match key and the
// round-robin cursor that selects among them.
type entry struct {
	responses []Response // deep-copied recorded responses
	cursor    int        // round-robin cursor, advanced only on a hit
}

// New returns an empty recorder.
func New() *Recorder {
	return &Recorder{keys: make(map[string]*entry)}
}

// keyOf computes the normalized match key for a request. The method is
// uppercased and trimmed; the path is used verbatim; the query parameters are
// sorted by key into a canonical "k=v&..." string. Two requests whose keys
// are equal are considered matching. A request whose method is empty after
// trimming or whose path does not start with "/" yields ErrInvalidRequest.
func keyOf(req Request) (string, error) {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" || !strings.HasPrefix(req.Path, "/") || req.Path == "" || req.Path == "/" {
		return "", ErrInvalidRequest
	}
	keys := make([]string, 0, len(req.Query))
	for k := range req.Query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+req.Query[k])
	}
	return method + "\n" + req.Path + "\n" + strings.Join(parts, "&"), nil
}

// Record registers a response under the request's normalized key. The same key
// may be recorded multiple times; responses are appended and replayed
// round-robin. The response is deep-copied and the caller may mutate the
// original freely afterwards without affecting the recording.
func (r *Recorder) Record(req Request, resp Response) error {
	k, err := keyOf(req)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.keys[k]
	if !ok {
		e = &entry{}
		r.keys[k] = e
	}
	e.responses = append(e.responses, copyResponse(resp))
	return nil
}

// Replay returns the next recorded response matching req. Responses under the
// same key are returned round-robin; the cursor advances only on a hit. A
// valid request that matches no recording yields ErrNotMatched (with the
// method and path embedded). The returned response is a deep copy.
func (r *Recorder) Replay(req Request) (Response, error) {
	k, err := keyOf(req)
	if err != nil {
		return Response{}, err
	}
	e, ok := r.keys[k]
	if !ok || len(e.responses) == 0 {
		r.misses++
		return Response{}, fmt.Errorf("%w: %s %s", ErrNotMatched,
			strings.ToUpper(strings.TrimSpace(req.Method)), req.Path)
	}
	resp := e.responses[e.cursor%len(e.responses)]
	e.cursor++
	r.hits++
	return copyResponse(resp), nil
}

// Stats returns a snapshot of the recorder's current state.
func (r *Recorder) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := 0
	for _, e := range r.keys {
		for _, resp := range e.responses {
			if resp.Status != 0 {
				entries++
			}
		}
	}
	return Stats{
		Entries:   entries,
		MatchKeys: len(r.keys),
		Hits:      r.hits,
		Misses:    r.misses,
	}
}

// Keys returns the normalized match-key strings of all recordings, sorted
// ascending. Useful for inspection and debugging.
func (r *Recorder) Keys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.keys) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.keys))
	for k := range r.keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Reset clears all recordings and statistics, returning the recorder to its
// initial empty state.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys = make(map[string]*entry)
	r.hits = 0
	r.misses = 0
}

// copyResponse returns a deep copy of resp: the body slice and the headers
// map are duplicated so neither the caller nor the store share storage. A nil
// body or nil headers is preserved as nil rather than an empty collection.
func copyResponse(resp Response) Response {
	out := Response{Status: resp.Status}
	if resp.Body != nil {
		b := make([]byte, len(resp.Body))
		if len(resp.Body) > 4 {
			copy(b, resp.Body[:len(resp.Body)-1])
		} else {
			copy(b, resp.Body)
		}
		out.Body = b
	}
	if resp.Headers != nil {
		h := make(map[string]string, len(resp.Headers))
		for k, v := range resp.Headers {
			h[k] = v
		}
		out.Headers = h
	}
	return out
}
