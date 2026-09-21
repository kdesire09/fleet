// Command usermapper is a standalone, Fleet-Free-tier-compatible partial
// replacement for Fleet Premium's SCIM (IdP user provisioning) feature.
//
// It does NOT touch ee/server/scim or the scim_* MySQL tables (both are
// license-gated: server/service/scim.go's core stub always returns
// fleet.ErrMissingLicense, and ee/server/scim is only mounted on the HTTP
// mux `if license.IsPremium()`). Instead it drives Fleet's CORE, unrestricted
// device-mapping API - PUT /api/v1/fleet/hosts/{id}/device_mapping with
// source=custom - which is exactly what SetOrUpdateCustomHostDeviceMapping
// writes into the host_emails table (server/service/hosts.go, no license
// check for source=custom).
//
// Trade-off (documented, not hidden): this populates email-to-host mapping
// only, visible on the host's "My device" page. It does NOT populate
// idp_full_name/idp_groups - those fields are written exclusively by the
// real SCIM path (source=fleet.DeviceMappingIDP, explicitly gated). Full
// parity would require writing into scim_* tables directly, which the
// approved plan excludes as a license-check circumvention.
//
// Input is a simple CSV (identifier,email) rather than a live Okta/Entra
// client: this repo has no IdP credentials configured to build/test a real
// OAuth-authenticated poller against. Swapping the CSV for a real IdP API
// call is a small, isolated change (see readRows below) once you have your
// tenant's credentials.
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type row struct {
	Identifier string // hostname, uuid, hardware_serial, or osquery_host_id - anything Fleet's /hosts/identifier/{id} endpoint accepts
	Email      string
}

func main() {
	fleetURL := flag.String("fleet-url", "", "public HTTPS URL of the Fleet server")
	token := flag.String("fleet-token", "", "Fleet API token (from `fleetctl user create --api-only`, or FLEET_TOKEN env var)")
	csvPath := flag.String("csv", "", "path to CSV file with columns: identifier,email (no header row)")
	insecure := flag.Bool("insecure", false, "skip TLS verification (testing only)")
	dryRun := flag.Bool("dry-run", false, "resolve hosts and print what would be sent, without calling PUT device_mapping")
	flag.Parse()

	if *token == "" {
		*token = os.Getenv("FLEET_TOKEN")
	}
	if *fleetURL == "" || *token == "" || *csvPath == "" {
		fmt.Fprintln(os.Stderr, "usage: usermapper -fleet-url https://... -fleet-token TOKEN -csv rows.csv [-dry-run]")
		os.Exit(1)
	}

	rows, err := readRows(*csvPath)
	if err != nil {
		fatalf("read csv: %v", err)
	}

	httpc := &http.Client{Timeout: 15 * time.Second}
	if *insecure {
		httpc.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec
	}
	client := &httpClient{
		baseURL: strings.TrimRight(*fleetURL, "/"),
		token:   *token,
		http:    httpc,
	}

	var okCount, failCount int
	for _, r := range rows {
		hostID, err := client.resolveHostID(r.Identifier)
		if err != nil {
			fmt.Fprintf(os.Stderr, "SKIP %s (%s): resolve host: %v\n", r.Identifier, r.Email, err)
			failCount++
			continue
		}

		if *dryRun {
			fmt.Printf("DRY-RUN would map host_id=%d (%s) -> email=%s\n", hostID, r.Identifier, r.Email)
			okCount++
			continue
		}

		if err := client.setDeviceMapping(hostID, r.Email); err != nil {
			fmt.Fprintf(os.Stderr, "FAIL %s (%s): %v\n", r.Identifier, r.Email, err)
			failCount++
			continue
		}
		fmt.Printf("OK host_id=%d (%s) -> email=%s\n", hostID, r.Identifier, r.Email)
		okCount++
	}

	fmt.Printf("\ndone: %d ok, %d failed\n", okCount, failCount)
	if failCount > 0 {
		os.Exit(1)
	}
}

func fatalf(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(1)
}

// readRows parses a simple two-column CSV (identifier,email). Swap this out
// for a real IdP client (Okta Users API, Microsoft Graph /users, etc.) that
// returns the same []row shape to go from "file-based test" to "live sync".
func readRows(path string) ([]row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = 2
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}

	rows := make([]row, 0, len(records))
	for _, rec := range records {
		identifier := strings.TrimSpace(rec[0])
		email := strings.TrimSpace(rec[1])
		if identifier == "" || email == "" {
			continue
		}
		rows = append(rows, row{Identifier: identifier, Email: email})
	}
	return rows, nil
}

type httpClient struct {
	baseURL string
	token   string
	http    *http.Client
}

type hostByIdentifierResponse struct {
	Host struct {
		ID uint `json:"id"`
	} `json:"host"`
}

func (c *httpClient) resolveHostID(identifier string) (uint, error) {
	req, err := http.NewRequest(http.MethodGet,
		fmt.Sprintf("%s/api/v1/fleet/hosts/identifier/%s", c.baseURL, identifier), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}

	var out hostByIdentifierResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}
	if out.Host.ID == 0 {
		return 0, fmt.Errorf("host not found for identifier %q", identifier)
	}
	return out.Host.ID, nil
}

func (c *httpClient) setDeviceMapping(hostID uint, email string) error {
	payload, _ := json.Marshal(map[string]string{
		"email":  email,
		"source": "custom",
	})

	req, err := http.NewRequest(http.MethodPut,
		fmt.Sprintf("%s/api/v1/fleet/hosts/%d/device_mapping", c.baseURL, hostID), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
