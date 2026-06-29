# goa

Pure-Go polyglot embedding. Run Go, Python, TypeScript/JavaScript, and
Rust/WASM services in one process — as goroutines, no cgo, no subprocesses, no
GIL — so a fleet of services collapses into a single static binary
(`CGO_ENABLED=0`) that scales horizontally by replication.

```
import "github.com/hanzoai/goa"
```

## Why

hanzoai/cloud is one Go binary: subsystems register and mount onto a
[zip](https://github.com/hanzoai/zip) (gofiber) app. Most of our backends are
*not* Go — Langflow is Python, the migrated explorer services are Rust,
assorted tools are TypeScript. The choice was a swarm of sidecar containers, or
embed the runtimes. goa embeds them, in pure Go, preserving the static build.

## Model

One contract, every language: **JSON in, JSON out.**

```go
type Module interface {
    Invoke(ctx context.Context, fn string, payload []byte) ([]byte, error)
    Funcs() []string
    Close() error
}

type Engine interface {
    Lang() string
    Aliases() []string
    Load(ctx context.Context, src []byte, opt LoadOptions) (Module, error)
}
```

Four engines register themselves, all pure-Go:

| Lang             | Engine                              | Notes                                            |
|------------------|-------------------------------------|--------------------------------------------------|
| Go               | native                              | `map[string]HandlerFunc`, zero overhead          |
| JavaScript / TS  | [goja](https://github.com/dop251/goja) + [esbuild](https://github.com/evanw/esbuild) | TS transpiled to ES2020 CommonJS |
| Python           | [gpython](https://github.com/go-python/gpython) | no cgo, no GIL → true goroutine parallelism |
| Rust / WASM      | [wazero](https://github.com/tetratelabs/wazero) | wasm32 + WASI, trivial string ABI    |

Engines that run single-threaded code (goja, gpython, a wasm instance) are made
concurrent by a **Pool**: N independent interpreters, one per in-flight request,
so every HTTP route is still just a goroutine.

## Use

### Embed source directly

```go
m, _ := goa.Load(ctx, "python", []byte(`
import json
def greet(p):
    return json.dumps({"hello": json.loads(p)["name"]})
`), goa.LoadOptions{Name: "greeter"})

out, _ := m.Invoke(ctx, "greet", []byte(`{"name":"ada"}`))
// {"hello":"ada"}
```

### Mount Go natively (zero overhead)

```go
m := goa.NewNativeModule(map[string]goa.HandlerFunc{
    "greet": func(ctx context.Context, p []byte) ([]byte, error) { return p, nil },
})
```

### Drop-in services: a manifest + a source file

`hello-py.manifest.json`
```json
{
  "name": "hello-py",
  "lang": "python",
  "source": "hello.py",
  "pool": 8,
  "prefix": "/v1/hello-py",
  "routes": [{ "method": "POST", "path": "/greet", "func": "greet" }]
}
```

```go
services, _ := goa.LoadDir(ctx, "services")  // every *.manifest.json
mux := http.NewServeMux()
for _, s := range services {
    s.Mount(mux)
}
```

In hanzoai/cloud the same `Service` mounts onto the gofiber app via
`zip.AdaptNetHTTP(svc.Handler())` — goa stays host-agnostic.

## Python stdlib

gpython is "batteries not included." goa ships **Go-backed** implementations of
the modules our backends actually use, registered under their real names — so
the Python keeps `import json` and runs at Go speed (the modules that are
C-accelerated in CPython are Go-native here). `json` ships today; `re`,
`hashlib`, `base64`, `datetime`, `uuid` extend the same `pyToGo`/`goToPy`
pattern in `stdlib_gpython.go`.

## Two tiers

goa is **Tier 1**: in-process logic, pure-Go, for handlers and transforms.
Anything that needs a real OS process — a long-running server, a GPU workload,
heavy native Python (NumPy/Torch), an unported C extension — is **Tier 2**:
run it out-of-process and front it with a native proxy module. Tier 1 is the
default; reach for Tier 2 only when the workload genuinely needs an OS boundary.

## Rust / WASM ABI

A guest module exports:

```
alloc(len: i32) -> i32              // buffer pointer for the payload
dealloc(ptr: i32, len: i32)         // optional
<fn>(ptr: i32, len: i32) -> i64     // returns (resultPtr << 32 | resultLen)
```

The host writes JSON at `alloc()`'s pointer, calls `<fn>`, reads JSON back. A
`#[goa::handler]` proc-macro generates this boilerplate on the Rust side.

## License

Apache-2.0
