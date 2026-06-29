// Command serve loads every *.manifest.json in the examples directory and
// serves the polyglot services on :8080 — Python (gpython) and TypeScript
// (goja) handlers running as goroutines inside one static Go binary.
//
//	go run ./examples/serve
//	curl -s localhost:8080/v1/hello-py/greet -d '{"name":"ada"}'
//	curl -s localhost:8080/v1/hello-ts/greet -d '{"name":"ada"}'
package main

import (
	"context"
	"log"
	"net/http"

	"github.com/hanzoai/goa"
)

func main() {
	ctx := context.Background()
	services, err := goa.LoadDir(ctx, "examples")
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	for _, s := range services {
		s.Mount(mux)
		log.Printf("mounted %s at %s (%d routes)", s.Name, s.Prefix, len(s.Routes))
	}
	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
