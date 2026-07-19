// Command serve exposes the disposable mirror over HTTPS for the Incus proof.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

// main serves a fixed directory with the locally trusted proof certificate.
func main() {
	listen := flag.String("listen", ":8443", "HTTPS listen address")
	root := flag.String("root", "mirror", "mirror root")
	cert := flag.String("cert", "certs/phase1.pem", "TLS certificate")
	key := flag.String("key", "certs/phase1-key.pem", "TLS private key")
	flag.Parse()

	server := &http.Server{
		Addr:              *listen,
		Handler:           http.FileServer(http.Dir(*root)),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	fmt.Fprintf(os.Stderr, "serving %s on https://host.lima.internal%s\n", *root, *listen)
	if err := server.ListenAndServeTLS(*cert, *key); err != nil {
		fmt.Fprintf(os.Stderr, "serve mirror: %v\n", err)
		os.Exit(1)
	}
}
