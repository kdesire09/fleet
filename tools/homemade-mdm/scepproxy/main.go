// Command scepproxy is a standalone, Fleet-Free-tier-compatible replacement
// for Fleet Premium's "SCEP proxy" feature (RegisterSCEPProxy /
// certificate_authorities, ee-gated: server/service/certificate_authorities.go
// stubs return fleet.ErrMissingLicense on Free).
//
// It is a transparent SCEP relay in front of a real CA (NDES, an internal
// SCEP server, etc). It deliberately does not use Fleet's CA-configuration
// feature at all (that feature is 100% ee-gated with no public API to read
// CA config back out on Free) - instead it keeps its own CA URL/trust config
// locally, and devices are pointed at it directly via a plain custom
// configuration profile (.mobileconfig) uploaded through Fleet's CORE,
// unrestricted profile API/GitOps (controls.macos_settings.custom_settings).
//
// Implementation note: this reuses server/mdm/scep (Fleet's own MIT-licensed
// fork of micromdm/scep, not ee/) almost as-is. scepclient.New() returns a
// Client that implements scepserver.Service by forwarding every SCEP verb
// (GetCACaps/GetCACert/PKIOperation/GetNextCACert) to the upstream CA over
// HTTP. scepserver.MakeServerEndpoints + MakeHTTPHandler then re-expose that
// same Service as our own SCEP HTTP server. The result is a working proxy in
// ~60 lines, with zero protocol logic of our own to get wrong.
//
// Security trade-off (documented, not hidden): this v1 proxy does not
// terminate/rewrite the SCEP challenge password per-device the way Fleet EE's
// one-time challenge does - the challenge lives in the .mobileconfig pushed
// to devices (same as any traditional SCEP payload predating Fleet's CA
// management UI). See HOMEMADE-SCEP-PROXY.md for the full trade-off writeup.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	scepclient "github.com/fleetdm/fleet/v4/server/mdm/scep/client"
	scepserver "github.com/fleetdm/fleet/v4/server/mdm/scep/server"
)

func main() {
	listen := flag.String("listen", ":8090", "address to listen on")
	upstreamURL := flag.String("upstream-url", "", "SCEP URL of the real CA to proxy to (e.g. https://ndes.example.com/certsrv/mscep/mscep.dll)")
	upstreamRootCA := flag.String("upstream-root-ca", "", "optional path to a CA cert bundle to trust when connecting to -upstream-url")
	upstreamInsecure := flag.Bool("upstream-insecure", false, "skip TLS verification when connecting to -upstream-url (testing only, never in production)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if *upstreamURL == "" {
		fmt.Fprintln(os.Stderr, "error: -upstream-url is required (the real CA's SCEP endpoint)")
		os.Exit(1)
	}

	var opts []scepclient.Option
	if *upstreamRootCA != "" {
		opts = append(opts, scepclient.WithRootCA(*upstreamRootCA))
	}
	if *upstreamInsecure {
		opts = append(opts, scepclient.Insecure())
	}

	// client forwards every SCEP verb to *upstreamURL - it IS a
	// scepserver.Service, just backed by an HTTP call instead of a local CA.
	client, err := scepclient.New(*upstreamURL, logger, opts...)
	if err != nil {
		logger.Error("create upstream SCEP client", "err", err)
		os.Exit(1)
	}

	endpoints := scepserver.MakeServerEndpoints(client)
	handler := scepserver.MakeHTTPHandler(endpoints, client, logger)

	logger.Info("scep proxy starting", "listen", *listen, "upstream", *upstreamURL)
	if err := http.ListenAndServe(*listen, handler); err != nil {
		logger.Error("http server", "err", err)
		os.Exit(1)
	}
}
