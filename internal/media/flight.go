package media

import (
	"context"
	"sync"
)

type flightCall struct {
	done  chan struct{}
	value Preview
	err   error
}

type flightGroup struct {
	mu    sync.Mutex
	calls map[string]*flightCall
}

func (group *flightGroup) Do(ctx context.Context, key string, operation func() (Preview, error)) (Preview, error) {
	group.mu.Lock()
	if group.calls == nil {
		group.calls = make(map[string]*flightCall)
	}
	if existing, ok := group.calls[key]; ok {
		group.mu.Unlock()
		select {
		case <-ctx.Done():
			return Preview{}, ctx.Err()
		case <-existing.done:
			return existing.value, existing.err
		}
	}
	call := &flightCall{done: make(chan struct{})}
	group.calls[key] = call
	group.mu.Unlock()

	call.value, call.err = operation()
	group.mu.Lock()
	delete(group.calls, key)
	close(call.done)
	group.mu.Unlock()
	return call.value, call.err
}
