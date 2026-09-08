package main

import (
	"flag"
	"log"

	sdk "github.com/crossplane/function-sdk-go"
)

// main serves the composition function over gRPC. Flags mirror the
// function-sdk-go conventions: --address to bind, --tls-server-certs-dir for
// mTLS (supplied by Crossplane in-cluster), and --insecure for local render.
func main() {
	var (
		address     = flag.String("address", ":9443", "Address at which to listen for gRPC connections.")
		tlsCertsDir = flag.String("tls-server-certs-dir", "", "Directory containing server certs (tls.key, tls.crt) and the CA used to verify clients (ca.crt).")
		insecure    = flag.Bool("insecure", false, "Run without mTLS credentials. Not recommended outside of local development.")
		debug       = flag.Bool("debug", false, "Emit debug logs.")
	)
	flag.Parse()

	logger, err := sdk.NewLogger(*debug)
	if err != nil {
		log.Fatalf("cannot create logger: %v", err)
	}

	opts := []sdk.ServeOption{
		sdk.Listen("tcp", *address),
		sdk.Insecure(*insecure),
	}
	if *tlsCertsDir != "" {
		opts = append(opts, sdk.MTLSCertificates(*tlsCertsDir))
	}

	if err := sdk.Serve(&Function{log: logger}, opts...); err != nil {
		log.Fatalf("cannot serve function: %v", err)
	}
}
