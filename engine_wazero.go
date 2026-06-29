package goa

import (
	"context"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// wazeroEngine runs WebAssembly — the in-process path for Rust (and C, Zig,
// TinyGo). Rust compiles cleanly to wasm32, so a Rust service becomes a .wasm
// loaded here, sandboxed and pure-Go (no cgo). Like the others it serves the
// JSON-in/JSON-out contract through a tiny string ABI the guest exports:
//
//	alloc(len: i32) -> i32                 // return a buffer pointer
//	dealloc(ptr: i32, len: i32)            // optional, freed if present
//	<fn>(ptr: i32, len: i32) -> i64        // returns (resultPtr<<32 | resultLen)
//
// The host writes the JSON payload at alloc()'s pointer, calls <fn>, and reads
// the JSON result back from linear memory. (A #[goa::handler] proc-macro on the
// Rust side will generate this boilerplate; the ABI is intentionally trivial.)
type wazeroEngine struct{}

func init() { Register(wazeroEngine{}) }

func (wazeroEngine) Lang() string      { return "wasm" }
func (wazeroEngine) Aliases() []string { return []string{"rust", "wat", "wasm32"} }

func (wazeroEngine) Load(ctx context.Context, src []byte, opt LoadOptions) (Module, error) {
	rt := wazero.NewRuntime(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, rt) // many Rust/wasm targets need WASI
	mod, err := rt.InstantiateWithConfig(ctx, src,
		wazero.NewModuleConfig().WithName(opt.Name))
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("goa/wasm: load %q: %w", opt.Name, err)
	}
	if mod.ExportedFunction("alloc") == nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("goa/wasm: %q must export alloc(i32)->i32 for the string ABI", opt.Name)
	}
	return &wasmModule{rt: rt, mod: mod}, nil
}

type wasmModule struct {
	mu  sync.Mutex // a wasm instance is single-threaded; Pool gives concurrency
	rt  wazero.Runtime
	mod api.Module
}

func (m *wasmModule) Invoke(ctx context.Context, fn string, payload []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	alloc := m.mod.ExportedFunction("alloc")
	handler := m.mod.ExportedFunction(fn)
	if handler == nil {
		return nil, fmt.Errorf("goa/wasm: no export %q", fn)
	}
	mem := m.mod.Memory()

	in, err := alloc.Call(ctx, uint64(len(payload)))
	if err != nil {
		return nil, fmt.Errorf("goa/wasm: alloc: %w", err)
	}
	inPtr := uint32(in[0])
	if !mem.Write(inPtr, payload) {
		return nil, fmt.Errorf("goa/wasm: write payload out of range")
	}

	out, err := handler.Call(ctx, uint64(inPtr), uint64(len(payload)))
	if err != nil {
		return nil, fmt.Errorf("goa/wasm: %s: %w", fn, err)
	}
	if dealloc := m.mod.ExportedFunction("dealloc"); dealloc != nil {
		_, _ = dealloc.Call(ctx, uint64(inPtr), uint64(len(payload)))
	}
	if len(out) == 0 {
		return []byte("null"), nil
	}
	packed := out[0]
	resPtr := uint32(packed >> 32)
	resLen := uint32(packed)
	data, ok := mem.Read(resPtr, resLen)
	if !ok {
		return nil, fmt.Errorf("goa/wasm: result out of range (ptr=%d len=%d)", resPtr, resLen)
	}
	result := make([]byte, resLen) // copy out of guest memory before reuse
	copy(result, data)
	if dealloc := m.mod.ExportedFunction("dealloc"); dealloc != nil {
		_, _ = dealloc.Call(ctx, uint64(resPtr), uint64(resLen))
	}
	return result, nil
}

func (m *wasmModule) Funcs() []string {
	var out []string
	for name, def := range m.mod.ExportedFunctionDefinitions() {
		switch name {
		case "alloc", "dealloc", "_start", "memory":
			continue
		}
		_ = def
		out = append(out, name)
	}
	return out
}

func (m *wasmModule) Close() error { return m.rt.Close(context.Background()) }
