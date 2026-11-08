package server

import "sync"

// streamRegistry tracks the open streams of each token subject, so a deny
// can end every connection a subject has open. Anonymous streams aren't in
// it.
type streamRegistry struct {
	mu    sync.Mutex
	bySub map[string]map[*CollabBidiStream]struct{}
}

func newStreamRegistry() *streamRegistry {
	return &streamRegistry{bySub: make(map[string]map[*CollabBidiStream]struct{})}
}

func (r *streamRegistry) add(sub string, s *CollabBidiStream) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bySub[sub] == nil {
		r.bySub[sub] = make(map[*CollabBidiStream]struct{})
	}
	r.bySub[sub][s] = struct{}{}
}

func (r *streamRegistry) remove(sub string, s *CollabBidiStream) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.bySub[sub], s)
	if len(r.bySub[sub]) == 0 {
		delete(r.bySub, sub)
	}
}

func (r *streamRegistry) count(sub string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bySub[sub])
}

// endSubject ends each of sub's open streams with SessionEnded{denied}. It
// works on a snapshot, since ending a stream removes it from the registry.
// Each stream ends in its own goroutine: one whose client stopped reading
// can block until the write timeout, and the deny call shouldn't wait on it.
func (r *streamRegistry) endSubject(sub string) {
	r.mu.Lock()
	streams := make([]*CollabBidiStream, 0, len(r.bySub[sub]))
	for s := range r.bySub[sub] {
		streams = append(streams, s)
	}
	r.mu.Unlock()
	for _, s := range streams {
		go s.endSession("denied")
	}
}
