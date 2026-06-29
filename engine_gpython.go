package goa

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/go-python/gpython/py"
	_ "github.com/go-python/gpython/compile" // wires py.Compile
	_ "github.com/go-python/gpython/stdlib"  // wires py.NewContext + built-in stdlib
)

// gpythonEngine runs Python on gpython — a pure-Go interpreter. Because it is
// plain Go with no shared CPython GIL, each loaded module owns an independent
// py.Context and N modules in a Pool run N Python calls in true parallel, as
// ordinary goroutines, inside the CGO_ENABLED=0 static binary. A handler is
// `def name(payload: str) -> str` returning a JSON string.
//
// gpython is "batteries not included"; goa augments it with Go-backed stdlib
// shims (see stdlib_gpython.go) so real backends keep working in Python while
// the hot modules (json, re, hashlib, base64) run at Go speed.
type gpythonEngine struct{}

func init() {
	registerGoBackedStdlib() // must run before any context is created
	Register(gpythonEngine{})
}

func (gpythonEngine) Lang() string      { return "python" }
func (gpythonEngine) Aliases() []string { return []string{"py", "python3"} }

func (gpythonEngine) Load(_ context.Context, src []byte, opt LoadOptions) (Module, error) {
	pyctx := py.NewContext(py.DefaultContextOpts())
	// ModuleInit compiles src in ExecMode (whole module) and runs it, so every
	// top-level statement executes and all defs land in Globals. (py.RunSrc uses
	// SingleMode — interactive, first-statement-only — which silently drops
	// handlers defined after the first statement.)
	mod, err := pyctx.ModuleInit(&py.ModuleImpl{
		Info:    py.ModuleInfo{Name: opt.Name, FileDesc: opt.Name + ".py"},
		CodeSrc: string(src),
	})
	if err != nil {
		_ = pyctx.Close()
		return nil, fmt.Errorf("goa/py: load %q: %w", opt.Name, err)
	}
	return &gpyModule{ctx: pyctx, globals: mod.Globals}, nil
}

type gpyModule struct {
	mu      sync.Mutex // a py.Context is single-threaded; Pool gives concurrency
	ctx     py.Context
	globals py.StringDict
}

func (m *gpyModule) Invoke(_ context.Context, fn string, payload []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.globals[fn]
	if !ok {
		return nil, fmt.Errorf("goa/py: no function %q", fn)
	}
	res, err := py.Call(obj, py.Tuple{py.String(payload)}, nil)
	if err != nil {
		return nil, fmt.Errorf("goa/py: %s: %w", fn, err)
	}
	switch v := res.(type) {
	case py.String:
		return []byte(v), nil
	case py.Bytes:
		return []byte(v), nil
	case py.NoneType:
		return []byte("null"), nil
	default:
		return nil, fmt.Errorf("goa/py: %s must return a JSON str/bytes, got %s", fn, res.Type().Name)
	}
}

func (m *gpyModule) Funcs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for name, obj := range m.globals {
		if _, ok := obj.(*py.Function); ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (m *gpyModule) Close() error { return m.ctx.Close() }
