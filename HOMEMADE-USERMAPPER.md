# Notebook — mapping utilisateur↔host maison (remplace SCIM Fleet Premium, partiellement)

Phase 3 du plan approuvé (`~/.claude/plans/indexed-leaping-volcano.md`). Remplace la partie utile de SCIM par un outil qui alimente `PUT /api/v1/fleet/hosts/{id}/device_mapping` avec `source=custom` — **core, aucun check de licence**, confirmé lors de la recherche de faisabilité (`server/service/hosts.go`).

Outil : [`tools/homemade-mdm/usermapper/main.go`](tools/homemade-mdm/usermapper/main.go). Compile et tourne (vérifié).

## Limite assumée (à ne pas contourner)

Ça alimente uniquement l'email associé à un host (visible sur "My device"). Les champs `idp_full_name`/`idp_groups` restent vides — ils ne sont écrits que par le vrai SCIM EE (`source=idp`, explicitement gaté dans `server/service/hosts.go`). Le plan approuvé exclut d'écrire directement dans les tables `scim_*` pour avoir la parité complète (ce serait un contournement du check de licence, pas une réimplémentation).

## Pourquoi un CSV et pas une vraie intégration Okta/Entra live

Ce repo n'a pas de credentials Okta/Entra configurés pour construire et tester un vrai client OAuth contre un vrai tenant IdP. Le CSV permet de tester le bout-en-bout (résolution host → écriture device_mapping) dès maintenant. `readRows()` dans `main.go` est le seul point à remplacer pour brancher un vrai client IdP (même format de sortie `[]row{Identifier, Email}` à respecter) — swap isolé, documenté en commentaire dans le code.

---

## Étape 1 — créer un utilisateur API-only dans Fleet

```bash
docker exec fleet-fleet-1 fleetctl login   # si pas déjà connecté en admin
docker exec fleet-fleet-1 fleetctl user create \
  --api-only \
  --name "Homemade usermapper" \
  --email usermapper@example.com \
  --global-role maintainer
```

- [ ] Noter le token retourné (ou le récupérer via **Settings → Users** dans l'UI, la ligne de l'utilisateur API-only affiche un bouton pour régénérer/copier son token)
- [ ] `maintainer` suffit pour `ActionWrite` sur les hosts (device_mapping) ; `admin` fonctionne aussi

---

## Étape 2 — préparer le CSV de test

Format : `identifier,email` (pas de header). `identifier` = n'importe quoi accepté par `GET /hosts/identifier/{id}` (hostname, UUID, hardware serial, osquery_host_id).

```bash
cat > tools/homemade-mdm/usermapper/rows.csv <<'EOF'
mon-macbook-test,alice@example.com
EOF
```

- [ ] Remplacer `mon-macbook-test` par le hostname réel d'un host déjà enrollé dans ta Fleet (visible dans **Hosts**)

---

## Étape 3 — build et dry-run

```bash
alias fleetgo='docker run --rm -e GOTOOLCHAIN=auto -v /Users/macbookpro/fleet:/src -w /src golang:1.24'
fleetgo go build -o /src/tools/homemade-mdm/usermapper/usermapper ./tools/homemade-mdm/usermapper/

./tools/homemade-mdm/usermapper/usermapper \
  -fleet-url https://boundless-arson-versus.ngrok-free.dev \
  -fleet-token <ton-token-api-only> \
  -csv tools/homemade-mdm/usermapper/rows.csv \
  -dry-run
```

- [ ] Sortie attendue : `DRY-RUN would map host_id=<N> (mon-macbook-test) -> email=alice@example.com`
- [ ] Si `resolve host: http 404` → vérifier le hostname exact dans l'UI Fleet (identifiants sensibles à la casse/exacts)

---

## Étape 4 — exécution réelle

```bash
./tools/homemade-mdm/usermapper/usermapper \
  -fleet-url https://boundless-arson-versus.ngrok-free.dev \
  -fleet-token <ton-token-api-only> \
  -csv tools/homemade-mdm/usermapper/rows.csv
```

- [ ] Sortie `OK host_id=<N> ... -> email=alice@example.com`, puis `done: 1 ok, 0 failed`

---

## Étape 5 — vérification

- [ ] Dans Fleet UI, ouvrir le host ciblé → l'email `alice@example.com` apparaît dans la section device mapping / "My device"
- [ ] Ou via API : `GET /api/v1/fleet/hosts/{id}/device_mapping` → l'entrée `{"email": "...", "source": "custom"}` apparaît

**Testé le 2026-07-23** contre un host réel (`homemade-test-host-01`, id=1, créé via `POST /api/v1/osquery/enroll` avec l'enroll secret existant, sans agent réel) : dry-run puis run réel tous les deux réussis, confirmé via `GET .../device_mapping`.

---

## Pour brancher une vraie source Okta/Entra plus tard

Remplacer `readRows()` par un appel à l'API Users d'Okta (`GET /api/v1/users`) ou Microsoft Graph (`GET /v1.0/users`), en gardant la même sortie `[]row`. Tourner ça en cron/tâche planifiée pour un sync périodique plutôt qu'un run manuel.
