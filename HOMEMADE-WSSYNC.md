# Notebook — sync Google Workspace maison (upgrade de usermapper, Phase 5)

Suite de [HOMEMADE-USERMAPPER.md](HOMEMADE-USERMAPPER.md) (Phase 3). Remplace la source CSV manuelle par une vraie synchronisation Google Workspace — `tools/homemade-mdm/wssync/` produit le même format de CSV que `usermapper` consomme déjà, donc **zéro modification** de `usermapper`.

Réutilise le même flow que `ee/server/googleworkspace/google_workspace.go` (JWT + domain-wide delegation via `golang.org/x/oauth2/jwt` et `google.golang.org/api/admin/directory/v1`, tous deux MIT/tiers) — sans importer le package `ee/`.

---

## Étape 1 — activer la délégation domain-wide (Workspace Admin Console)

Nécessite les droits **Super Admin** sur le Workspace.

1. Aller sur **https://admin.google.com** → **Sécurité → Contrôle des accès et des données → Délégation au niveau du domaine**
2. **Ajouter un nouveau** : renseigner le **Client ID** du service account GCP (celui de `mobisoft-mdm` déjà créé en Phase 4, ou un nouveau dédié — le Client ID est le champ `client_id` du fichier credentials JSON)
3. Coller les 3 scopes en lecture seule, séparés par des virgules :
   ```
   https://www.googleapis.com/auth/admin.directory.user.readonly,https://www.googleapis.com/auth/admin.directory.group.readonly,https://www.googleapis.com/auth/admin.directory.group.member.readonly
   ```
4. Autoriser

---

## Étape 2 — build et exécution

```bash
alias fleetgo='docker run --rm -e GOTOOLCHAIN=auto -v /Users/macbookpro/fleet:/src -w /src golang:1.24'
fleetgo go build -o /src/tools/homemade-mdm/wssync/wssync ./tools/homemade-mdm/wssync/
```

```bash
./tools/homemade-mdm/wssync/wssync \
  -creds-file tools/homemade-mdm/androidcmd/keys/credentials.json \
  -domain <ton-domaine-workspace.com> \
  -admin-email <ton-admin>@<ton-domaine-workspace.com> \
  -out tools/homemade-mdm/wssync/rows.csv
```

- [ ] `N user(s) found in <domaine>` affiché sur stderr
- [ ] `rows.csv` généré avec deux colonnes : `email,email` par défaut (voir note ci-dessous)

**Testé le 2026-07-29** : build validé (le retry automatique a absorbé un flake réseau transitoire sur le module proxy). Pas encore testé contre un vrai domaine Workspace dans cette session — étape suivante.

---

## ⚠️ Étape 3 — corriger la colonne "identifier" avant de lancer usermapper

`wssync` ne connaît que Google Workspace — il ignore tout des hostnames Fleet. Par défaut, la colonne `identifier` du CSV contient l'**email** (ou le nom complet avec `-identifier-field username`), **pas** un identifiant Fleet valide.

`usermapper` résout chaque ligne via `GET /hosts/identifier/{id}` (hostname, UUID, serial, osquery_host_id) — il faut donc éditer `rows.csv` pour remplacer la colonne `identifier` par le bon identifiant Fleet de chaque device, avant de lancer `usermapper` dessus. Pas d'automatisation de cette corrélation dans ce v1 (dépend entièrement de la convention de nommage des devices dans l'org).

---

## Étape 4 — chaîner avec usermapper (déjà validé en Phase 3)

```bash
./tools/homemade-mdm/usermapper/usermapper \
  -fleet-url https://boundless-arson-versus.ngrok-free.dev \
  -fleet-token <token-api-only> \
  -csv tools/homemade-mdm/wssync/rows.csv
```

---

## Pièges classiques

- **Client ID GCP mal collé dans la délégation domain-wide** → erreur `unauthorized_client` côté Google au premier appel ; revérifier le `client_id` exact du fichier credentials JSON, pas le `client_email`
- **Admin email impersonné qui n'est pas Super Admin** → l'API Directory refuse, il faut un vrai compte avec les droits d'admin
- **CSV envoyé tel quel à usermapper sans corriger la colonne identifier** → tous les hosts échouent en `resolve host: http 404` (voir étape 3)
