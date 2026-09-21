// Command androidcmd is a standalone, Fleet-Free-tier-compatible replacement
// for Fleet Premium's ee-gated Android device MDM commands (Lock, Wipe,
// Clear passcode).
//
// Unlike the ABM/DEP and SCEP proxy tools, the business logic for these
// commands is already CORE, not ee/: server/mdm/android/service/service.go's
// LockAndroidHost/WipeAndroidHost/ClearAndroidPasscode are plain MIT-licensed
// Go that call the Android Management API directly. The ONLY gate is
// architectural: cmd/fleet/serve.go only wraps fleet.Service with
// ee/server/service (which is what makes the HTTP endpoints
// /api/v1/fleet/hosts/{id}/lock|wipe|unlock reachable) `if
// license.IsPremium()`. Core's own stub (server/service/scripts.go) returns
// fleet.ErrMissingLicense unconditionally for every platform.
//
// Blocker and how this tool works around it: a Free-tier admin has no core
// Fleet API path to learn an Android host's AMAPI Device.DeviceID (never
// serialized in any JSON response - confirmed by reading
// server/fleet/hosts.go, server/mdm/android/android.go, and grepping
// server/service/*.go). This tool sidesteps that by talking to Google's
// Android Management API DIRECTLY with our own GCP service account
// credentials (same approach as Fleet's own internal debug tool,
// tools/android/android.go) instead of going through Fleet at all: list
// devices straight from Google (which DOES include the hardware serial
// number, unlike Fleet's DB-backed API), match against the serial number
// shown for the host in Fleet's UI/API, then issue the command.
//
// Deliberately not using server/mdm/android/service/androidmgmt.Client here:
// its EnterprisesDevicesListPartial applies a
// .Fields("nextPageToken", "devices/name") restriction that strips
// HardwareInfo.SerialNumber, which we need for matching. Using
// androidmanagement.Service directly (like tools/android/android.go does)
// avoids that.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/fleetdm/fleet/v4/server/mdm/android"
	"google.golang.org/api/androidmanagement/v1"
	"google.golang.org/api/option"
)

// longCommandDuration mirrors server/mdm/android/service/service.go's
// longCommandDuration: AMAPI has no "pending forever" concept, so Fleet (and
// we) set an effectively-infinite duration (10 years) to match how Apple/
// Windows MDM commands stay queued until delivered.
const longCommandDuration = "315360000s"

func main() {
	credsPath := flag.String("creds-file", "", "path to the GCP service account JSON key (or set GOOGLE_APPLICATION_CREDENTIALS_JSON env var with the JSON content directly)")
	enterpriseID := flag.String("enterprise-id", "", "AMAPI enterprise ID (without the 'enterprises/' prefix) - get it from Fleet: GET /api/v1/fleet/android_enterprise")
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		usage()
		os.Exit(1)
	}

	credsJSON, err := loadCredentials(*credsPath)
	if err != nil {
		fatalf("load credentials: %v", err)
	}

	ctx := context.Background()
	mgmt, err := androidmanagement.NewService(ctx, option.WithCredentialsJSON(credsJSON))
	if err != nil {
		fatalf("create android management client: %v", err)
	}

	*enterpriseID = strings.TrimPrefix(*enterpriseID, "enterprises/")
	if *enterpriseID == "" {
		fatalf("-enterprise-id is required")
	}

	switch args[0] {
	case "list-devices":
		listDevices(mgmt, *enterpriseID)
	case "check-op":
		fs := flag.NewFlagSet("check-op", flag.ExitOnError)
		opName := fs.String("operation", "", "operation name returned by a previous command (the 'operation:' line)")
		fs.Parse(args[1:])
		checkOperation(mgmt, *opName)
	case "lock":
		fs := flag.NewFlagSet("lock", flag.ExitOnError)
		deviceName := fs.String("device-name", "", "full AMAPI device resource name, from list-devices output (e.g. enterprises/LC.../devices/862...)")
		fs.Parse(args[1:])
		issueCommand(mgmt, mustDeviceName(*deviceName), &androidmanagement.Command{
			Type:     string(android.MDMAndroidCommandTypeLock),
			Duration: longCommandDuration,
		})
	case "wipe":
		fs := flag.NewFlagSet("wipe", flag.ExitOnError)
		deviceName := fs.String("device-name", "", "full AMAPI device resource name, from list-devices output")
		fs.Parse(args[1:])
		issueCommand(mgmt, mustDeviceName(*deviceName), &androidmanagement.Command{
			Type:       string(android.MDMAndroidCommandTypeWipe),
			WipeParams: &androidmanagement.WipeParams{},
			Duration:   longCommandDuration,
		})
	case "clear-passcode":
		fs := flag.NewFlagSet("clear-passcode", flag.ExitOnError)
		deviceName := fs.String("device-name", "", "full AMAPI device resource name, from list-devices output")
		fs.Parse(args[1:])
		issueCommand(mgmt, mustDeviceName(*deviceName), &androidmanagement.Command{
			Type:        string(android.MDMAndroidCommandTypeResetPassword),
			NewPassword: "", // explicit empty: clears the passcode, matches server/mdm/android/service/service.go
			Duration:    longCommandDuration,
		})
	case "create-web-app":
		fs := flag.NewFlagSet("create-web-app", flag.ExitOnError)
		title := fs.String("title", "", "display title of the web app shortcut")
		startURL := fs.String("url", "", "absolute start URL the shortcut opens (e.g. https://example.com)")
		fs.Parse(args[1:])
		createWebApp(mgmt, *enterpriseID, *title, *startURL)
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: androidcmd -enterprise-id ID [-creds-file FILE] <command> [flags]

Commands:
  list-devices                        List devices in the enterprise, with hardware serial numbers, straight from Google
  check-op       -operation NAME      Check whether a previously issued command has been delivered/acked
  lock           -device-name NAME    Lock a device
  wipe           -device-name NAME    Wipe a device (factory reset / work profile removal)
  clear-passcode -device-name NAME    Clear a device's screen lock passcode
  create-web-app -title T -url URL    Create a bookmark-style web app in Managed Google Play for this enterprise`)
}

func fatalf(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(1)
}

func mustDeviceName(name string) string {
	if name == "" {
		fatalf("-device-name is required (get it from `androidcmd list-devices`)")
	}
	return name
}

// loadCredentials reads the GCP service account JSON either from -creds-file
// or, if unset, from the same env var Fleet itself uses
// (FLEET_DEV_ANDROID_GOOGLE_SERVICE_CREDENTIALS) so the exact same
// credentials.json used to configure Fleet can be reused here without
// duplicating a copy on disk.
func loadCredentials(path string) ([]byte, error) {
	if path != "" {
		return os.ReadFile(path)
	}
	if v := os.Getenv("FLEET_DEV_ANDROID_GOOGLE_SERVICE_CREDENTIALS"); v != "" {
		return []byte(v), nil
	}
	return nil, fmt.Errorf("provide -creds-file or set FLEET_DEV_ANDROID_GOOGLE_SERVICE_CREDENTIALS")
}

// deviceSummary is a trimmed view of androidmanagement.Device for
// list-devices output - the full struct is large and mostly noise for the
// purpose of finding a device's resource name and serial to target a
// command at.
type deviceSummary struct {
	Name         string `json:"name"`
	SerialNumber string `json:"serial_number,omitempty"`
	State        string `json:"state,omitempty"`
	Model        string `json:"model,omitempty"`
}

func listDevices(mgmt *androidmanagement.Service, enterpriseID string) {
	result, err := mgmt.Enterprises.Devices.List("enterprises/" + enterpriseID).Do()
	if err != nil {
		fatalf("list devices: %v", err)
	}
	if len(result.Devices) == 0 {
		fmt.Println("no devices found in this enterprise")
		return
	}

	summaries := make([]deviceSummary, 0, len(result.Devices))
	for _, d := range result.Devices {
		s := deviceSummary{Name: d.Name, State: d.State}
		if d.HardwareInfo != nil {
			s.SerialNumber = d.HardwareInfo.SerialNumber
			s.Model = d.HardwareInfo.Model
		}
		summaries = append(summaries, s)
	}

	b, err := json.MarshalIndent(summaries, "", "  ")
	if err != nil {
		fatalf("marshal devices: %v", err)
	}
	fmt.Println(string(b))
	fmt.Fprintf(os.Stderr, "\n%d device(s). Match serial_number against the hardware serial shown for the host in Fleet, then use `name` as -device-name.\n", len(summaries))
}

// createWebApp mirrors ee/server/service/vpp.go's CreateAndroidWebApp
// (the ee wrapper around this is just a thin auth+icon-validation layer over
// this exact API call - androidmanagement.WebApp{DisplayMode: "STANDALONE", ...}).
func createWebApp(mgmt *androidmanagement.Service, enterpriseID, title, startURL string) {
	if title == "" {
		fatalf("-title is required")
	}
	parsedURL, err := url.Parse(startURL)
	if err != nil || !parsedURL.IsAbs() {
		fatalf("-url must be a valid absolute URL")
	}

	webApp := &androidmanagement.WebApp{
		DisplayMode: "STANDALONE",
		Title:       title,
		StartUrl:    startURL,
	}

	enterpriseName := "enterprises/" + enterpriseID
	created, err := mgmt.Enterprises.WebApps.Create(enterpriseName, webApp).Do()
	if err != nil {
		fatalf("create web app: %v", err)
	}

	// created.Name is "enterprises/{id}/webApps/{packageName}" - the package
	// name isn't returned as its own field (matches ee/server/service/vpp.go's
	// CreateAndroidWebApp, which derives it the same way).
	packageName := strings.TrimPrefix(created.Name, enterpriseName+"/webApps/")

	fmt.Printf("OK: web app created\nname: %s\npackageName: %s\n", created.Name, packageName)
	fmt.Println("Add this packageName to the enterprise's Policy (applications list) to push it to devices.")
}

// checkOperation polls the AMAPI long-running operation behind a previously
// issued command. Fleet itself learns command outcomes asynchronously via a
// Pub/Sub COMMAND notification (server/mdm/android/service.go's
// ProcessPubSubPush) - this tool has no Pub/Sub subscriber, so this is the
// direct equivalent: Operation.Done/Error mirror what that notification would
// have carried. Note Done=true only means AMAPI finished processing the
// command server-side (e.g. delivered to the device's command queue), not
// that the device necessarily applied the change - some command types
// (RESET_PASSWORD in particular) can be silently ignored by the device/OS
// policy with no error surfaced here.
func checkOperation(mgmt *androidmanagement.Service, opName string) {
	if opName == "" {
		fatalf("-operation is required (the 'operation:' line printed by lock/wipe/clear-passcode)")
	}
	op, err := mgmt.Enterprises.Devices.Operations.Get(opName).Do()
	if err != nil {
		fatalf("get operation: %v", err)
	}
	b, err := json.MarshalIndent(op, "", "  ")
	if err != nil {
		fatalf("marshal operation: %v", err)
	}
	fmt.Println(string(b))
}

func issueCommand(mgmt *androidmanagement.Service, deviceName string, cmd *androidmanagement.Command) {
	op, err := mgmt.Enterprises.Devices.IssueCommand(deviceName, cmd).Do()
	if err != nil {
		fatalf("issue command %s: %v", cmd.Type, err)
	}
	fmt.Printf("OK: %s command issued to %s\n", cmd.Type, deviceName)
	if op != nil {
		fmt.Printf("operation: %s\n", op.Name)
	}
}
