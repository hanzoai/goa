package goa

import (
	"context"
	"fmt"
	"sort"
)

// HandlerFunc is a native Go handler in the canonical JSON-in/JSON-out shape.
type HandlerFunc func(ctx context.Context, payload []byte) ([]byte, error)

// NativeModule mounts plain Go functions through the same Module interface as
// the interpreted engines — zero overhead, full type safety. This is how Go
// services join the unified binary: register their handlers and they sit beside
// the Python/JS/Rust ones behind the identical routing surface.
//
// NativeModule is goroutine-safe (the underlying Go funcs are expected to be),
// so it needs no Pool — though you may still pool it uniformly if you like.
type NativeModule struct {
	funcs map[string]HandlerFunc
}

// NewNativeModule wraps a set of named Go handlers as a Module.
func NewNativeModule(funcs map[string]HandlerFunc) *NativeModule {
	cp := make(map[string]HandlerFunc, len(funcs))
	for k, v := range funcs {
		cp[k] = v
	}
	return &NativeModule{funcs: cp}
}

func (m *NativeModule) Invoke(ctx context.Context, fn string, payload []byte) ([]byte, error) {
	h, ok := m.funcs[fn]
	if !ok {
		return nil, fmt.Errorf("go: no handler %q", fn)
	}
	return h(ctx, payload)
}

func (m *NativeModule) Funcs() []string {
	out := make([]string, 0, len(m.funcs))
	for k := range m.funcs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (m *NativeModule) Close() error { return nil }
