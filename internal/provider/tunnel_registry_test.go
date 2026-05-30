package provider

import (
	"errors"
	"testing"
)

type fakeCloser struct {
	closed int
	err    error
}

func (f *fakeCloser) Close() error {
	f.closed++
	return f.err
}

func TestTunnelRegistryAddTake(t *testing.T) {
	r := NewTunnelRegistry()
	c := &fakeCloser{}

	id, err := r.Add(c)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty id")
	}

	got, ok := r.Take(id)
	if !ok {
		t.Fatal("expected Take to find the tunnel")
	}
	if got != c {
		t.Fatal("Take returned a different closer")
	}

	// Second Take should miss.
	if _, ok := r.Take(id); ok {
		t.Fatal("expected second Take to miss")
	}
}

func TestTunnelRegistryTakeUnknown(t *testing.T) {
	r := NewTunnelRegistry()
	if _, ok := r.Take("nope"); ok {
		t.Fatal("expected miss for unknown id")
	}
}

func TestTunnelRegistryCloseAll(t *testing.T) {
	r := NewTunnelRegistry()
	a := &fakeCloser{}
	b := &fakeCloser{err: errors.New("boom")}
	c := &fakeCloser{}
	for _, x := range []*fakeCloser{a, b, c} {
		if _, err := r.Add(x); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	err := r.CloseAll()
	if err == nil {
		t.Fatal("expected first error to propagate")
	}
	for i, x := range []*fakeCloser{a, b, c} {
		if x.closed != 1 {
			t.Fatalf("closer %d closed %d times, want 1", i, x.closed)
		}
	}

	// Registry should be empty now.
	if err := r.CloseAll(); err != nil {
		t.Fatalf("expected empty CloseAll to return nil, got %v", err)
	}
}

func TestTunnelRegistryUniqueIDs(t *testing.T) {
	r := NewTunnelRegistry()
	seen := map[string]struct{}{}
	for i := 0; i < 100; i++ {
		id, err := r.Add(&fakeCloser{})
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = struct{}{}
	}
}
