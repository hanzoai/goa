package goa

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// echoWasm is the goa Rust guest (examples/echo-rust) compiled to wasm32. Build:
//
//	cd examples/echo-rust && cargo build --release --target wasm32-unknown-unknown
//	cp target/wasm32-unknown-unknown/release/echo_rust.wasm echo.wasm
//
//go:embed examples/echo-rust/echo.wasm
var echoWasm []byte

func mustInvoke(t *testing.T, m Module, fn, payload string) string {
	t.Helper()
	out, err := m.Invoke(context.Background(), fn, []byte(payload))
	if err != nil {
		t.Fatalf("Invoke(%s): %v", fn, err)
	}
	return string(out)
}

func TestNative(t *testing.T) {
	m := NewNativeModule(map[string]HandlerFunc{
		"echo": func(_ context.Context, p []byte) ([]byte, error) { return p, nil },
	})
	if got := mustInvoke(t, m, "echo", `{"a":1}`); got != `{"a":1}` {
		t.Fatalf("native echo = %q", got)
	}
}

func TestGoja(t *testing.T) {
	src := `function greet(p){ var d = JSON.parse(p); return JSON.stringify({hello:d.name, lang:"javascript"}); }`
	m, err := Load(context.Background(), "js", []byte(src), LoadOptions{Name: "g"})
	if err != nil {
		t.Fatal(err)
	}
	got := mustInvoke(t, m, "greet", `{"name":"world"}`)
	assertJSONEq(t, got, `{"hello":"world","lang":"javascript"}`)
}

func TestGojaTypeScript(t *testing.T) {
	// TS-only syntax (type annotations) must transpile via esbuild.
	src := `export function add(p: string): string { const d: {x:number,y:number} = JSON.parse(p); return JSON.stringify({sum: d.x + d.y}); }`
	m, err := Load(context.Background(), "ts", []byte(src), LoadOptions{Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEq(t, mustInvoke(t, m, "add", `{"x":2,"y":3}`), `{"sum":5}`)
}

func TestGpython(t *testing.T) {
	src := "import json\n" +
		"def greet(payload):\n" +
		"    d = json.loads(payload)\n" +
		"    return json.dumps({'hello': d['name'], 'lang': 'python'})\n"
	m, err := Load(context.Background(), "python", []byte(src), LoadOptions{Name: "g"})
	if err != nil {
		t.Fatal(err)
	}
	got := mustInvoke(t, m, "greet", `{"name":"world"}`)
	assertJSONEq(t, got, `{"hello":"world","lang":"python"}`)
}

func TestGpythonJSONRoundTrip(t *testing.T) {
	src := "import json\n" +
		"def rt(payload):\n" +
		"    return json.dumps(json.loads(payload))\n"
	m, err := Load(context.Background(), "py", []byte(src), LoadOptions{Name: "rt"})
	if err != nil {
		t.Fatal(err)
	}
	in := `{"n":42,"f":1.5,"b":true,"s":"x","a":[1,2,3],"o":{"k":"v"},"z":null}`
	assertJSONEq(t, mustInvoke(t, m, "rt", in), in)
}

func TestPoolParallel(t *testing.T) {
	src := "def busy(payload):\n    return payload\n"
	pool, err := LoadPool(context.Background(), "python", []byte(src), 4, LoadOptions{Name: "p"})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := pool.Invoke(context.Background(), "busy", []byte(`"ok"`))
			if err != nil || string(out) != `"ok"` {
				t.Errorf("pool invoke = %q err=%v", out, err)
			}
		}()
	}
	wg.Wait()
}

func TestServiceHTTP(t *testing.T) {
	src := "import json\n" +
		"def greet(payload):\n" +
		"    return json.dumps({'hi': json.loads(payload)['name']})\n"
	pool, err := LoadPool(context.Background(), "python", []byte(src), 2, LoadOptions{Name: "greeter"})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	svc := &Service{Name: "greeter", Prefix: "/v1/greeter", Pool: pool,
		Routes: []Route{{Method: "POST", Path: "/greet", Func: "greet"}}}
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/greeter/greet", "application/json", strings.NewReader(`{"name":"ada"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got["hi"] != "ada" {
		t.Fatalf("got %v", got)
	}
}

func TestWazero(t *testing.T) {
	m, err := Load(context.Background(), "rust", echoWasm, LoadOptions{Name: "echo"})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	assertJSONEq(t, mustInvoke(t, m, "echo", `{"name":"ada"}`), `{"echo":{"name":"ada"}}`)

	// Hammer it well past the 64 KiB arena: if dealloc didn't reset, the bump
	// allocator would overflow and the host's writes would trap.
	for i := 0; i < 5000; i++ {
		if _, err := m.Invoke(context.Background(), "echo", []byte(`{"i":1}`)); err != nil {
			t.Fatalf("repeat %d: %v", i, err)
		}
	}
}

func TestLoadDir(t *testing.T) {
	services, err := LoadDir(context.Background(), "examples")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*Service{}
	for _, s := range services {
		byName[s.Name] = s
		defer s.Pool.Close()
	}
	cases := []struct{ svc, fn, in, want string }{
		{"hello-py", "greet", `{"name":"x"}`, `{"hello":"x","lang":"python"}`},
		{"hello-ts", "greet", `{"name":"x"}`, `{"hello":"x","lang":"typescript"}`},
		{"echo-rust", "echo", `{"name":"x"}`, `{"echo":{"name":"x"}}`},
	}
	for _, c := range cases {
		s := byName[c.svc]
		if s == nil {
			t.Errorf("service %q not loaded from manifests", c.svc)
			continue
		}
		out, err := s.Pool.Invoke(context.Background(), c.fn, []byte(c.in))
		if err != nil {
			t.Errorf("%s.%s: %v", c.svc, c.fn, err)
			continue
		}
		assertJSONEq(t, string(out), c.want)
	}
}

func TestLangsRegistered(t *testing.T) {
	want := map[string]bool{"javascript": false, "python": false, "wasm": false}
	for _, l := range Langs() {
		if _, ok := want[l]; ok {
			want[l] = true
		}
	}
	for l, found := range want {
		if !found {
			t.Errorf("engine %q not registered", l)
		}
	}
}

func assertJSONEq(t *testing.T, got, want string) {
	t.Helper()
	var g, w interface{}
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("got not JSON: %q (%v)", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want not JSON: %q", want)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if string(gb) != string(wb) {
		t.Fatalf("JSON mismatch:\n got=%s\nwant=%s", gb, wb)
	}
}
