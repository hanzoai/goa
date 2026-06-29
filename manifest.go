package goa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
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
	Source string            `json:"source"` // path to source, resolved within the manifest's fs.FS
	Pool   int               `json:"pool"`   // pooled interpreter count (default 8)
	Prefix string            `json:"prefix"`
	Routes []Route           `json:"routes"`
	Env    map[string]string `json:"env,omitempty"`
}

// ParseManifest validates raw manifest bytes. desc names the source for errors.
func ParseManifest(b []byte, desc string) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("goa: manifest %s: %w", desc, err)
	}
	if m.Name == "" || m.Lang == "" || m.Source == "" {
		return nil, fmt.Errorf("goa: manifest %s: name, lang and source are required", desc)
	}
	if m.Pool <= 0 {
		m.Pool = 8
	}
	return &m, nil
}

// LoadManifest reads and validates a manifest file from disk.
func LoadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseManifest(b, path)
}

// Build loads the manifest's source (resolved within fsys) through its engine
// into a pooled Service.
func (m *Manifest) Build(ctx context.Context, fsys fs.FS) (*Service, error) {
	src, err := fs.ReadFile(fsys, m.Source)
	if err != nil {
		return nil, fmt.Errorf("goa: service %q source: %w", m.Name, err)
	}
	pool, err := LoadPool(ctx, m.Lang, src, m.Pool, LoadOptions{Name: m.Name, Env: m.Env})
	if err != nil {
		return nil, err
	}
	return &Service{Name: m.Name, Prefix: m.Prefix, Pool: pool, Routes: m.Routes}, nil
}

// LoadFS discovers every *.manifest.json at the root of fsys and builds the
// services. fsys is the single source abstraction: pass an embed.FS to bake
// services into a static binary, or os.DirFS(dir) to load them from disk.
// Successful services are returned even if some fail; failures are joined.
func LoadFS(ctx context.Context, fsys fs.FS) ([]*Service, error) {
	matches, err := fs.Glob(fsys, "*.manifest.json")
	if err != nil {
		return nil, err
	}
	var services []*Service
	var errs []error
	for _, path := range matches {
		b, err := fs.ReadFile(fsys, path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		man, err := ParseManifest(b, path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		svc, err := man.Build(ctx, fsys)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		services = append(services, svc)
	}
	if len(errs) > 0 {
		return services, fmt.Errorf("goa: %d service(s) failed to load: %w", len(errs), errors.Join(errs...))
	}
	return services, nil
}

// LoadDir builds every service whose manifest lives in dir on disk. Convenience
// over LoadFS(ctx, os.DirFS(dir)).
func LoadDir(ctx context.Context, dir string) ([]*Service, error) {
	return LoadFS(ctx, os.DirFS(dir))
}
