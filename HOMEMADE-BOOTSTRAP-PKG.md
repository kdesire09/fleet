# Notebook — Bootstrap package maison (remplace Fleet Premium)

Phase 6 du plan approuvé. Remplace la feature "Bootstrap package" de Fleet Premium (installation automatique d'un `.pkg` pendant l'enrollment DEP zero-touch) par un flow maison — **sans utiliser le stockage/CRUD bootstrap package de Fleet (100% ee-gated)**, en s'appuyant uniquement sur l'API core déjà libre de Fleet pour l'exécution de commandes MDM.

Outil : [`tools/homemade-mdm/bootstrappkg/main.go`](tools/homemade-mdm/bootstrappkg/main.go).

## Pourquoi ce n'est pas le même schéma que `androidcmd`

Contrairement à Android (où AMAPI est directement appelable avec nos propres credentials GCP, sans passer par Fleet), **Fleet EST le serveur MDM Apple** — pas de relai vers un tiers indépendant. Impossible de pousser une commande `InstallEnterpriseApplication` à un Mac sans passer par le push APNs/nanomdm que Fleet gère en interne.

Bonne nouvelle après vérification (`PREMIUM-FEATURES-INVENTORY.md`) : contrairement à Lock/Wipe/Clear passcode (bloqués explicitement, `server/service/mdm.go:676-680`), **`InstallEnterpriseApplication` n'est PAS dans la liste des commandes premium-only**. L'endpoint générique `POST /api/v1/fleet/mdm/commands/run` (`fleetctl mdm run-command`) est core et libre pour cette commande précise. Donc l'outil n'appelle jamais `ee/`, ne contourne aucun check de licence — il utilise juste normalement une API core que Fleet expose déjà, exactement comme le ferait un admin via `fleetctl` ou l'UI.

Seule la partie réellement ee-gated (stockage/upload/download du `.pkg` par Fleet, `ee/server/service/mdm.go:382-556` + `server/service/apple_mdm.go:3634-3687`) est évitée : l'outil héberge lui-même le `.pkg`.

## Architecture

```
bootstrappkg serve  --------------------->  .pkg + manifest.plist servis en local
        |                                    (tunnel ngrok dédié, 2e tunnel séparé
        |                                     de celui de Fleet)
        v
bootstrappkg login  --------------------->  POST /api/v1/fleet/login (core)
        |                                    récupère un token de session normal
        v
bootstrappkg find-host  ----------------->  GET /api/v1/fleet/hosts?query=... (core)
        |                                    trouve l'UUID du Mac cible
        v
bootstrappkg install  -------------------->  POST /api/v1/fleet/mdm/commands/run (core)
        |                                    plist InstallEnterpriseApplication +
        |                                    ManifestURL -> notre serve
        v
bootstrappkg check-command  -------------->  GET /api/v1/fleet/mdm/commandresults (core)
                                              statut de livraison de la commande

                                    jamais ee/, jamais gaté
```

Le plist `InstallEnterpriseApplication` (variante `ManifestURL`) et le format du manifest sont repris tels quels du code core de Fleet (`server/mdm/apple/commander.go`, package `server/mdm/apple/appmanifest` — le même que `tools/mdm/apple/appmanifest`, déjà interne à Fleet).

## Statut de vérification (2026-09-19)

- ✅ **Build** : compile proprement (`GOOS=darwin GOARCH=amd64`, cache modules `/tmp/gomodcache`)
- ✅ **`serve`** : testé en local avec un fichier factice — sha256 du manifest généré vérifié identique à `shasum -a 256`, `/pkg` et `/manifest.plist` répondent correctement
- ✅ **`login`** : testé contre le vrai Fleet (token obtenu par l'utilisateur directement, jamais passé de mot de passe dans la conversation)
- ✅ **`find-host`** : testé contre le vrai Fleet — retrouve bien `homemade-test-host-01`, confirme l'auth Bearer + le fix HTTP/1.1 (même souci `http2: client conn could not be established` que rencontré plus tôt dans la session avec `curl` sur ce même tunnel ngrok — corrigé en désactivant l'upgrade HTTP/2 automatique du client Go, `TLSNextProto` vidé)
- ⬜ **`install`** / **`check-command`** : câblage HTTP identique aux commandes ci-dessus (donc a priori fonctionnel), mais **pas encore testé end-to-end** — aucun Mac physique disponible actuellement, et `homemade-test-host-01` n'a jamais complété un vrai enrollment (uuid/serial vides dans Fleet, host créé le 2026-07-23 mais jamais fetché). Nécessite un Mac réel enrollé via DEP pour valider le flow complet.

## Pour reprendre le test plus tard

1. Enrôler un vrai Mac via DEP zero-touch (`depsync`, Phase 1) — s'assurer qu'`AwaitDeviceConfigured` est à `true` dans le profil DEP si on veut bloquer Setup Assistant le temps de l'installation
2. `bootstrappkg serve -pkg-file <un .pkg réel> -public-url <URL du 2e tunnel ngrok>`
3. `bootstrappkg login` puis `find-host` pour récupérer l'UUID réel du Mac
4. `bootstrappkg install -manifest-url <.../manifest.plist> -host-uuid <uuid>`
5. `bootstrappkg check-command -command-uuid <uuid>` pour suivre la livraison
