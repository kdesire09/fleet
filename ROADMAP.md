# ROADMAP — Prise en main du projet Fleet (Backend Go & Orbit)

Feuille de route pour monter en compétence sur deux volets du projet : le **serveur Fleet (Go)** et **Orbit** (l'agent déployé sur les devices). Chaque étape renvoie vers des fichiers réels du repo plutôt que de dupliquer la doc existante.

Doc de référence à garder ouverte en parallèle : [`docs/Contributing/getting-started/README.md`](docs/Contributing/getting-started/README.md) et [`docs/Contributing/architecture/high-level-architecture.md`](docs/Contributing/architecture/high-level-architecture.md).

---

## Phase 0 — Environnement (jour 1)

- [ ] Lire [`docs/Contributing/getting-started/building-fleet.md`](docs/Contributing/getting-started/building-fleet.md)
- [ ] `make deps` puis `make build` (build de `fleet` + `fleetctl`)
- [ ] `make db-reset` puis `make serve` (ou `make up`) — serveur dev qui tourne
- [ ] `make generate-dev` — webpack en mode watch pour le frontend (utile même si le focus est backend, pour voir l'UI répondre)
- [ ] Lire [`docs/Contributing/getting-started/testing-and-local-development.md`](docs/Contributing/getting-started/testing-and-local-development.md)
- [ ] Vérifier que `go test ./server/fleet/...` passe (tests sans dépendances externes)

---

## Phase 1 — Backend Go : point d'entrée, workflow, protocoles, patterns, config (semaine 1)

### 1.0 Point d'entrée du binaire

Le binaire `fleet` (`cmd/fleet/`) est une CLI Cobra avec plusieurs sous-commandes, câblées dans `cmd/fleet/main.go:28` (`func main()`) :

```
main() → createRootCmd() → rootCmd.AddCommand(...)
                              ├── serve          (cmd/fleet/serve.go)   → LE serveur HTTP/API — celui qu'on veut comprendre
                              ├── prepare         (cmd/fleet/prepare.go) → migrations DB (fleet prepare db)
                              ├── config_dump     (cmd/fleet/config_dump.go) → dump la config effective résolue
                              ├── version
                              └── vuln_processing (cmd/fleet/vuln_process.go) → traitement vuln en tâche isolée
```

`config.NewManager(rootCmd)` (voir 1.5) est créé **avant** l'ajout des sous-commandes : chaque sous-commande enregistre ses propres flags dessus.

Le vrai point d'entrée du serveur est `createServeCmd` dans **`cmd/fleet/serve.go`** (1400 lignes — le fichier de bootstrap le plus important du repo). Séquence de démarrage (dans l'ordre où on la trouve dans le fichier) :

1. Chargement de la config (`configManager.LoadConfig()`)
2. Init logger (`slog`), tracing OpenTelemetry/APM
3. Connexion MySQL (`datastore.go` → `ds fleet.Datastore`) et Redis (`redis.go`)
4. Construction du `fleet.Service` (implémentation concrète du gros service, wrappée par `ee/` si licence premium, puis par `service.NewMetricsService` pour Prometheus)
5. `service.MakeHandler(...)` → construit le routeur HTTP principal de l'API (`apiHandler`, voir 1.1)
6. Enregistrement des protocoles additionnels sur un second routeur (`rootMux`) : MDM Apple, SCEP, SCIM, gRPC launcher, frontend SPA, healthz/metrics (voir 1.3)
7. `srv := config.Server.DefaultHTTPServer(ctx, handler)` puis `srv.ListenAndServe()` / `ListenAndServeTLS()`

- [ ] Lire `cmd/fleet/main.go` en entier (court, ~100 lignes utiles)
- [ ] Parcourir `cmd/fleet/serve.go` une première fois en suivant uniquement les commentaires de section, sans rentrer dans le détail
- [ ] Lancer `go run ./cmd/fleet config_dump` (ou `./build/fleet config_dump` après `make build`) pour voir la config résolue réelle

### 1.1 Le workflow d'exécution d'une requête API

Fleet utilise **go-kit** (`github.com/go-kit/kit/transport/http`) par-dessus **gorilla/mux** pour le routage. Le pattern go-kit sépare strictement 3 responsabilités par endpoint : `DecodeRequestFunc`, `Endpoint` (logique), `EncodeResponseFunc`. Fleet ajoute une 4e étape : l'authentification, injectée comme middleware d'`Endpoint`.

```
requête HTTP entrante
   │
   ▼
gorilla/mux router (server/service/handler.go:151, r := mux.NewRouter())
   │  → matche la route sur méthode + path (ex: POST /api/_version_/fleet/hosts)
   ▼
kithttp.ServerOption "Before" (server/service/handler.go:135-141)
   │  1. kithttp.PopulateRequestContext   — infos de requête dans le ctx
   │  2. auth.SetRequestsContexts(svc)     — extrait le token (cookie/Bearer) dans le ctx, PAS encore vérifié
   │  3. endpointer.LogDeprecatedPathAlias
   │  4. setCarveStoreInRequestContext
   ▼
AuthMiddleware (spécifique au type d'endpointer, voir 1.2)
   │  → vérifie réellement les credentials, peuple viewer.Viewer{} dans le ctx si OK
   │  → sinon retourne fleet.NewAuthRequiredError / NewAuthFailedError AVANT d'atteindre le service
   ▼
DecodeRequestFunc (makeDecoder, server/service/endpoint_utils.go)
   │  → décode JSON body + path params (reflection sur les tags de la request struct)
   ▼
handlerFunc(ctx, request, svc) (server/service/*.go — l'"endpoint" métier)
   │  → appelle svc.XxxMethod(ctx, ...): AUTHZ (autz.Authorize) + logique métier
   │  → svc appelle ds.XxxMethod(ctx, ...) sur le Datastore (SQL, server/datastore/mysql/)
   ▼
EncodeResponseFunc (encodeResponse)
   │  → sérialise la response struct en JSON (ou l'erreur si response.Error() != nil)
   ▼
kithttp.ServerOption "After" (SetContentType, LogRequestEnd, checkLicenseExpiration)
   ▼
réponse HTTP
```

- [ ] Lire `server/service/handler.go:114-194` (`MakeHandler`) — c'est le point de montage de toute la chaîne ci-dessus
- [ ] Lire un exemple complet de bout en bout — CLAUDE.md recommande `server/service/vulnerabilities.go` comme référence, comparé à son enregistrement dans `handler.go` et son datastore dans `server/datastore/mysql/`
- [ ] Suivre `.claude/rules/fleet-api.md` pour le pattern request/response exact (`Err error` + `func (r xResponse) Error() error`)

### 1.2 Les "endpointers" — un modèle d'auth différent par type de client

`attachFleetAPIRoutes` (`server/service/handler.go:292`) ne crée pas un seul routeur mais **plusieurs "endpointers"**, chacun avec son propre `AuthMiddleware` (définis dans `server/service/endpoint_utils.go`). Comprendre lequel est utilisé où est la clé pour lire `handler.go` :

| Endpointer | Credential vérifié | Utilisé pour |
|---|---|---|
| `newUserAuthenticatedEndpointer` | session key (cookie ou `Authorization: Bearer`), vérifiée via `AuthViewer` → peuple `viewer.Viewer` | Toute l'API utilisateur (`/api/_version_/fleet/...`) : UI web, `fleetctl`, API publique |
| `newNoAuthEndpointer` | aucun | login, forgot-password, `/api/_version_/fleet/osquery/enroll`, healthz |
| `newHostAuthenticatedEndpointer` | `node_key` osquery (dans le body) | API TLS **native osquery** : `/api/osquery/config`, `/distributed/read`, `/distributed/write`, `/log` |
| `newDeviceAuthenticatedEndpointer` | token de device (Fleet Desktop) ou certificat client (header `X-Client-Cert-Serial`) | API Fleet Desktop (device-side, pas admin) |
| `newOrbitAuthenticatedEndpointer` | `node_key` **orbit** (différent du node_key osquery) | API orbit authentifiée (config, scripts, installers...) |
| `newOrbitNoAuthEndpointer` | aucun | enrollment orbit initial |
| `androidAuthenticatedEndpointer` | header `Node key ...` | API Android Enterprise |

Au-delà de ces endpointers go-kit, `serve.go` monte aussi des handlers **non-go-kit**, directement sur le `rootMux` racine (pas de pattern request/response Fleet, protocoles/libs tiers) — voir 1.3.

- [ ] Repérer dans `server/service/endpoint_utils.go:143-304` chaque fonction `new*Endpointer` et son `AuthMiddleware`
- [ ] Lire `server/service/middleware/auth/auth.go` (`AuthenticatedUser`, `AuthViewer`) pour le cas utilisateur
- [ ] Lire `server/service/endpoint_middleware.go` (`authenticatedHost`, `authenticatedDevice`, `authenticatedOrbitHost`) pour les 3 cas agent

### 1.3 Les différents protocoles servis par `fleet serve`

Un seul process Go, un seul port TCP en général, mais **plusieurs protocoles applicatifs** multiplexés sur le même `http.Server` (voir `cmd/fleet/serve.go:825-1020`, variable `rootMux`) :

| Protocole | Où c'est monté | Rôle |
|---|---|---|
| **REST JSON** (go-kit + gorilla/mux) | `rootMux.HandleFunc("/api/", ...)` → `apiHandler` | API principale (UI, fleetctl, GitOps, intégrations) |
| **API TLS native osquery** (JSON, protocole osquery historique) | endpointer `he`/`ne` dans `handler.go` (`/api/osquery/enroll`, `/config`, `/distributed/read|write`, `/log`) | Communication avec des agents osquery "purs" (sans orbit) |
| **gRPC** | `launcher.New(svc, ...)`, servi via `launcher.Handler(rootMux)` (`cmd/fleet/serve.go:816` et `1016-1018`) | Protocole [Kolide Launcher](server/launcher/) — un client osquery alternatif, multiplexé HTTP/gRPC sur le même port |
| **MDM Apple** (plist over HTTPS) | `service.RegisterAppleMDMProtocolServices(rootMux, ...)` (`cmd/fleet/serve.go:881`), s'appuie sur **nanomdm**/**nanodep** | Check-in et commandes MDM pour hosts Apple enrollés, + push via APNs |
| **SCEP** | `service.RegisterSCEPProxy` (proxy vers CA externe type NDES), `hostidentity.RegisterSCEP`, `condaccess.RegisterSCEP` — tous premium only | Émission de certificats d'identité pour hosts MDM |
| **SCIM** | `scim.RegisterSCIM(rootMux, ...)` — premium only | Provisioning d'utilisateurs (Okta/Entra → Fleet) |
| **WebSocket** | `server/websocket/`, utilisé par `server/service/endpoint_campaigns.go` | Live queries : streaming des résultats en temps réel vers l'UI (`GET /api/latest/fleet/queries/run`) |
| **HTTP simple** (pas d'auth Fleet) | `/healthz`, `/version`, `/metrics` (Prometheus, avec Basic Auth optionnelle), `/assets/` | Ops/monitoring |
| **SMTP** (sortant, pas servi par `fleet serve`) | `server/mail/` | Envoi d'emails (invitations, reset password) |

Protocole côté agent (traité en Phase 2/3, pas ici) : **TUF** pour l'autoupdate d'Orbit — c'est `updates.fleetdm.com` qui le sert, pas le serveur Fleet lui-même.

- [ ] Repérer dans `serve.go` la construction de `rootMux` (ligne ~825) et suivre chaque `rootMux.Handle(...)` un par un
- [ ] Comprendre pourquoi gRPC (launcher) et REST cohabitent sur le même listener (`launcher.Handler` fait le multiplexage protocole par inspection du premier byte / content-type)
- [ ] Si le sujet MDM/SCEP t'intéresse en profondeur, se référer à la Phase 3 du présent roadmap

### 1.4 Patterns clés du code de service

- **Request/Response struct** — voir `.claude/rules/fleet-api.md`. Toujours `Err error` + `Error() error` dans la response ; jamais `return nil, err` pour une erreur métier
- **Autorisation en deux temps** pour les entités scopées (team/fleet) — check générique puis check spécifique à l'entité chargée (voir `.claude/rules/fleet-go-backend.md`, section "Service Methods")
- **Viewer context** — `viewer.FromContext(ctx)` est la SEULE source fiable de l'identité de l'appelant ; ne jamais faire confiance à un user/ID dans le body de la requête
- **ctxerr.Wrap** — tout retour d'erreur venant d'un datastore ou d'un appel externe doit être wrappé avec du contexte
- **Bounded contexts** — `server/activity/` et `server/mdm/` dérogent au layering classique `fleet/`→`service/`→`datastore/` et gardent leurs propres types internes ; ne pas essayer de leur appliquer le pattern global
- **Datastore mocké** — `server/mock/` génère des mocks pour l'interface `fleet.Datastore` ; après ajout d'une méthode à l'interface, `go test ./server/service/` peut casser ailleurs si le mock n'est pas régénéré/complété
- **Enterprise wrapping** — `ee/server/service/` enveloppe le `Service` core avec des checks de licence ; un nouveau comportement premium ne se code jamais directement dans `server/service/`

### 1.5 Configuration — comment `FleetConfig` est résolue

`server/config/config.go` — gestion via **viper**, avec 3 sources et un ordre de priorité strict pour chaque clé :

```
flag CLI  >  variable d'env  >  fichier config (yaml)  >  valeur par défaut
```

Mécanique (voir `config.go:2232-2314`) :
- Chaque clé de config (ex. `mysql.address`) est enregistrée une fois via `addConfigString`/`addConfigInt`/... dans `addConfigs()`
- Le nom de flag CLI correspondant : `.` → `_` (`mysql.address` → `--mysql_address`)
- Le nom de variable d'env correspondant : préfixe **`FLEET_`** + `.` → `_` + majuscules (`mysql.address` → `FLEET_MYSQL_ADDRESS`) — c'est la méthode la plus utilisée en dev/prod/Docker
- Un fichier yaml peut être chargé (`--config` ou emplacement par défaut), lu par `viper.ReadInConfig()`

Sections principales à connaître (structs dans `config.go`) : `Server` (adresse, TLS, url_prefix), `Mysql`/`MysqlReadReplica`, `Redis`, `Auth`, `Session`, `Logging` (tracing OTEL/APM), `MDM`, `License`, `Prometheus`.

- [ ] Lancer `fleet config_dump` (ou `go run ./cmd/fleet config_dump`) et comparer avec le fichier `tools/` ou `.env` utilisé par `make serve` / `make up`
- [ ] Modifier une valeur via variable d'env (`FLEET_MYSQL_ADDRESS=...`) et confirmer via `config_dump` qu'elle prend le dessus sur le fichier de config
- [ ] Repérer où `config.MDM`, `config.Auth`, `config.License` sont lues dans `serve.go` pour comprendre quelles features elles activent/désactivent au boot

### 1.6 Les couches clés à cartographier

| Couche | Répertoire | Ce qu'il faut en retenir |
|---|---|---|
| Types & interfaces | `server/fleet/` | `Service` (service.go) et `Datastore` (datastore.go) — les deux interfaces centrales de tout le backend |
| Services (logique métier + auth) | `server/service/` (~2390 fichiers dans `server/`) | Chaque domaine = un ou plusieurs fichiers (`apple_mdm.go`, `hosts.go`, `policies.go`, `campaigns.go`...) |
| Datastore MySQL | `server/datastore/mysql/` | Requêtes SQL brutes, une paire `xxx.go` / `xxx_test.go` par domaine |
| Migrations | `server/datastore/mysql/migrations/tables/` (628 fichiers) | Historique du schéma — ne jamais modifier une migration mergée |
| Enterprise | `ee/server/service/` | Wrappe le service core + vérif de licence (`//go:build !premium` pour le core-only) |
| MDM | `server/mdm/` (apple, microsoft, android, scep, nanomdm, nanodep...) | Le sous-système le plus complexe — voir Phase 3 |
| Auth & policies d'accès | `server/authz/`, `server/contexts/` | Pattern d'autorisation utilisé dans chaque service method |
| Cron / jobs async | `server/cron/`, `server/worker/` | Traitements différés (sync Apple, nettoyage, etc.) |

### 1.7 Conventions à intégrer avant d'écrire du code
Ces conventions se chargent automatiquement via `.claude/rules/` quand on édite les fichiers correspondants — à lire une fois manuellement :
- [ ] `.claude/rules/fleet-go-backend.md` — wrapping d'erreurs `ctxerr`, types d'erreur, `slog`, pattern `new(expression)`
- [ ] `.claude/rules/fleet-api.md` — structs request/response, enregistrement d'endpoint
- [ ] `.claude/rules/fleet-database.md` — conventions SQL/migrations

### 1.8 Terminologie à connaître (piège classique)
- "Teams" → **"Fleets"** dans le produit, mais le code garde `team_id`, table `teams`, etc. (legacy)
- "Queries" → **"Reports"** dans le produit ; "query" ne désigne plus que le SQL brut

### 1.9 Exercice pratique de fin de phase
- [ ] Choisir un endpoint simple (ex. `GET /api/_version_/fleet/me`), tracer son chemin complet router → auth middleware → decode → service → datastore → SQL → encode
- [ ] Lancer `MYSQL_TEST=1 go test ./server/datastore/mysql/... -run TestHosts` (ou équivalent) pour voir un test d'intégration tourner
- [ ] Modifier `FLEET_MYSQL_ADDRESS` via env var, relancer, confirmer via `fleet config_dump` que ça a bien pris le dessus sur le fichier de config

---

## Phase 2 — Orbit : l'agent déployé sur le device (semaine 2)

### 2.1 Où se situe Orbit dans l'architecture
D'après `docs/Contributing/architecture/high-level-architecture.md`, l'agent installé sur le device (**fleetd**) regroupe trois composants :
- **orbit** — cœur de l'agent, gère la communication avec le serveur Fleet et l'autoupdate
- **osqueryd** — le daemon osquery qui exécute les requêtes
- **Fleet Desktop** — UI optionnelle côté utilisateur final

Orbit lui-même : *"a lightweight osquery installer and autoupdater"* — il peut être utilisé avec ou sans Fleet, et Fleet peut être utilisé avec ou sans Orbit ([`orbit/README.md`](orbit/README.md)).

### 2.2 Cartographie du code
| Répertoire | Rôle |
|---|---|
| `orbit/cmd/orbit/` | Point d'entrée du binaire orbit (`orbit.go`, `shell.go`) |
| `orbit/cmd/desktop/` | Binaire Fleet Desktop |
| `orbit/cmd/fetch_cert/`, `orbit/cmd/fleetd_tables/` | Binaires utilitaires annexes |
| `orbit/pkg/update/` | **Le cœur de l'autoupdater** : `runner.go`, `update.go`, `flag_runner.go`, `config_fetcher.go`, `selfheal.go`, `notifications.go`, `nudge.go` (intégration Nudge macOS) |
| `orbit/pkg/osquery/` | Gestion du processus osqueryd |
| `orbit/pkg/packaging/` | Génération des packages d'installation (pkg/msi/deb/rpm) |
| `orbit/pkg/installer/`, `orbit/pkg/setup_experience/` | Installation de logiciels et setup experience MDM |
| `orbit/pkg/bitlocker/`, `orbit/pkg/luks/`, `orbit/pkg/lvm/` | Chiffrement disque (Windows/Linux) |
| `orbit/pkg/execuser/`, `orbit/pkg/useraction/` | Exécution dans le contexte de la session utilisateur (nécessaire sur Windows notamment) |
| `orbit/pkg/windows/`, code suffixé `_darwin.go` / `_windows.go` / `_linux*.go` | Spécificités par plateforme — chercher systématiquement les variantes par OS |
| `orbit/TUF.md` | Versions actuellement déployées sur les canaux `stable`/`edge` (généré, ne pas éditer à la main) |

### 2.3 Le mécanisme d'update (TUF) — cycle détaillé

Orbit s'auto-met-à-jour via **TUF (The Update Framework)**, servi par défaut par `updates.fleetdm.com` (`DefaultURL` dans `orbit/pkg/update/update.go:57`). Deux types de fichiers Go portent tout le mécanisme :

- **`orbit/pkg/update/update.go`** → type `Updater`, bas niveau : parle au serveur TUF, télécharge, vérifie, extrait
- **`orbit/pkg/update/runner.go`** → type `Runner`, haut niveau : boucle qui décide *quand* et *quoi* mettre à jour, et applique le changement (symlink)

#### a) Les acteurs

| Composant | Rôle |
|---|---|
| `client.Client` (lib `theupdateframework/go-tuf`) | Implémentation TUF pure : vérifie les rôles `root`/`targets`/`snapshot`/`timestamp` et leurs signatures |
| `Updater` (`update.go`) | Wrapper Fleet autour du client TUF : gère le layout de fichiers local, le téléchargement, l'extraction `.tar.gz`, l'installation `.pkg` |
| `Runner` (`runner.go`) | Boucle de polling (`Execute()`), compare hash local vs hash distant, déclenche `updateTarget()` |
| `LocalStore` (`badgerstore`/`filestore`) | Persistance locale des métadonnées TUF déjà vérifiées (`updates-metadata.json`) |

#### b) Les targets suivies

Enregistrées dans `orbit/cmd/orbit/orbit.go` au démarrage, avec un canal indépendant par target (flags CLI `--orbit-channel`, `--osqueryd-channel`, `--desktop-channel`, défaut `stable`) :

- `orbit` (`constant.OrbitTUFTargetName`) — toujours suivi
- `osqueryd` (`constant.OsqueryTUFTargetName`) — toujours suivi
- `desktop` (`constant.DesktopTUFTargetName`) — suivi seulement si Fleet Desktop est activé
- targets additionnelles ajoutables dynamiquement à chaud via `Runner.AddRunnerOptTarget()` / `RemoveRunnerOptTarget()` (utilisé par ex. pour Nudge, swiftDialog, escrowBuddy — cf. `orbit/pkg/update/nudge.go`, `escrow_buddy.go`)

Chaque target est localisée dans le repo TUF par un chemin `target/platform/channel/fichier`, construit par `Updater.repoPath()` — ex. `orbit/macos/stable/orbit`.

#### c) Le cycle, étape par étape

1. **Bootstrap de la confiance** (`NewUpdater`) — au tout premier lancement (ou si les métadonnées locales sont absentes), le client TUF s'initialise avec des **clés root pinnées dans le binaire** (`defaultRootMetadata`, hardcodé dans `update.go`). C'est la racine de confiance : impossible de la falsifier sans recompiler orbit.
2. **`Runner.Execute()`** démarre une boucle `ticker` :
   - Premier check à un intervalle **randomisé** (`--update-interval`, défaut **15 min**, + jusqu'à 10 min de jitter aléatoire) pour éviter que toute la flotte ne tape le serveur TUF en même temps (thundering herd)
   - Chaque tick appelle `UpdateAction()`
3. **`Updater.UpdateMetadata()`** → `client.Update()` : télécharge/vérifie la chaîne TUF complète `timestamp → snapshot → targets → (delegated targets)`, en respectant l'ordre et les signatures à seuil (threshold). Erreur réseau = non fatal, on retente au prochain tick.
4. **Comparaison de hash** — pour chaque target suivie, `Runner` compare le hash local en cache (`localHashes`, calculé une seule fois au démarrage puis mis à jour après chaque update — pas recalculé à chaque tick, optimisation volontaire) au hash annoncé dans les métadonnées TUF fraîches.
5. **Si divergence** → `updateTarget()` :
   - `Updater.Get()` télécharge le fichier dans `staging/`, avec **retry limité** : 3 tentatives consécutives puis **cooldown de 24h** (`retry.NewLimitedWithCooldown(3, 24*time.Hour)`) — évite de marteler le serveur si un artefact est cassé
   - Vérification du hash **et de la taille max** par la lib go-tuf pendant le download
   - `checkExec()` : avant d'activer le binaire, orbit l'exécute réellement (`--help` ou `--version` selon le composant, ou une vérif custom comme `pkgutil` pour un `.pkg` macOS) pour s'assurer qu'il n'est pas corrompu
   - Si `.tar.gz` → extraction (avec protection anti **zip-slip**, cf. le check `strings.Contains(header.Name, "..")` dans `extractTarGz`)
   - Si `.pkg` macOS → installation via `installer -pkg ... -target /`
   - Le fichier remplace l'ancien via `os.Rename` (atomique côté FS)
6. **Application du changement** — pour `orbit` uniquement : le nouveau binaire est **symlinké** (`bin/orbit/<version>` → lien `bin/orbit/orbit`), pas copié en place. Ça permet un rollback simple et d'avoir plusieurs versions coexistantes sur disque.
7. **Redémarrage** — si le binaire `orbit` lui-même a été mis à jour, `Execute()` retourne (`didUpdate == true`) et le process manager (launchd/systemd/Windows service) relance orbit, qui redémarre alors sur le nouveau binaire. Pour `osqueryd`/`desktop`, pas de redémarrage d'orbit nécessaire — orbit relance juste le sous-processus.

#### d) Cas particuliers à connaître

- **Migration d'historique** : jusqu'à orbit 1.37, le fichier de métadonnées locales s'appelait `tuf-metadata.json` et pointait vers `tuf.fleetctl.com`. Depuis 1.38+, c'est `updates-metadata.json` vers `updates.fleetdm.com`. Le code gère la migration/génération à la volée (`OldFleetTUFURL`, `OldMetadataFileName` dans `update.go`).
- **Signatures expirées au démarrage** (`SignaturesExpiredAtStartup`) : si `root`/`targets`/`snapshot` a une signature expirée, orbit ne peut pas charger les targets. Le `Runner` bascule alors dans un mode dégradé où il *ne fait que* vérifier périodiquement si les signatures sont redevenues valides (nouveau root publié), sans jamais mettre à jour tant que ce n'est pas le cas.
- **TUF serveur injoignable au tout premier démarrage** (pas de métadonnées locales) : `NewRunner` accepte de démarrer quand même sans optimisation de hash, plutôt que de bloquer le boot d'orbit — compromis explicite pendant la période de migration d'URL.
- **Self-heal** (`orbit/pkg/update/selfheal.go`) : si le binaire local attendu a disparu/est corrompu, un mécanisme de fallback (`getComponentWithSelfHeal` dans `orbit.go`) retélécharge le composant.
- **TUF custom / self-hosted** : `RootKeys` dans `Options` permet de pointer vers un dépôt TUF maison (déploiements on-prem qui ne veulent pas dépendre de `updates.fleetdm.com`).
- **`--disable-updates`** : coupe entièrement le `Runner`, utile en dev ou pour des hosts gérés autrement.

#### e) Ce qui n'est PAS géré par TUF
Important pour ne pas confondre les deux mécanismes de synchronisation d'Orbit :
- **La config d'agent (flags osquery, options)** est récupérée via `orbit/pkg/update/flag_runner.go` et `config_fetcher.go`, qui interrogent **directement l'API Fleet** (pas TUF) à intervalle régulier — c'est un canal de configuration, pas de distribution de binaires.
- **Les commandes MDM / scripts / installs logiciels** transitent par l'API Fleet classique (`server/service/`), pas par le repo TUF.

- [ ] Lire `orbit/pkg/update/update.go` et `runner.go` en entier avec ce cycle en tête
- [ ] Comprendre les canaux `stable` vs `edge` et les versions actuellement déployées (voir `orbit/TUF.md`, généré — ne pas éditer à la main)
- [ ] Comprendre `orbit/pkg/update/badgerstore` et `filestore` (persistance locale des métadonnées TUF)
- [ ] Repérer dans `orbit/cmd/orbit/orbit.go` (autour de la ligne 675-700) le câblage `NewUpdater` → `NewRunner` → liste des targets

### 2.4 Build & test en local
- [ ] Lire [`docs/Contributing/getting-started/run-locally-built-fleetd.md`](docs/Contributing/getting-started/run-locally-built-fleetd.md)
- [ ] Builder orbit nativement (pas de cross-compile, voir `orbit/README.md`) :
  ```sh
  CGO_ENABLED=1 ORBIT_VERSION=dev ORBIT_BINARY_PATH=./orbit-macos \
    go run ./orbit/tools/build/build.go
  ```
- [ ] Faire tourner un orbit local pointant sur le serveur Fleet dev, observer l'enrollment côté UI

### 2.5 Exercice pratique de fin de phase
- [ ] Modifier un flag runner ou une notification, rebuild, observer le comportement sur un host local
- [ ] Identifier le point de contact exact entre Orbit et le backend Go (quel endpoint `server/service/` orbit appelle pour son check-in / config fetch)

---

## Phase 3 — Le pont Backend ↔ Orbit ↔ MDM (semaine 3)

C'est le sujet le plus complexe du projet — à aborder une fois les phases 1 et 2 acquises.

- [ ] Lire `docs/Contributing/architecture/mdm/` (architecture MDM détaillée)
- [ ] Comprendre `server/mdm/apple/` (nanomdm, nanodep, APNS) vs `server/mdm/microsoft/` (Autopilot, Entra JWT) vs `server/mdm/android/` (Android Management API, Pub/Sub)
- [ ] Comprendre le rôle de Fleet comme **SCEP proxy** (`server/mdm/scep/`) vers des CA externes
- [ ] Repérer où Orbit et fleetd interagissent avec ces flux (setup experience, disk encryption escrow, profils)

---

## Sujets transverses (en continu)

- [ ] **Tests** : `go test ./server/fleet/...` (rapide) → `MYSQL_TEST=1 go test ./server/datastore/mysql/...` → `MYSQL_TEST=1 REDIS_TEST=1 go test ./server/service/...`. Utiliser le skill `/find-related-tests` après une modif.
- [ ] **Lint** : `make lint-go-incremental` après chaque édition, `make lint-go` avant commit
- [ ] **Migrations** : `make migration name=CamelCaseName`, ou skill `/new-migration`
- [ ] **Nouvel endpoint** : skill `/new-endpoint`
- [ ] **PR** : la description doit partir de `.github/pull_request_template.md` (vérifié par CI `check-pr-template`)
- [ ] **Revue** : agents `go-reviewer` (proactif après édition Go) et `fleet-security-auditor` (auth/MDM/sécurité)

---

## Checklist de sortie ("prêt à contribuer")

- [ ] Je peux tracer un endpoint complet handler → service → datastore sans aide
- [ ] Je sais où se trouve la logique d'autoupdate d'Orbit et comment un canal `stable`/`edge` fonctionne
- [ ] Je sais builder et lancer un orbit local connecté à mon serveur Fleet dev
- [ ] Je connais la différence de traitement core (`server/`) vs enterprise (`ee/`)
- [ ] J'ai fait tourner les 3 principaux bundles de tests (`fast`, `mysql`, `service`) en local
- [ ] J'ai ouvert une première PR avec le template rempli et passé `make lint-go-incremental`
