package goa

import (
	"context"
	"fmt"
	"sync"

	esbuild "github.com/evanw/esbuild/pkg/api"
	"github.com/dop251/goja"
)

// gojaEngine runs JavaScript and TypeScript. TS/JSX is transpiled to JS with
// esbuild first (pure Go), then executed on goja (pure Go). The module is
// loaded as CommonJS: handlers are read from module.exports or the global
// scope. A handler is `function name(jsonString) { return jsonString }`.
type gojaEngine struct{}

func init() { Register(gojaEngine{}) }

func (gojaEngine) Lang() string      { return "javascript" }
func (gojaEngine) Aliases() []string { return []string{"js", "ts", "typescript", "jsx", "tsx"} }

func (gojaEngine) Load(_ context.Context, src []byte, opt LoadOptions) (Module, error) {
	res := esbuild.Transform(string(src), esbuild.TransformOptions{
		Loader: esbuild.LoaderTS, // TS is a superset of JS, so this also accepts plain JS
		Format: esbuild.FormatCommonJS,
		Target: esbuild.ES2020,
	})
	if len(res.Errors) > 0 {
		return nil, fmt.Errorf("goa/js: esbuild: %s", res.Errors[0].Text)
	}

	vm := goja.New()
	// Minimal CommonJS surface + injected env.
	module := vm.NewObject()
	exports := vm.NewObject()
	_ = module.Set("exports", exports)
	_ = vm.Set("module", module)
	_ = vm.Set("exports", exports)
	procEnv := vm.NewObject()
	for k, v := range opt.Env {
		_ = procEnv.Set(k, v)
	}
	proc := vm.NewObject()
	_ = proc.Set("env", procEnv)
	_ = vm.Set("process", proc)

	if _, err := vm.RunString(string(res.Code)); err != nil {
		return nil, fmt.Errorf("goa/js: load %q: %w", opt.Name, err)
	}
	exp := module.Get("exports")
	expObj, _ := exp.(*goja.Object) // module.exports may have been reassigned
	if expObj == nil {
		expObj = exports
	}
	return &gojaModule{vm: vm, exports: expObj}, nil
}

type gojaModule struct {
	mu      sync.Mutex
	vm      *goja.Runtime
	exports *goja.Object
}

func (m *gojaModule) resolve(fn string) (goja.Callable, bool) {
	if v := m.exports.Get(fn); v != nil {
		if cb, ok := goja.AssertFunction(v); ok {
			return cb, true
		}
	}
	if v := m.vm.Get(fn); v != nil {
		if cb, ok := goja.AssertFunction(v); ok {
			return cb, true
		}
	}
	return nil, false
}

func (m *gojaModule) Invoke(_ context.Context, fn string, payload []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cb, ok := m.resolve(fn)
	if !ok {
		return nil, fmt.Errorf("goa/js: no exported function %q", fn)
	}
	res, err := cb(goja.Undefined(), m.vm.ToValue(string(payload)))
	if err != nil {
		return nil, fmt.Errorf("goa/js: %s: %w", fn, err)
	}
	if res == nil || goja.IsUndefined(res) || goja.IsNull(res) {
		return []byte("null"), nil
	}
	return []byte(res.String()), nil
}

func (m *gojaModule) Funcs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, k := range m.exports.Keys() {
		if _, ok := goja.AssertFunction(m.exports.Get(k)); ok {
			out = append(out, k)
		}
	}
	return out
}

func (m *gojaModule) Close() error { return nil }
