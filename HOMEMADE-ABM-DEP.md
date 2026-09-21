# Notebook — ABM/DEP zero-touch maison (remplace Fleet Premium)

Suite de [MDM-SETUP.md](MDM-SETUP.md). Ce notebook remplace la section 3 ("ABM/DEP — nécessite Premium") par un flow 100% maison, conforme au plan approuvé (`~/.claude/plans/indexed-leaping-volcano.md`, Phase 1) : **zéro code `ee/`, zéro table gatée**, uniquement des appels directs à l'API Apple DEP + notre endpoint Fleet core déjà libre (`/api/mdm/apple/enroll`).

Outil : [`tools/homemade-mdm/depsync/`](tools/homemade-mdm/depsync/main.go) — CLI Go qu'on a écrit, qui réutilise les packages core MIT de Fleet (`server/mdm/nanodep/godep`, `.../client`, `.../storage/file`), les mêmes que Fleet utilise en interne, sans toucher à `ee/`.

⚠️ Pas de Go installé en local sur cette machine — toutes les commandes `go run`/`go build` ci-dessous doivent passer par Docker (voir Étape 0).

---

## Étape 0 — image de build Go (une seule fois)

On réutilise `golang:1.24` avec `GOTOOLCHAIN=auto` (le `go.mod` du repo demande Go 1.26.5, le toolchain se télécharge automatiquement au premier run).

```bash
alias fleetgo='docker run --rm -e GOTOOLCHAIN=auto -v /Users/macbookpro/fleet:/src -w /src golang:1.24'
```

---

## Étape 1 — générer le keypair et récupérer le token ABM chiffré

On réutilise l'outil `deptokens`, déjà présent dans `server/mdm/nanodep/cmd/deptokens/` (core, pas besoin de le réécrire).

```bash
mkdir -p tools/homemade-mdm/depsync/keys && cd tools/homemade-mdm/depsync/keys
fleetgo go run ../../../../server/mdm/nanodep/cmd/deptokens -cert cert.pem -key cert.key
```

- [ ] `cert.pem` et `cert.key` générés (validité **1 jour seulement** par défaut — enchaîner vite avec l'étape ABM ci-dessous)
- [ ] Aller sur **https://business.apple.com** (ou **https://school.apple.com**), se connecter en tant qu'admin de l'organisation
- [ ] **Settings → Your MDM Servers** (ou équivalent selon l'UI actuelle d'ABM) → **Add MDM Server**
- [ ] Donner un nom au serveur, uploader `cert.pem`
- [ ] Télécharger le token serveur chiffré (`.p7m`) proposé par ABM
- [ ] Assigner les devices voulus (par numéro de série) à ce serveur MDM dans ABM — **Add Devices**

---

## Étape 2 — déchiffrer le token

```bash
fleetgo go run ./server/mdm/nanodep/cmd/deptokens \
  -cert tools/homemade-mdm/depsync/keys/cert.pem \
  -key tools/homemade-mdm/depsync/keys/cert.key \
  -token <fichier-telecharge>.p7m > tools/homemade-mdm/depsync/keys/decrypted.json
```

- [ ] `decrypted.json` contient les champs `consumer_key`, `consumer_secret`, `access_token`, `access_secret`, `access_token_expiry`

---

## Étape 3 — importer le token dans le CLI maison

```bash
fleetgo go run ./tools/homemade-mdm/depsync \
  -storage-dir ./tools/homemade-mdm/depsync/data \
  import-config -token-json ./tools/homemade-mdm/depsync/keys/decrypted.json
```

- [ ] Sortie `OK: ABM auth tokens imported`
- [ ] Sanity check de l'authentification :
  ```bash
  fleetgo go run ./tools/homemade-mdm/depsync -storage-dir ./tools/homemade-mdm/depsync/data account-detail
  ```
  → doit renvoyer le JSON du compte ABM (`org_name`, `server_uuid`, etc.), pas une erreur d'auth

---

## Étape 4 — enregistrer le profil d'enrollment auprès d'Apple

Le profil pointe **directement vers l'endpoint core de Fleet**, sans SSO (`ConfigurationWebURL` volontairement omis dans `main.go` — l'utiliser déclencherait le flow MDM SSO qui est ee-gated).

```bash
fleetgo go run ./tools/homemade-mdm/depsync \
  -storage-dir ./tools/homemade-mdm/depsync/data \
  define-profile -fleet-url https://boundless-arson-versus.ngrok-free.dev -name "Fleet (homemade DEP)"
```

- [ ] Récupérer le `profile_uuid` retourné

---

## Étape 5 — assigner le profil aux devices

```bash
fleetgo go run ./tools/homemade-mdm/depsync \
  -storage-dir ./tools/homemade-mdm/depsync/data \
  assign -profile-uuid <uuid-etape-4> SERIAL1 SERIAL2
```

- [ ] Sortie `OK: all devices assigned successfully` (sinon le detail des échecs par serial s'affiche)

---

## Étape 6 — vérification

- [ ] Boot (ou reset) du device Apple assigné → il doit arriver directement sur l'écran Apple **Remote Management** du Setup Assistant, sans étape manuelle
- [ ] Le device s'enrolle en tapant `https://boundless-arson-versus.ngrok-free.dev/api/mdm/apple/enroll` — c'est notre endpoint core, déjà validé fonctionnel avec l'enrollment manuel
- [ ] Dans Fleet UI → **Hosts** → le host apparaît avec MDM **"On (automatic)"**
- [ ] `docker logs fleet-fleet-1 --tail 100` → confirmer que le check-in provient bien du endpoint `EnrollPath` core, aucune route `ee/` sollicitée

---

## Pièges classiques

- **Cert `deptokens` expiré (1 jour)** avant d'avoir uploadé sur ABM et téléchargé le token → refaire l'étape 1 depuis le début (regénérer un nouveau keypair)
- **`ConfigurationWebURL` renseigné par erreur** → bascule sur le flow MDM SSO ee-gated, casse tout ; le laisser vide dans `define-profile`
- **URL ngrok qui change** → il faut refaire `define-profile` avec la nouvelle URL et réassigner (le profil DEP référence l'URL en dur, pas de redirection dynamique)
- **Access token ABM expiré** (`access_token_expiry` dans `decrypted.json`) → il faut relancer tout le cycle de token ABM (étapes 1-3), l'auto-refresh OAuth1 de nanodep gère le renouvellement de session mais pas l'expiration du token ABM lui-même
