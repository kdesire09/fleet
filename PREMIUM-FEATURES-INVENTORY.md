# Inventaire complet des fonctionnalités Fleet Premium (ee-gated)

Audit exhaustif du repo (3 agents de recherche en parallèle, un par domaine) pour identifier **toutes** les fonctionnalités gatées derrière `license.IsPremium()`, en vue de construire des équivalents maison — même démarche que les 4 phases déjà livrées (ABM/DEP, SCEP proxy, SCIM basique, commandes MDM Android).

**Mécanisme de gate, rappel** : `cmd/fleet/serve.go` n'enveloppe `fleet.Service` avec `ee/server/service` que `if license.IsPremium()`. Les stubs core dans `server/service/*.go` retournent `fleet.ErrMissingLicense` sans condition. Donc **le vrai code métier de `ee/`, quel qu'il soit, n'est jamais exécuté sur cette instance** — c'est le point de vigilance constant du garde-fou du plan.

**Légende buildability** :
- 🟢 **Facile** — logique déjà dans des libs core/MIT réutilisables, peu de couplage DB Fleet
- 🟡 **Moyen** — protocole/lib réutilisable, mais un peu de state/DB Fleet à recréer soi-même
- 🔴 **Difficile** — protocole propriétaire complexe (JWT/crypto fine, Secure Enclave...) à réimplémenter correctement
- ⚫ **Non extractible proprement** — logique métier profondément couplée au schéma DB Fleet (teams, software_installers...), pas de "vrai" protocole externe à appeler directement

---

## A. MDM Device Management (Apple / Windows)

| Feature | Protocole | Buildability | Détail |
|---|---|---|---|
| **Lock / Wipe / Clear passcode (Apple)** | Commandes MDM plist natives (`DeviceLock`/`EraseDevice`) | ⚫ | ~~🟢~~ **Corrigé après vérification approfondie** : contrairement à Android, Fleet EST le serveur MDM (pas un relai vers un tiers) — pas de vraie API externe à appeler directement. Pire : `server/service/mdm.go:676-694` montre un **double blocage délibéré** — même le endpoint générique core `RunMDMCommand` (`fleetctl mdm run-command`) vérifie explicitement `license.IsPremium()` pour `EraseDevice`/`DeviceLock`/`ClearPasscode` spécifiquement, précisément pour empêcher ce contournement. Pas un candidat viable sans violer le garde-fou du plan. |
| **FileVault escrow (macOS) / BitLocker (Windows)** | Payload DDM FileVault + CSP BitLocker | 🟡 | `server/service/apple_mdm.go:4125`, `mdm.go:3320` / `ee/server/service/apple_mdm.go:150,176` — payload simple, mais stockage/déchiffrement de la clé d'escrow couplé aux tables Fleet |
| **Bootstrap package (DEP zero-touch installer)** | Commande MDM `InstallEnterpriseApplication` | 🟢 | `apple_mdm.go:3638+` / `ee/apple_mdm.go:382-556` — commande MDM + stockage blob, peu de logique propriétaire |
| **EULA enrollment** | Aucun (fichier statique servi) | 🟢 | `mdm.go:306+` / `ee/apple_mdm.go:557-641` — CRUD blob trivial |
| **Setup Assistant / profil DEP custom + MDM SSO à l'enrollment** | API DEP Apple (`godep`, déjà core) + SAML | 🟡 | `apple_mdm.go:3833+` / `ee/apple_mdm.go:642-1182` — la partie DEP réutilise `godep` (comme notre Phase 1), mais SSO/end-user-auth dépend des sessions Fleet |
| **Custom OS-updates enforcement (Apple + Windows)** | Déclaration DDM `softwareupdate.enforcement` / CSP Windows `Update/ApprovedUpdates` | 🟡 | `ee/mdm.go:1452,1515,1537` — génération de payload simple, persistance liée aux team settings |
| **DDM Assets (blobs référencés par déclarations)** | Spec DDM Apple | 🟢 | `apple_mdm.go:4157+` / `ee/apple_mdm.go:90-311` — CRUD blob + activity log |

## B. Certificats & Identité device

| Feature | Protocole | Buildability | Détail |
|---|---|---|---|
| **CA custom : DigiCert** | API REST DigiCert (`/mpki/api/v2/certificate`) | 🟡 | `ee/server/service/digicert/digicert.go:45` — client Go autonome, léger, quasi drop-in |
| **CA custom : EST (NDES via proxy)** | RFC 7030 EST | 🟡 | `ee/server/service/est/est.go:31,67,98` — idem, package autonome |
| **CA custom : SCEP générique (Smallstep etc.)** | RFC 8894 SCEP | 🟢 | Réutilise `server/mdm/scep` (déjà core) — **c'est exactement notre Phase 2**, déjà fait |
| **Conditional Access (Okta device trust)** | SAML 2.0 IdP + SCEP | 🟡 | `ee/server/service/condaccess/` — `crewjam/saml` (MIT) + SCEP core, storage cert lié à `mdm_config_assets` |
| **Host Identity certs (mTLS host auth)** | SCEP + HTTP Message Signatures | 🟡 | `ee/server/service/hostidentity/` — SCEP réutilisable, middleware httpsig couplé au pipeline auth orbit |
| **Platform SSO (Apple PSSO)** | Protocole Apple natif + JWT/JOSE/OIDC ROPG | 🔴 | `ee/server/service/apple_psso*.go` (941 lignes) — crypto fine, gestion Secure Enclave, state complexe |

## C. Teams / Accès / Utilisateurs

| Feature | Protocole | Buildability | Détail |
|---|---|---|---|
| **Teams/Fleets (CRUD complet, multi-équipe)** | Aucun — pur DB/logique métier | ⚫ | `ee/server/service/teams.go` (~2600 lignes) — fondation dont dépendent policies/queries/profils partout. Pas de "vrai" protocole à appeler ; refaire ça = refaire une bonne partie de Fleet |
| **MFA (TOTP)** | Email TOTP déjà en core | 🟢 | `server/service/users.go:601` — juste un flag `lic.IsPremium()` à débloquer, le moteur MFA lui-même est déjà core |
| **Rôles premium / RBAC team-scoped** | Aucun | 🟢 | `users.go:149,713` — modèle de données déjà core/MIT, juste le check de licence à retirer |
| **SCIM — provisioning groupes** | SCIM 2.0 (`/Groups`) | 🟡 | `ee/server/scim/scim.go` — moteur SCIM auto-contenu, mais tables `scim_groups`/`scim_user_group` Fleet-spécifiques |
| **Google Workspace comme source SCIM (sync JIT)** | Google Admin SDK Directory API | 🟢 | `ee/server/googleworkspace/google_workspace.go` — wrapper fin sur le client Go officiel Google |
| **Calendrier (Google Calendar) pour auto-remédiation policies** | Google Calendar API v3 | 🟡 | `ee/server/calendar/google_calendar.go` — client réutilisable, mais logique de lock/scheduling couplée à `calendar_events` |

## D. Software / Apps / Vulnérabilités

| Feature | Protocole | Buildability | Détail |
|---|---|---|---|
| **VPP (Apple App Store)** | API VPP Apple (metadata via `server/mdm/apple/apple_apps`, déjà core) | 🟡 | `ee/server/service/vpp.go` — fetch metadata réutilisable, mais gestion token/association team très couplée DB |
| **Android Enterprise web apps** | Android Management API (`EnterprisesWebAppsCreate`) | 🟢 | ✅ **Fait** — `androidcmd create-web-app` (Phase 4), vérifié end-to-end : app créée dans Managed Google Play, ajoutée via Fleet Software (core), installée en self-service sur device réel |
| **Software installers custom (upload/install/uninstall/self-service)** | Aucun — agent orbit + storage Fleet | ⚫ | `ee/server/service/software_installers.go` (~4000 lignes) — trop couplé au schéma Fleet |
| **In-house apps (distribution interne signée)** | Manifest `x-apple-aspen-config` (OTA install macOS) | ⚫ | `ee/server/service/in_house_apps.go` — même couplage que ci-dessus |
| **Fleet-maintained apps — catalogue + téléchargement** | Aucun (lecture de manifests JSON) | 🟢 | Le vrai moteur (`server/mdm/maintainedapps`) est **déjà core/MIT** ! Seule la couche service team-scoped est gatée |
| **Auto-update des FMA installées** | Aucun | 🟢 | `ee/server/service/maintained_apps_auto_update.go` — juste absent du cron Free, pas vraiment "protégé" |
| **Catégories software self-service** | Aucun | 🟡 | CRUD simple mais couplé à `software_titles` |
| **Icônes software/device** | Aucun (storage blob) | 🟡 | Plomberie fichier, indépendant mais peu d'intérêt seul |
| **Setup experience (software/scripts au setup)** | DEP/Autopilot + tables Fleet | ⚫ | Entièrement mêlé à la state machine d'enrollment MDM |
| **Filtres vuln CVSS/known-exploit + tri** | Aucun — requête SQL | 🟢 | `ee/server/service/vulnerabilities.go:18,24` — débloquer des colonnes de tri/filtre sur une requête déjà core |

---

## Priorisation recommandée pour la suite

**État au 2026-09-19** — Phases 1-4 livrées et vérifiées end-to-end sur infra/device réels : ABM/DEP (`depsync`), SCEP proxy (`scepproxy`), device mapping type-SCIM (`usermapper`), sync Google Workspace (`wssync`), commandes MDM Android + web app Managed Play (`androidcmd`, y compris `list-devices`/`lock`/`clear-passcode`/`wipe`/`create-web-app`, plus la policy Android core et le Pub/Sub — voir `HOMEMADE-ANDROID-CMD.md`).

**Phase 6 en cours** — Bootstrap package (`bootstrappkg`, voir `HOMEMADE-BOOTSTRAP-PKG.md`) : outil écrit et buildé, `serve`/`login`/`find-host` vérifiés contre le vrai Fleet ; `install`/`check-command` en attente d'un Mac réel enrollé via DEP pour le test end-to-end complet.

~~**Lock/Wipe/Clear passcode Apple** listé plus haut comme "Phase 5 évidente"~~ — **corrigé, invalidé** : le tableau A ci-dessus documente un double blocage délibéré (`server/service/mdm.go:676-694`) sur ce point précis, contrairement à l'équivalent Android. Pas un candidat viable, à ne pas retenter.

**Candidats 🟢 restants, même pattern que le déjà-fait** :
- **Bootstrap package (DEP zero-touch installer)** — suite naturelle de la Phase 1 (`depsync`), même lib `godep`/commande MDM `InstallEnterpriseApplication`
- **EULA enrollment** et **DDM Assets** — CRUD blob trivial, peu d'intérêt isolément mais rapides
- **Fleet-maintained apps (catalogue)** / **Auto-update FMA** — moteur déjà core, juste la couche service team-scoped gatée ; à vérifier si "No team" suffit à contourner comme pour nos policies Android

**Candidats 🟡, plus de travail mais solides** :
- **CA custom DigiCert/EST** — clients Go quasi autonomes dans `ee/`, à réécrire hors du wrapping ee
- **Google Calendar** (auto-remédiation policies) — bon complément à `wssync`, l'org utilise déjà Workspace
- **FileVault (macOS) / BitLocker (Windows) escrow** — payload simple, stockage de clé à recréer
- **Conditional Access (Okta)** / **Host Identity certs (mTLS)** — plus de state/couplage orbit à gérer

**Exclus par le garde-fou du plan (pas de vraie logique ee/, juste un flag de licence)** :
- MFA (TOTP), rôles premium, filtres vuln CVSS — voir note ci-dessous

**À éviter / non prioritaire** :
- Teams, software installers, in-house apps, setup experience (⚫) — pas de protocole externe à appeler, il faudrait recréer une bonne partie du schéma Fleet
- Platform SSO Apple (🔴) — trop de crypto fine pour le ROI

---

## Note importante sur MFA et les filtres vuln

Contrairement à ABM/SCEP/SCIM/Android (où la vraie logique métier vit dans des libs/protocoles externes qu'on peut appeler nous-mêmes), **MFA et les filtres CVSS n'ont aucune "vraie" implémentation ee/ à reproduire** — c'est juste un `if !lic.IsPremium() { return ErrMissingLicense }` posé sur une fonctionnalité déjà 100% core. Le seul moyen de les "débloquer" serait de patcher ce check dans le binaire — ce que le garde-fou du plan interdit explicitement (contournement technique, pas réimplémentation indépendante). Je les liste ici pour l'exhaustivité de l'audit, mais je ne recommande pas de les traiter comme les autres phases.
