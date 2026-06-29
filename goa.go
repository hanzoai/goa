// Package goa is a pure-Go polyglot embedding runtime. It runs Go, JavaScript/
// TypeScript (goja), Python (gpython) and Rust/WASM (wazero) behind ONE
// interface, so every Hanzo cloud service — whatever language it is written in —
// can be compiled into a single static (CGO_ENABLED=0) Go binary and served as
// goroutines. No cgo, no subprocess, no per-language runtime to deploy.
//
// The one calling convention, shared by every engine and language, is
// JSON-in / JSON-out: a handler receives a JSON payload (bytes) and returns a
// JSON result (bytes). That keeps the boundary trivial to port to — a service
// is just a function `(jsonIn) -> jsonOut` in its native language — and lets the
// host (e.g. hanzoai/zip / gofiber) mount any handler as an HTTP/ZAP route via
// the adapters in http.go.
//
// Concurrency: an engine's Module is single-threaded (a goja Runtime, a gpython
// Context, a wasm instance are not safe to share). Pool (pool.go) keeps N
// Modules per service and hands one to each in-flight call, so N goroutines run
// N requests in true parallel — including Python, because gpython is plain Go
// with no shared GIL.
package goa

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Module is one loaded unit of code in some language. It is NOT required to be
// goroutine-safe; Pool provides concurrency. Invoke runs the named exported
// function with a JSON payload and returns a JSON result.
type Module interface {
	// Invoke calls fn(payload) where payload is JSON and the return is JSON.
	Invoke(ctx context.Context, fn string, payload []byte) ([]byte, error)
	// Funcs lists the callable exports discovered in the module.
	Funcs() []string
	// Close releases the module's runtime resources.
	Close() error
}

// Engine compiles source in one language into a Module. Engines MUST be pure Go
// (no cgo) so the host binary stays statically linkable.
type Engine interface {
	// Lang is the canonical language id: "go", "javascript", "python", "wasm".
	Lang() string
	// Aliases are additional ids that resolve to this engine (e.g. "ts","py").
	Aliases() []string
	// Load compiles src into a fresh Module.
	Load(ctx context.Context, src []byte, opt LoadOptions) (Module, error)
}

// LoadOptions configures a single Load.
type LoadOptions struct {
	Name string            // logical module name (diagnostics, require keys)
	Env  map[string]string // exposed as process.env / os.environ to the guest
}

var (
	mu      sync.RWMutex
	engines = map[string]Engine{} // keyed by canonical lang AND every alias
)

// Register makes e resolvable by its Lang() and each of its Aliases(). Engines
// register themselves from init(), so importing the package is enough.
func Register(e Engine) {
	mu.Lock()
	defer mu.Unlock()
	engines[e.Lang()] = e
	for _, a := range e.Aliases() {
		engines[a] = e
	}
}

// EngineFor returns the engine registered for a language id or alias.
func EngineFor(lang string) (Engine, bool) {
	mu.RLock()
	defer mu.RUnlock()
	e, ok := engines[lang]
	return e, ok
}

// Langs returns the sorted set of canonical languages currently registered.
func Langs() []string {
	mu.RLock()
	defer mu.RUnlock()
	seen := map[string]struct{}{}
	for _, e := range engines {
		seen[e.Lang()] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Load compiles src in the given language (id or alias) into a Module.
func Load(ctx context.Context, lang string, src []byte, opt LoadOptions) (Module, error) {
	e, ok := EngineFor(lang)
	if !ok {
		return nil, fmt.Errorf("goa: no engine for language %q (have %v)", lang, Langs())
	}
	return e.Load(ctx, src, opt)
}
