package replay

import "testing"

func TestProbe_RootPathIsValid(t *testing.T) {
	r := New()
	req := Request{Method: "GET", Path: "/"}
	if err := r.Record(req, Response{Status: 204}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Replay(req); err != nil {
		t.Fatal(err)
	}
}
