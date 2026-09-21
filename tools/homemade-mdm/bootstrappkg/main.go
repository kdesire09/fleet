// Command bootstrappkg is a standalone, Fleet-Free-tier-compatible replacement for Fleet
// Premium's ee-gated "Bootstrap package" feature (auto-install a .pkg during DEP zero-touch
// enrollment).
//
// Unlike the ABM/DEP and SCEP proxy tools, this one does NOT talk to an external protocol
// directly (there's no equivalent of Apple's DEP API for pushing an arbitrary MDM command --
// Fleet itself is the MDM server, and its nanomdm+APNs push infrastructure isn't independently
// callable). Instead it calls Fleet's own CORE, already-license-free API:
//
//   - POST /api/v1/fleet/login                  (get a session token -- same as the UI/fleetctl)
//   - GET  /api/v1/fleet/hosts                   (find the target host's UUID by serial)
//   - POST /api/v1/fleet/mdm/commands/run        (enqueue an arbitrary Apple MDM command)
//   - GET  /api/v1/fleet/mdm/commandresults      (poll delivery status)
//
// Confirmed clean per PREMIUM-FEATURES-INVENTORY.md's research: InstallEnterpriseApplication is
// NOT in the appleMDMPremiumCommands block-list (server/service/mdm.go:676-680) that blocks
// EraseDevice/DeviceLock/ClearPasscode -- so this is not a license circumvention, just normal use
// of an endpoint Fleet already ships unlocked. The only genuinely ee-gated pieces (bootstrap
// package upload/storage/download, ee/server/service/mdm.go:382-556 and
// server/service/apple_mdm.go:3634-3687) are avoided entirely: this tool hosts the .pkg itself
// instead of asking Fleet to store/serve it.
//
// The InstallEnterpriseApplication plist and the app manifest format are reused as-is from
// Fleet's own core code (server/mdm/apple/commander.go's ManifestURL variant, and the core
// server/mdm/apple/appmanifest package -- the same one backing tools/mdm/apple/appmanifest).
package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/fleetdm/fleet/v4/server/mdm/apple/appmanifest"
	"github.com/google/uuid"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		pkgFile := fs.String("pkg-file", "", "path to the .pkg installer to serve")
		publicURL := fs.String("public-url", "", "public HTTPS base URL this will be reachable at (e.g. the ngrok URL for this tunnel), no trailing slash")
		port := fs.String("port", "8089", "local port to listen on (point your ngrok tunnel at this)")
		fs.Parse(os.Args[2:])
		cmdServe(*pkgFile, *publicURL, *port)
	case "login":
		fs := flag.NewFlagSet("login", flag.ExitOnError)
		fleetURL := fs.String("fleet-url", "", "Fleet server base URL (e.g. https://boundless-arson-versus.ngrok-free.dev)")
		email := fs.String("email", "", "Fleet admin email")
		password := fs.String("password", "", "Fleet admin password")
		insecure := fs.Bool("insecure", false, "skip TLS certificate verification")
		fs.Parse(os.Args[2:])
		cmdLogin(*fleetURL, *email, *password, *insecure)
	case "find-host":
		fs := flag.NewFlagSet("find-host", flag.ExitOnError)
		fleetURL := fs.String("fleet-url", "", "Fleet server base URL")
		token := fs.String("token", "", "Fleet API token (from `login`)")
		query := fs.String("query", "", "search term matched against hostname/serial/UUID (Fleet's own hosts search)")
		insecure := fs.Bool("insecure", false, "skip TLS certificate verification")
		fs.Parse(os.Args[2:])
		cmdFindHost(*fleetURL, *token, *query, *insecure)
	case "install":
		fs := flag.NewFlagSet("install", flag.ExitOnError)
		fleetURL := fs.String("fleet-url", "", "Fleet server base URL")
		token := fs.String("token", "", "Fleet API token (from `login`)")
		hostUUID := fs.String("host-uuid", "", "target host UUID (from `find-host`)")
		manifestURL := fs.String("manifest-url", "", "public URL of the manifest.plist (from `serve`, e.g. https://.../manifest.plist)")
		insecure := fs.Bool("insecure", false, "skip TLS certificate verification")
		fs.Parse(os.Args[2:])
		cmdInstall(*fleetURL, *token, *hostUUID, *manifestURL, *insecure)
	case "check-command":
		fs := flag.NewFlagSet("check-command", flag.ExitOnError)
		fleetURL := fs.String("fleet-url", "", "Fleet server base URL")
		token := fs.String("token", "", "Fleet API token")
		commandUUID := fs.String("command-uuid", "", "command UUID printed by `install`")
		insecure := fs.Bool("insecure", false, "skip TLS certificate verification")
		fs.Parse(os.Args[2:])
		cmdCheckCommand(*fleetURL, *token, *commandUUID, *insecure)
	case "unenroll-mdm":
		fs := flag.NewFlagSet("unenroll-mdm", flag.ExitOnError)
		fleetURL := fs.String("fleet-url", "", "Fleet server base URL")
		token := fs.String("token", "", "Fleet API token")
		hostID := fs.String("host-id", "", "numeric host ID (from `find-host`)")
		insecure := fs.Bool("insecure", false, "skip TLS certificate verification")
		fs.Parse(os.Args[2:])
		cmdUnenrollMDM(*fleetURL, *token, *hostID, *insecure)
	case "delete-host":
		fs := flag.NewFlagSet("delete-host", flag.ExitOnError)
		fleetURL := fs.String("fleet-url", "", "Fleet server base URL")
		token := fs.String("token", "", "Fleet API token")
		hostID := fs.String("host-id", "", "numeric host ID (from `find-host`)")
		insecure := fs.Bool("insecure", false, "skip TLS certificate verification")
		fs.Parse(os.Args[2:])
		cmdDeleteHost(*fleetURL, *token, *hostID, *insecure)
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: bootstrappkg <command> [flags]

Commands:
  serve         -pkg-file FILE -public-url URL [-port PORT]   Host the .pkg + generated manifest.plist locally (tunnel this with ngrok)
  login         -fleet-url URL -email E -password P            Log in to Fleet, prints an API token
  find-host     -fleet-url URL -token T -query Q                Search Fleet hosts (by serial/hostname/UUID), prints matches with their UUID
  install       -fleet-url URL -token T -host-uuid U -manifest-url URL   Enqueue the InstallEnterpriseApplication MDM command
  check-command -fleet-url URL -token T -command-uuid U          Poll delivery status of a previously issued command
  unenroll-mdm  -fleet-url URL -token T -host-id ID              Force Fleet to mark a host unenrolled (fixes stale state after an out-of-band androidcmd wipe/lock/clear-passcode)
  delete-host   -fleet-url URL -token T -host-id ID              Delete a host record entirely (e.g. a wiped COBO device that won't re-enroll)`)
}

func fatalf(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(1)
}

// cmdServe hosts the .pkg and a manifest.plist generated from it (via Fleet's own core
// appmanifest package -- the same format server/mdm/apple/commander.go's
// InstallEnterpriseApplication command expects) at /pkg and /manifest.plist.
func cmdServe(pkgFile, publicURL, port string) {
	if pkgFile == "" || publicURL == "" {
		fatalf("-pkg-file and -public-url are required")
	}

	f, err := os.Open(pkgFile)
	if err != nil {
		fatalf("open pkg file: %v", err)
	}
	pkgBytes, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		fatalf("read pkg file: %v", err)
	}

	sum := sha256.Sum256(pkgBytes)
	manifest := appmanifest.NewFromSha(sum[:], publicURL+"/pkg")
	manifestPlist, err := manifest.Plist()
	if err != nil {
		fatalf("build manifest plist: %v", err)
	}

	http.HandleFunc("/pkg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(pkgBytes)
	})
	http.HandleFunc("/manifest.plist", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write(manifestPlist)
	})

	fmt.Printf("Serving:\n  package:  %s/pkg\n  manifest: %s/manifest.plist\n", publicURL, publicURL)
	fmt.Printf("Point `install -manifest-url` at %s/manifest.plist once your tunnel to :%s is up.\n", publicURL, port)
	fatalf("%v", http.ListenAndServe(":"+port, nil))
}

func cmdLogin(fleetURL, email, password string, insecure bool) {
	if fleetURL == "" || email == "" || password == "" {
		fatalf("-fleet-url, -email and -password are required")
	}
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	resp := doRequest(fleetURL, "POST", "/api/v1/fleet/login", "", body, insecure)

	var out struct {
		Token string `json:"token"`
		Err   string `json:"error"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		fatalf("decode login response: %v (raw: %s)", err, string(resp))
	}
	if out.Token == "" {
		fatalf("login failed: %s", string(resp))
	}
	fmt.Println(out.Token)
}

func cmdFindHost(fleetURL, token, query string, insecure bool) {
	if fleetURL == "" || token == "" || query == "" {
		fatalf("-fleet-url, -token and -query are required")
	}
	resp := doRequest(fleetURL, "GET", "/api/v1/fleet/hosts?query="+urlEscape(query), token, nil, insecure)

	var out struct {
		Hosts []struct {
			ID           uint   `json:"id"`
			UUID         string `json:"uuid"`
			Hostname     string `json:"hostname"`
			HardwareSerial string `json:"hardware_serial"`
			Platform     string `json:"platform"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		fatalf("decode hosts response: %v (raw: %s)", err, string(resp))
	}
	if len(out.Hosts) == 0 {
		fmt.Println("no matching hosts found")
		return
	}
	for _, h := range out.Hosts {
		fmt.Printf("id=%d  uuid=%s  serial=%s  hostname=%s  platform=%s\n", h.ID, h.UUID, h.HardwareSerial, h.Hostname, h.Platform)
	}
}

// cmdUnenrollMDM calls Fleet's own core, license-free DELETE /hosts/{id}/mdm (UnenrollMDM ->
// UnenrollAndroidHost for Android hosts). This is the fix for the "stale host after an
// out-of-band wipe/lock/clear-passcode" problem: those commands are issued directly against
// AMAPI via `androidcmd`, bypassing Fleet's mdm_android_commands tracking table entirely, so when
// Google's Pub/Sub delivers the COMMAND ack, Fleet's handlePubSubCommand can't find a matching
// row and (per server/mdm/android/service/pubsub.go:290-314) assumes a race window and retries
// forever instead of acking -- meaning the ack that would normally flip host_mdm.enrolled to 0
// never arrives, and Fleet's dashboard is left showing stale, pre-wipe data indefinitely.
// Calling this endpoint ourselves, right after confirming the command is done (see androidcmd's
// `check-op`), drives the exact same core Fleet code path a real Pub/Sub ack would have -- no
// ee/, no license check (server/mdm/android/service/service.go has zero IsPremium references),
// just normal use of an admin action Fleet already exposes.
func cmdUnenrollMDM(fleetURL, token, hostID string, insecure bool) {
	if fleetURL == "" || token == "" || hostID == "" {
		fatalf("-fleet-url, -token and -host-id are required")
	}
	resp := doRequest(fleetURL, "DELETE", "/api/v1/fleet/hosts/"+hostID+"/mdm", token, nil, insecure)
	fmt.Printf("MDM unenroll requested for host %s. response: %s\n", hostID, string(resp))
}

// cmdDeleteHost removes the host record entirely (core DELETE /hosts/{id}) -- for a COBO device
// that's already been factory-reset (won't re-enroll on its own), this is usually more
// appropriate than unenroll-mdm, which leaves a "was enrolled, now unenrolled" record behind.
func cmdDeleteHost(fleetURL, token, hostID string, insecure bool) {
	if fleetURL == "" || token == "" || hostID == "" {
		fatalf("-fleet-url, -token and -host-id are required")
	}
	resp := doRequest(fleetURL, "DELETE", "/api/v1/fleet/hosts/"+hostID, token, nil, insecure)
	fmt.Printf("host %s deleted. response: %s\n", hostID, string(resp))
}

// cmdInstall builds the exact InstallEnterpriseApplication plist that
// server/mdm/apple/commander.go's InstallEnterpriseApplication (ManifestURL variant) sends, and
// enqueues it via Fleet's core, unblocked POST /api/v1/fleet/mdm/commands/run.
func cmdInstall(fleetURL, token, hostUUID, manifestURL string, insecure bool) {
	if fleetURL == "" || token == "" || hostUUID == "" || manifestURL == "" {
		fatalf("-fleet-url, -token, -host-uuid and -manifest-url are required")
	}

	cmdUUID := uuid.NewString()
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Command</key>
    <dict>
      <key>ManifestURL</key>
      <string>%s</string>
      <key>RequestType</key>
      <string>InstallEnterpriseApplication</string>
    </dict>

    <key>CommandUUID</key>
    <string>%s</string>
  </dict>
</plist>`, manifestURL, cmdUUID)

	body, _ := json.Marshal(map[string]interface{}{
		"command":    base64.StdEncoding.EncodeToString([]byte(plist)),
		"host_uuids": []string{hostUUID},
	})
	resp := doRequest(fleetURL, "POST", "/api/v1/fleet/mdm/commands/run", token, body, insecure)

	fmt.Printf("command_uuid: %s\n", cmdUUID)
	fmt.Printf("response: %s\n", string(resp))
	fmt.Println("Check delivery with: check-command -command-uuid " + cmdUUID)
}

func cmdCheckCommand(fleetURL, token, commandUUID string, insecure bool) {
	if fleetURL == "" || token == "" || commandUUID == "" {
		fatalf("-fleet-url, -token and -command-uuid are required")
	}
	resp := doRequest(fleetURL, "GET", "/api/v1/fleet/mdm/commandresults?command_uuid="+urlEscape(commandUUID), token, nil, insecure)
	fmt.Println(string(resp))
}

func doRequest(baseURL, method, path, token string, body []byte, insecure bool) []byte {
	req, err := http.NewRequest(method, baseURL+path, bytes.NewReader(body))
	if err != nil {
		fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// Force HTTP/1.1: this repo's ngrok tunnel has repeatedly hit "http2: client conn could not
	// be established" / HTTP2 framing errors on this edge -- TLSNextProto set to a non-nil empty
	// map disables Go's automatic HTTP/2 upgrade over TLS.
	transport := &http.Transport{TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in dev flag for self-signed local Fleet TLS
	}
	client := &http.Client{Transport: transport}

	resp, err := client.Do(req)
	if err != nil {
		fatalf("request %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fatalf("read response: %v", err)
	}
	if resp.StatusCode >= 300 {
		fatalf("%s %s -> HTTP %d: %s", method, path, resp.StatusCode, string(respBody))
	}
	return respBody
}

func urlEscape(s string) string {
	return url.QueryEscape(s)
}
