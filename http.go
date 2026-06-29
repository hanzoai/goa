package goa

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Route binds an HTTP method+path to an exported function in the module.
type Route struct {
	Method string `json:"method"` // GET, POST, ...
	Path   string `json:"path"`   // e.g. /greet  (combined with the service Prefix)
	Func   string `json:"func"`   // exported function name to invoke
}

// Service is a mounted polyglot module: a Pool of interpreters plus the route
// table that maps HTTP requests onto its functions. Handler() returns a plain
// net/http.Handler so any host can serve it — in hanzoai/cloud it is mounted
// onto the zip/gofiber app via zip.AdaptNetHTTP, keeping goa host-agnostic.
type Service struct {
	Name   string
	Prefix string // e.g. /v1/greeter ; routes are mounted under it
	Pool   *Pool
	Routes []Route
}

// Mount registers this service's routes onto an existing mux, so a host can
// serve many services from one mux (the Go 1.22 "METHOD /path" patterns keep
// them non-conflicting). This is the single route-registration path.
func (s *Service) Mount(mux *http.ServeMux) {
	for _, r := range s.Routes {
		fn := r.Func
		pattern := fmt.Sprintf("%s %s%s", r.Method, s.Prefix, r.Path)
		mux.HandleFunc(pattern, func(w http.ResponseWriter, req *http.Request) {
			body, err := io.ReadAll(io.LimitReader(req.Body, 32<<20))
			if err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			if len(body) == 0 {
				body = []byte("{}")
			}
			out, err := s.Pool.Invoke(req.Context(), fn, body)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(out)
		})
	}
}

// Handler returns a standalone http.Handler serving just this service. In
// hanzoai/cloud it is mounted onto the zip/gofiber app via zip.AdaptNetHTTP,
// keeping goa host-agnostic. To serve many services from one mux, use Mount.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	s.Mount(mux)
	return mux
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	b, _ := json.Marshal(map[string]string{"error": err.Error()})
	_, _ = w.Write(b)
}
