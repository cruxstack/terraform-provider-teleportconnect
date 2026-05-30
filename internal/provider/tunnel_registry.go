package provider

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// Closer is the minimal interface tracked tunnels must satisfy. Both
// *tunnel.DBTunnel and *tunnel.SSHTunnel implement Close() error.
type Closer interface {
	Close() error
}

// TunnelRegistry tracks active in-process tunnels so that an ephemeral
// resource's Close handler can locate the right listener to shut down.
//
// Lifetimes: a tunnel is registered during ephemeral.Open and removed
// during ephemeral.Close. Registered tunnels are also closed defensively
// at provider shutdown to avoid leaks if Close is somehow skipped.
type TunnelRegistry struct {
	mu      sync.Mutex
	tunnels map[string]Closer
}

func NewTunnelRegistry() *TunnelRegistry {
	return &TunnelRegistry{tunnels: make(map[string]Closer)}
}

// Add registers any tunnel-like Closer and returns an opaque ID used
// during Close to look it up.
func (r *TunnelRegistry) Add(c Closer) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tunnels[id] = c
	return id, nil
}

// Take removes a tunnel from the registry and returns it. If the ID is
// unknown the bool is false.
func (r *TunnelRegistry) Take(id string) (Closer, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tunnels[id]
	if ok {
		delete(r.tunnels, id)
	}
	return t, ok
}

// CloseAll tears down every tracked tunnel. Returns the first error
// encountered, but always attempts to close everything.
func (r *TunnelRegistry) CloseAll() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var firstErr error
	for id, t := range r.tunnels {
		if err := t.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(r.tunnels, id)
	}
	return firstErr
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
