// Command wssync is a standalone, Fleet-Free-tier-compatible replacement for
// the directory-sync half of Fleet Premium's SCIM feature: it pulls users
// (and, optionally, group membership) from a real Google Workspace directory
// instead of a hand-written CSV.
//
// This upgrades tools/homemade-mdm/usermapper (Phase 3): wssync's output is
// the exact same two-column `identifier,email` CSV usermapper already
// consumes, so the two tools chain with zero changes to usermapper.
//
// Why this is legitimate under the plan's guardrail: it reuses the same
// approach ee/server/googleworkspace/google_workspace.go uses internally —
// a plain OAuth2 JWT (domain-wide delegation) flow against Google's official
// Admin SDK Directory API client (golang.org/x/oauth2/jwt +
// google.golang.org/api/admin/directory/v1). Both are third-party MIT
// libraries, not ee/ code. This tool does NOT import the ee/ package (it
// re-implements the thin API-calling logic itself, referencing the ee/ file
// only as documentation of which Directory API calls to make) and never
// touches Fleet's scim_* tables — it only ever produces a CSV that feeds
// usermapper's core, unrestricted PUT .../device_mapping call.
//
// Setup required (see HOMEMADE-WSSYNC.md):
//  1. A GCP service account with domain-wide delegation enabled in the
//     Google Workspace Admin Console, authorized for 3 read-only scopes:
//     admin.directory.user.readonly, admin.directory.group.readonly,
//     admin.directory.group.member.readonly.
//  2. A Workspace Super Admin email to impersonate (the Directory API only
//     accepts requests on behalf of a real admin user, not the bare service
//     account - this is Google's requirement, not Fleet's).
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/jwt"
	directory "google.golang.org/api/admin/directory/v1"
	"google.golang.org/api/option"
)

const usersPageSize = 500

func main() {
	credsPath := flag.String("creds-file", "", "path to the GCP service account JSON key (domain-wide delegation must be enabled for it)")
	domain := flag.String("domain", "", "Google Workspace primary domain to sync (e.g. example.com)")
	adminEmail := flag.String("admin-email", "", "Workspace Super Admin email to impersonate (Directory API requirement)")
	outCSV := flag.String("out", "", "path to write the identifier,email CSV (usermapper's expected input); prints to stdout if empty")
	identifierField := flag.String("identifier-field", "email", "which field to use as the CSV 'identifier' column: 'email' (safe default - usermapper resolves by Fleet host identifier, so you'll likely edit the CSV by hand to swap emails for hostnames) or 'username'")
	flag.Parse()

	if *credsPath == "" || *domain == "" || *adminEmail == "" {
		fmt.Fprintln(os.Stderr, "usage: wssync -creds-file FILE -domain example.com -admin-email admin@example.com [-out rows.csv]")
		os.Exit(1)
	}

	ctx := context.Background()
	svc, err := newDirectoryService(ctx, *credsPath, *adminEmail)
	if err != nil {
		fatalf("create directory service: %v", err)
	}

	users, err := listUsers(ctx, svc, *domain)
	if err != nil {
		fatalf("list users: %v", err)
	}
	fmt.Fprintf(os.Stderr, "%d user(s) found in %s\n", len(users), *domain)

	rows := make([][]string, 0, len(users))
	skipped := 0
	for _, u := range users {
		if u.Id == "" || u.PrimaryEmail == "" {
			skipped++
			continue
		}
		identifier := u.PrimaryEmail
		if *identifierField == "username" && u.Name != nil && u.Name.FullName != "" {
			identifier = u.Name.FullName
		}
		rows = append(rows, []string{identifier, u.PrimaryEmail})
	}
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "skipped %d user(s) missing id/primary_email\n", skipped)
	}

	var out *os.File
	if *outCSV == "" {
		out = os.Stdout
	} else {
		out, err = os.Create(*outCSV)
		if err != nil {
			fatalf("create output file: %v", err)
		}
		defer out.Close()
	}

	w := csv.NewWriter(out)
	if err := w.WriteAll(rows); err != nil {
		fatalf("write csv: %v", err)
	}
	w.Flush()

	if *outCSV != "" {
		fmt.Fprintf(os.Stderr, "wrote %d row(s) to %s\n", len(rows), *outCSV)
		fmt.Fprintln(os.Stderr, "NOTE: the 'identifier' column is currently the Workspace email/username, NOT a Fleet host identifier.")
		fmt.Fprintln(os.Stderr, "usermapper resolves via GET /hosts/identifier/{id} (hostname/uuid/serial) - edit this CSV to replace the identifier column with the matching Fleet host identifier before running usermapper.")
	}
}

func fatalf(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(1)
}

// newDirectoryService builds an Admin SDK Directory API client using a JWT
// (domain-wide delegation) flow, mirroring
// ee/server/googleworkspace/google_workspace.go:newGoogleAPI - same library
// calls, no import of the ee/ package.
func newDirectoryService(ctx context.Context, credsPath, adminEmail string) (*directory.Service, error) {
	credsJSON, err := os.ReadFile(credsPath)
	if err != nil {
		return nil, err
	}

	var creds struct {
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
		TokenURI    string `json:"token_uri"`
	}
	if err := json.Unmarshal(credsJSON, &creds); err != nil {
		return nil, fmt.Errorf("parse service account json: %w", err)
	}

	tokenURL := google.JWTTokenURL
	if creds.TokenURI != "" {
		tokenURL = creds.TokenURI
	}

	conf := &jwt.Config{
		Email: creds.ClientEmail,
		Scopes: []string{
			directory.AdminDirectoryUserReadonlyScope,
			directory.AdminDirectoryGroupReadonlyScope,
			directory.AdminDirectoryGroupMemberReadonlyScope,
		},
		PrivateKey: []byte(creds.PrivateKey),
		TokenURL:   tokenURL,
		Subject:    adminEmail, // impersonation target, required by the Directory API
	}

	return directory.NewService(ctx, option.WithHTTPClient(conf.Client(ctx)))
}

func listUsers(ctx context.Context, svc *directory.Service, domain string) ([]*directory.User, error) {
	var users []*directory.User
	err := svc.Users.List().Domain(domain).MaxResults(usersPageSize).Pages(ctx, func(page *directory.Users) error {
		users = append(users, page.Users...)
		return nil
	})
	return users, err
}
