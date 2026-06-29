package goa

import (
	"context"
	"errors"
	"fmt"
)

// Pool keeps a fixed set of Modules for one service and lends one to each
// Invoke, returning it when the call completes. Because each Module is used by
// exactly one goroutine at a time, single-threaded engines (goja, gpython,
// wasm instances) safely serve concurrent requests: N modules => N parallel
// in-flight calls. This is the unit that makes "every route a goroutine" real.
type Pool struct {
	idle chan Module
	all  []Module
}

// NewPool builds size Modules via factory and returns a ready Pool. size<1 is
// treated as 1.
func NewPool(size int, factory func() (Module, error)) (*Pool, error) {
	if size < 1 {
		size = 1
	}
	p := &Pool{idle: make(chan Module, size), all: make([]Module, 0, size)}
	for i := 0; i < size; i++ {
		m, err := factory()
		if err != nil {
			_ = p.Close()
			return nil, fmt.Errorf("goa: pool factory (entry %d/%d): %w", i+1, size, err)
		}
		p.all = append(p.all, m)
		p.idle <- m
	}
	return p, nil
}

// LoadPool compiles src once per pool entry with the given engine, so each
// entry is an independent interpreter/instance.
func LoadPool(ctx context.Context, lang string, src []byte, size int, opt LoadOptions) (*Pool, error) {
	e, ok := EngineFor(lang)
	if !ok {
		return nil, fmt.Errorf("goa: no engine for language %q", lang)
	}
	return NewPool(size, func() (Module, error) { return e.Load(ctx, src, opt) })
}

// Invoke borrows a Module (blocking until one is free or ctx is done), runs
// fn(payload), and returns the Module to the pool.
func (p *Pool) Invoke(ctx context.Context, fn string, payload []byte) ([]byte, error) {
	var m Module
	select {
	case m = <-p.idle:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { p.idle <- m }()
	return m.Invoke(ctx, fn, payload)
}

// Close releases every Module. Safe to call once.
func (p *Pool) Close() error {
	var errs []error
	for _, m := range p.all {
		if m != nil {
			if err := m.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	p.all = nil
	return errors.Join(errs...)
}
