// Command depsync is a standalone, Fleet-Free-tier-compatible replacement
// for the parts of Fleet Premium's ABM/DEP zero-touch enrollment feature
// that talk to Apple's DEP API.
//
// It deliberately touches zero code under ee/ and zero Fleet-Premium-gated
// tables. It reuses the same open (MIT), core nanodep packages Fleet itself
// uses internally (server/mdm/nanodep/...) to talk to Apple's DEP API
// directly, and points the resulting enrollment profile at Fleet's own
// unauthenticated, unlicensed enrollment endpoint (apple_mdm.EnrollPath).
//
// Typical flow:
//
//  1. Generate a keypair and get an ABM server token decrypted (reuse the
//     existing nanodep `deptokens` tool for this part, no need to duplicate
//     it): `go run ./server/mdm/nanodep/cmd/deptokens` then
//     `go run ./server/mdm/nanodep/cmd/deptokens -token token.p7m > decrypted.json`
//
//  2. Import that decrypted token into this tool's local storage:
//     `go run . -storage-dir ./data import-config -token-json decrypted.json`
//
//  3. Register an enrollment profile with Apple, pointed at our Fleet server:
//     `go run . -storage-dir ./data define-profile -fleet-url https://<host> -name "My org"`
//
//  4. Assign that profile to one or more device serials:
//     `go run . -storage-dir ./data assign -profile-uuid <uuid> SERIAL1 SERIAL2`
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	depclient "github.com/fleetdm/fleet/v4/server/mdm/nanodep/client"
	"github.com/fleetdm/fleet/v4/server/mdm/nanodep/godep"
	filestorage "github.com/fleetdm/fleet/v4/server/mdm/nanodep/storage/file"
)

// depName is a purely local label under which nanodep's storage keeps
// tokens/config for this ABM account. It doesn't need to match anything at
// Apple - it's just a key, kept fixed since we only ever manage one account.
const depName = "default"

// fleetEnrollPath mirrors apple_mdm.EnrollPath. Not imported directly to
// keep this tool a standalone module with no dependency on server/service
// (which pulls in the whole Fleet server dependency graph).
const fleetEnrollPath = "/api/mdm/apple/enroll"

func main() {
	storageDir := flag.String("storage-dir", "./depsync-data", "directory to store DEP auth tokens/config (nanodep file storage)")
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		usage()
		os.Exit(1)
	}

	if err := os.MkdirAll(*storageDir, 0o700); err != nil {
		fatalf("create storage dir: %v", err)
	}
	store, err := filestorage.New(*storageDir)
	if err != nil {
		fatalf("init storage: %v", err)
	}

	ctx := context.Background()

	switch args[0] {
	case "import-config":
		importConfig(ctx, store, args[1:])
	case "define-profile":
		defineProfile(ctx, store, args[1:])
	case "assign":
		assign(ctx, store, args[1:])
	case "account-detail":
		accountDetail(ctx, store, args[1:])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: depsync [-storage-dir DIR] <command> [flags]

Commands:
  import-config   -token-json FILE           Import a decrypted ABM token JSON (see deptokens -token)
  account-detail                             Fetch and print the DEP account info (sanity check auth)
  define-profile  -fleet-url URL -name NAME  Register an enrollment profile with Apple, pointed at Fleet
  assign          -profile-uuid UUID SERIAL...  Assign a profile UUID to one or more device serials`)
}

func fatalf(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(1)
}

// importConfig reads the decrypted ABM token JSON (the output of
// `deptokens -token ...`) and stores it as OAuth1 tokens + default config in
// nanodep's file storage, so godep.Client can authenticate.
func importConfig(ctx context.Context, store *filestorage.FileStorage, args []string) {
	fs := flag.NewFlagSet("import-config", flag.ExitOnError)
	tokenJSON := fs.String("token-json", "", "path to decrypted ABM token JSON (output of deptokens -token)")
	fs.Parse(args)

	if *tokenJSON == "" {
		fatalf("import-config: -token-json is required")
	}

	b, err := os.ReadFile(*tokenJSON)
	if err != nil {
		fatalf("read token json: %v", err)
	}

	var tokens depclient.OAuth1Tokens
	if err := json.Unmarshal(b, &tokens); err != nil {
		fatalf("parse token json: %v", err)
	}
	if !tokens.Valid() {
		fatalf("decrypted token JSON is missing required OAuth1 fields (consumer_key/consumer_secret/access_token/access_secret)")
	}

	if err := store.StoreAuthTokens(ctx, depName, &tokens); err != nil {
		fatalf("store auth tokens: %v", err)
	}
	// Default config (Apple's production base URL) - only needs to be
	// written once; StoreConfig with an empty BaseURL is fine, the client
	// falls back to depclient.DefaultBaseURL.
	if err := store.StoreConfig(ctx, depName, &depclient.Config{}); err != nil {
		fatalf("store config: %v", err)
	}

	fmt.Println("OK: ABM auth tokens imported")
	if !tokens.AccessTokenExpiry.IsZero() {
		fmt.Printf("access token expires: %s\n", tokens.AccessTokenExpiry.Format(time.RFC3339))
	}
}

func newGodepClient(store *filestorage.FileStorage) *godep.Client {
	return godep.NewClient(store, http.DefaultClient)
}

func accountDetail(ctx context.Context, store *filestorage.FileStorage, _ []string) {
	c := newGodepClient(store)
	acc, err := c.AccountDetail(ctx, depName)
	if err != nil {
		fatalf("account detail: %v", err)
	}
	b, _ := json.MarshalIndent(acc, "", "  ")
	fmt.Println(string(b))
}

func defineProfile(ctx context.Context, store *filestorage.FileStorage, args []string) {
	fs := flag.NewFlagSet("define-profile", flag.ExitOnError)
	fleetURL := fs.String("fleet-url", "", "public HTTPS URL of the Fleet server (e.g. https://boundless-arson-versus.ngrok-free.dev)")
	name := fs.String("name", "Fleet (homemade DEP)", "profile_name shown to the device during setup")
	removable := fs.Bool("removable", true, "whether MDM enrollment is user-removable (is_mdm_removable)")
	awaitConfigured := fs.Bool("await-device-configured", false, "hold the device at Setup Assistant until explicitly released (advanced)")
	fs.Parse(args)

	if *fleetURL == "" {
		fatalf("define-profile: -fleet-url is required")
	}

	enrollURL, err := joinURL(*fleetURL, fleetEnrollPath)
	if err != nil {
		fatalf("build enroll URL: %v", err)
	}

	c := newGodepClient(store)
	profile := &godep.Profile{
		ProfileName: *name,
		URL:         enrollURL,
		// Deliberately no ConfigurationWebURL: setting it would route the
		// device through Fleet's MDM SSO flow, which is ee-gated
		// (ee/server/service/mdm.go InitiateMDMSSO/MDMSSOCallback). Leaving
		// it empty makes the device enroll directly against URL, matching
		// what already works for our manual enrollment today.
		IsMDMRemovable:        *removable,
		AwaitDeviceConfigured: *awaitConfigured,
		AutoAdvanceSetup:      false,
	}

	resp, err := c.DefineProfile(ctx, depName, profile)
	if err != nil {
		fatalf("define profile: %v", err)
	}

	fmt.Printf("OK: profile registered\nprofile_uuid: %s\n", resp.ProfileUUID)
	fmt.Println("Save this UUID - you'll need it for the `assign` command.")
}

func assign(ctx context.Context, store *filestorage.FileStorage, args []string) {
	fs := flag.NewFlagSet("assign", flag.ExitOnError)
	profileUUID := fs.String("profile-uuid", "", "profile UUID returned by define-profile")
	fs.Parse(args)

	serials := fs.Args()
	if *profileUUID == "" {
		fatalf("assign: -profile-uuid is required")
	}
	if len(serials) == 0 {
		fatalf("assign: at least one device serial number is required")
	}

	c := newGodepClient(store)
	resp, err := c.AssignProfile(ctx, depName, *profileUUID, serials...)
	if err != nil {
		fatalf("assign profile: %v", err)
	}

	b, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(b))

	var failed []string
	for serial, status := range resp.Devices {
		if status != "SUCCESS" {
			failed = append(failed, fmt.Sprintf("%s=%s", serial, status))
		}
	}
	if len(failed) > 0 {
		fatalf("some devices failed assignment: %v", failed)
	}
	fmt.Println("OK: all devices assigned successfully")
}

// joinURL mirrors the shape of apple_mdm.ResolveAppleEnrollMDMURL without
// importing server/mdm/apple (which would pull in the full server module
// graph into this standalone tool).
func joinURL(base, path string) (string, error) {
	if base == "" {
		return "", errors.New("empty base URL")
	}
	for len(base) > 0 && base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	return base + path, nil
}
