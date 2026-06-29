package goa

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Manifest describes a polyglot service to mount: which language, where the
// source is, how many pooled interpreters, and the route table. Drop a
// service.manifest.json next to a .py/.ts/.wasm file and the loader does the
// rest — that is the "easy to port any Go/Python/Rust/JS service in" UX.
//
//	{
//	  "name": "greeter",
//	  "lang": "python",
//	  "source": "greet.py",
//	  "pool": 8,
//	  "prefix": "/v1/greeter",
//	  "routes": [{ "method": "POST", "path": "/greet", "func": "greet" }]
//	}
type Manifest struct {
	Name   string            `json:"name"`
	Lang   string            `json:"lang"`
	Source string            `json:"source"` // path to source, relative to the manifest
	Pool   int               `json:"pool"`   // pooled interpreter count (default = GOMAXPROCS-ish)
	Prefix string            `json:"prefix"`
	Routes []Route           `json:"routes"`
	Env    map[string]string `json:"env,omitempty"`
}

// LoadManifest reads and validates a manifest file.
func LoadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("goa: manifest %s: %w", path, err)
	}
	if m.Name == "" || m.Lang == "" || m.Source == "" {
		return nil, fmt.Errorf("goa: manifest %s: name, lang and source are required", path)
	}
	if m.Pool <= 0 {
		m.Pool = 8
	}
	return &m, nil
}

// Build loads the manifest's source through its engine into a pooled Service.
// baseDir is the directory the manifest's Source is resolved against.
func (m *Manifest) Build(ctx context.Context, baseDir string) (*Service, error) {
	src, err := os.ReadFile(filepath.Join(baseDir, m.Source))
	if err != nil {
		return nil, fmt.Errorf("goa: service %q source: %w", m.Name, err)
	}
	pool, err := LoadPool(ctx, m.Lang, src, m.Pool, LoadOptions{Name: m.Name, Env: m.Env})
	if err != nil {
		return nil, err
	}
	return &Service{Name: m.Name, Prefix: m.Prefix, Pool: pool, Routes: m.Routes}, nil
}

// LoadDir discovers every *.manifest.json under dir and builds the services.
// This is what a host calls at boot to auto-mount a folder of polyglot
// services. Failures are returned per service; successful ones are still built.
func LoadDir(ctx context.Context, dir string) ([]*Service, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.manifest.json"))
	if err != nil {
		return nil, err
	}
	var services []*Service
	var errs []error
	for _, path := range matches {
		man, err := LoadManifest(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		svc, err := man.Build(ctx, dir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		services = append(services, svc)
	}
	if len(errs) > 0 {
		return services, fmt.Errorf("goa: %d service(s) failed to load: %v", len(errs), errs)
	}
	return services, nil
}
