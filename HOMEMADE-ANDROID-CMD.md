# Notebook — commandes MDM Android maison (Lock / Wipe / Clear passcode)

Phase 4 du plan approuvé (`~/.claude/plans/indexed-leaping-volcano.md`). Cas différent des phases 1-3 : la logique métier (`LockAndroidHost`, `WipeAndroidHost`, `ClearAndroidPasscode`) est déjà **core, MIT** (`server/mdm/android/service/service.go`) — seule la route HTTP Fleet (`/hosts/{id}/lock|wipe|unlock`) est gatée, parce que `cmd/fleet/serve.go` n'enveloppe le service avec `ee/server/service` que `if license.IsPremium()`.

Outil : [`tools/homemade-mdm/androidcmd/main.go`](tools/homemade-mdm/androidcmd/main.go). Parle **directement à Google** (API Android Management), jamais à Fleet — contourne proprement le blocage (aucun moyen core de récupérer le `Device.DeviceID` AMAPI d'un host via l'API Fleet).

✅ **Vérifié end-to-end** (2026-09-19) : quota AMAPI augmenté et validé par Google, `list-devices` retrouve le Ulefone Armor X13 réel (`enterprises/LC037qt49t/devices/3204ef5da40231ea`, serial `3117SH1010031886`).
- `lock` : envoyée et effectivement reçue par le device (écran verrouillé).
- `clear-passcode` : envoyée, `done: true` sans erreur côté AMAPI (confirmé via la nouvelle sous-commande `check-op`, qui poll `Enterprises.Devices.Operations.Get` — l'ack normal de Fleet passe par Pub/Sub, absent dans cet outil standalone). Pas de changement visible sur le device testé faute de passcode préexistant à effacer, donc pas de validation visuelle possible, mais la commande a bien été traitée.
- `create-web-app` : app `com.google.enterprise.webapp.x6edc5928db0e12eb` créée dans le catalogue Managed Google Play de l'enterprise, puis ajoutée dans Fleet (Software → App store → Android, application ID collé directement) et installée avec succès depuis le Play Store sur le Ulefone en self-service. **Chaîne complète validée** : androidcmd → catalogue Play → Fleet Software (core) → device réel.
- `wipe` : testé en dernier comme prévu (2026-09-19), une fois toutes les autres fonctionnalités Android MDM/GCP validées. Commande envoyée, `check-op` a d'abord montré `done` absent (livraison en attente), puis le device a effectivement redémarré et exécuté le factory reset — confirmé par observation directe. **Les 3 commandes `androidcmd` (lock, clear-passcode, wipe) sont maintenant vérifiées end-to-end sur device réel.**

**Hors périmètre `androidcmd`, à tester séparément côté Fleet (déjà core, pas ee/)** :
- ✅ Ajout d'app Android via Fleet Software (App store tab) — vérifié ci-dessus
- ✅ **Policy Android avec restriction réelle** (2026-09-19) : profil `camera-restriction.json` (`{"cameraDisabled": true}`, `tools/homemade-mdm/androidcmd/policies/`) uploadé via Controls → OS settings → Configuration profiles, cible "No team" (reste core, pas de check `license.IsPremium()` déclenché). Statut Fleet passé à "Verified", et l'app caméra a effectivement disparu du Ulefone — confirmé par observation directe sur le device, pas seulement côté Fleet.
- ✅ **Remontée Pub/Sub en continu** (2026-09-19) : confirmé via l'inspecteur ngrok (`http://127.0.0.1:4040/api/requests/http`) sur `POST /api/v1/fleet/android_enterprise/pubsub` — des notifications `STATUS_REPORT` volumineuses (~160 Ko) arrivent en continu et sont traitées avec succès (200), pas seulement à l'enrollment.
  - ⚠️ Bruit de fond observé, expliqué et sans gravité : les notifications `COMMAND` pour le `lock` et le `clear-passcode` qu'on a émis via `androidcmd` (donc jamais insérés dans `mdm_android_commands`) reçoivent un 404 en boucle — `server/mdm/android/service/pubsub.go:290-314` (`ackOrRetryUnknownAndroidOperation`) suppose une race condition côté insertion Fleet et fait rejouer Google indéfiniment au lieu d'acquitter, puisque la ligne attendue n'existe pas et n'existera jamais (commande émise hors Fleet). S'éteint tout seul à l'expiration du délai de retry Google ; ne se reproduit pas pour des commandes émises depuis Fleet lui-même.

**Phase 4 terminée.** Effet de bord attendu du wipe, pour référence future : `handleAndroidWipeAckUnenroll` (`server/mdm/android/service/pubsub.go:240-282`) flippe `host_mdm.enrolled` à 0 dès l'ack — le Ulefone va donc apparaître comme désenrollé/offline dans Fleet, pas juste "reset". Pour retester, il faudra ré-enrôler via le lien QR (`HOMEMADE-ANDROID-CMD.md`/`ANDROID-MDM-SETUP.md`) comme au premier enrollment.

## ⚠️ Bug rencontré et corrigé (2026-09-21) : le dashboard Fleet reste figé après un `wipe` androidcmd

**Symptôme observé** : deux jours après le `wipe`, le Ulefone apparaissait toujours dans Fleet comme un host normal, actif (41,36 Go d'espace disque, "last fetched: il y a 2 jours") — alors que le device était bel et bien réinitialisé physiquement.

**Cause** : exactement le mécanisme de retry-au-lieu-d'acquitter décrit plus haut (`ackOrRetryUnknownAndroidOperation`), mais appliqué au `wipe` cette fois. Comme la commande n'existe pas dans `mdm_android_commands`, `handleAndroidWipeAckUnenroll` — qui flippe `host_mdm.enrolled` — **n'a jamais tourné**. Confirmé via l'inspecteur ngrok : plus aucune requête Pub/Sub reçue depuis le 19/09 23:59 alors que le tunnel et Fleet n'ont jamais été interrompus — Google a fini par abandonner la livraison de cet accusé précis. Aucun cron Fleet ne revérifie l'existence réelle d'un device Android auprès de Google pour rattraper ce genre de désync : la fiche restait donc figée indéfiniment.

**Correctif appliqué** : deux nouvelles sous-commandes dans [`tools/homemade-mdm/bootstrappkg/main.go`](tools/homemade-mdm/bootstrappkg/main.go) (outil déjà équipé de l'auth Fleet depuis la Phase 6) :
- `unenroll-mdm -host-id ID` → `DELETE /api/v1/fleet/hosts/{id}/mdm`, qui appelle le vrai code core `UnenrollMDM` → `UnenrollAndroidHost` (`server/mdm/android/service/service.go:846+`, **zéro référence à `IsPremium`/`ErrMissingLicense` dans tout le fichier**). C'est le même chemin de code qu'un ack Pub/Sub légitime aurait déclenché.
- `delete-host -host-id ID` → `DELETE /api/v1/fleet/hosts/{id}`, pour un device COBO déjà wipé qui ne reviendra pas tout seul (cas du Ulefone : fiche supprimée plutôt que juste désenrollée).

**Règle à suivre pour la suite** : après tout `wipe`/`lock`/`clear-passcode` émis via `androidcmd`, une fois `check-op` confirmé `done: true`, appeler `bootstrappkg unenroll-mdm` (ou `delete-host` si le device ne reviendra pas) pour que Fleet reflète l'état réel immédiatement — ne jamais compter sur l'ack Pub/Sub pour une commande émise hors Fleet, il n'arrivera jamais.

---

## Pourquoi un projet Google Cloud à nous (pas le proxy fleetdm.com par défaut)

Le proxy par défaut de Fleet (`https://fleetdm.com/api/android/`, documenté comme option principale dans [ANDROID-MDM-SETUP.md](ANDROID-MDM-SETUP.md)) ne restitue jamais le `device_id` technique nécessaire pour cibler une commande — cette info reste interne à Fleet. En utilisant notre propre projet GCP, on peut lister les devices **directement depuis Google** (avec numéro de série en clair) sans jamais passer par Fleet.

➡️ Pour cette instance, la Phase 4 **remplace** la section "par défaut" de `ANDROID-MDM-SETUP.md` par sa section "Alternative" (projet GCP dédié).

---

## Ce qui a été fait (résumé, déjà exécuté sur cette instance)

### 1. Projet GCP + service account
- Projet `mobisoft-mdm` (numéro `387690435225`)
- Service account `mdm-app@mobisoft-mdm.iam.gserviceaccount.com`, rôles **Android Management User** + **Pub/Sub Admin**
- Clé JSON téléchargée, stockée dans `tools/homemade-mdm/androidcmd/keys/credentials.json` (⚠️ gitignored via `tools/homemade-mdm/.gitignore` — ne jamais commit ce fichier)

### 2. Deux API Google à activer manuellement (piège rencontré)
Erreurs obtenues au premier essai, dans l'ordre :
1. `Android Management API has not been used in project ... or it is disabled` → activée via `https://console.developers.google.com/apis/api/androidmanagement.googleapis.com/overview?project=387690435225`
2. `Cloud Pub/Sub API has not been used in project ... or it is disabled` → activée via `https://console.developers.google.com/apis/api/pubsub.googleapis.com/overview?project=387690435225`

Les deux sont nécessaires : Android Management API pour les commandes, Pub/Sub pour que Fleet reçoive les notifications de statut des devices.

### 3. Configuration serveur
- `.env.prod` : ajout de
  ```
  FLEET_DEV_ANDROID_GOOGLE_CLIENT=1
  FLEET_DEV_ANDROID_GOOGLE_SERVICE_CREDENTIALS='<JSON compacté du service account, entre quotes simples>'
  ```
- `docker-compose.prod.yml` : le `command:` du service `fleet` est passé à `/usr/bin/fleet serve --dev` — **obligatoire**, sans ça `FLEET_DEV_ANDROID_*` sont silencieusement ignorées (`server/dev_mode/dev_mode.go` : `Env()` retourne `""` tant que `dev_mode.IsEnabled` est `false`, et ce flag n'est activé que par `--dev`)
- ⚠️ **Effet de bord assumé** : `--dev` désactive aussi la protection SSRF du serveur (`fleethttp.SetNetworkBlockingMode(BlockingBypassAll)` dans `cmd/fleet/serve.go`). Acceptable sur une instance de test perso, à reconsidérer si cette instance sert un jour de vraies données.
- Redémarrage : `docker compose -f docker-compose.prod.yml --env-file .env.prod up -d fleet`

### 4. Activation Android MDM dans Fleet
Settings → Integrations → MDM → Turn on Android → Connect. **Succès** : "Android MDM turned on successfully."

### 5. Récupération de l'enterprise ID
`GET /api/v1/fleet/android_enterprise` nécessite un rôle **admin** (notre token API-only est `maintainer` → 403, cohérent avec `server/authz/policy.rego`). Contournement : utilisé l'outil de référence interne `tools/android/android.go` (déjà dans le repo, jamais dans `ee/`) avec `-command enterprises.list` pour lister les enterprises **directement depuis Google**, sans passer par Fleet du tout.

**Enterprise ID trouvé : `LC037qt49t`** (affichage "Mobisoft")

---

## Utiliser `androidcmd`

```bash
alias fleetgo='docker run --rm -e GOTOOLCHAIN=auto -v /Users/macbookpro/fleet:/src -w /src golang:1.24'
fleetgo go build -o /src/tools/homemade-mdm/androidcmd/androidcmd ./tools/homemade-mdm/androidcmd/
```

### Lister les devices
```bash
./tools/homemade-mdm/androidcmd/androidcmd \
  -creds-file tools/homemade-mdm/androidcmd/keys/credentials.json \
  -enterprise-id LC037qt49t \
  list-devices
```
**Testé le 2026-07-28** : fonctionne (auth Google OK, requête OK), retourne `no devices found in this enterprise` — cohérent, aucun device enrollé pour l'instant (Phase B, voir plus bas).

### Lock / Wipe / Clear passcode
Une fois qu'un device apparaît dans `list-devices`, copier son `name` (ex. `enterprises/LC037qt49t/devices/862...`) :
```bash
./tools/homemade-mdm/androidcmd/androidcmd -creds-file .../credentials.json -enterprise-id LC037qt49t \
  lock -device-name "enterprises/LC037qt49t/devices/862..."

# idem avec `wipe` ou `clear-passcode`
```

### Create web app (Phase 6 — équivalent maison de `CreateAndroidWebApp` EE)
Crée une "web app" (raccourci type bookmark) dans Managed Google Play pour cette enterprise — équivalent de `ee/server/service/vpp.go:1615`, qui n'est qu'un wrapper fin sur `androidmanagement.WebApp{DisplayMode: "STANDALONE", ...}`.

```bash
./tools/homemade-mdm/androidcmd/androidcmd -creds-file .../credentials.json -enterprise-id LC037qt49t \
  create-web-app -title "Mon intranet" -url "https://intranet.example.com"
```

**Testé le 2026-07-29** : compile (après correction — `WebApp` ne renvoie pas de champ `PackageName` direct, il faut le dériver de `created.Name` en retirant le préfixe `enterprises/{id}/webApps/`, exactement comme le fait `ee/server/service/vpp.go:1688`). Retourne `name` + `packageName` dérivé. Pas encore testé contre un vrai enterprise (build only) — reste à ajouter le `packageName` retourné à la Policy de l'enterprise pour le pousser sur un device (pas automatisé dans ce v1, à faire via l'UI Fleet ou un appel `EnterprisesPoliciesModifyPolicyApplications` direct si besoin).

---

## Phase B — reste à faire pour tester lock/wipe en réel

Aucun device n'est encore enrollé sur cette instance. Deux options (déjà documentées dans [ANDROID-MDM-SETUP.md](ANDROID-MDM-SETUP.md) section 3) :

- **Émulateur** : Android Studio, image système **Google Play** (pas "Google APIs"/AOSP — sinon la stack Google Mobile Services est incomplète et l'enrollment Android Enterprise échoue)
- **Device réel (BYOD)** : lien de signup généré depuis Fleet, suivre le flow d'enrollment natif Android

Une fois le device enrollé :
1. `list-devices` doit le faire apparaître avec son `serial_number`
2. Comparer ce numéro de série avec le `hardware_serial` affiché côté Fleet (Hosts → détail du host) pour confirmer que c'est le bon
3. Tester `lock` → vérifier que l'écran se verrouille effectivement sur le device
4. Tester `clear-passcode` puis `wipe` avec précaution (wipe est destructif — sur BYOD ça ne wipe que le work profile, sur un device fully-managed ça reset l'appareil entier)

---

## Pièges classiques (mémo)

- **Oublier `--dev`** → les credentials GCP sont configurées mais silencieusement ignorées, Fleet retombe sur le proxy fleetdm.com par défaut sans erreur explicite
- **Une des deux API Google non activée** → erreur claire à l'activation d'Android MDM (Android Management API ou Pub/Sub), mais seulement au moment du "Connect", pas avant
- **Utiliser `EnterprisesDevicesListPartial` du package `androidmgmt`** → à éviter pour lister les devices, ce chemin applique un `.Fields()` qui supprime le numéro de série ; `androidcmd` utilise directement `mgmt.Enterprises.Devices.List(...).Do()` sans restriction de champs
- **`wipe` sur un device fully-managed** → efface tout l'appareil, pas juste un profil — à utiliser avec précaution même en test
