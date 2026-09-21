# Notebook — SCEP proxy maison (remplace Fleet Premium)

Phase 2 du plan approuvé (`~/.claude/plans/indexed-leaping-volcano.md`). Remplace la feature "SCEP proxy" de Fleet Premium par un service maison — **sans utiliser la feature CA de Fleet** (100% ee-gated, `server/service/certificate_authorities.go` retourne `fleet.ErrMissingLicense` sur Free, confirmé lors de la recherche de faisabilité).

Outil : [`tools/homemade-mdm/scepproxy/main.go`](tools/homemade-mdm/scepproxy/main.go) — ~70 lignes, réutilise `server/mdm/scep/{client,server}` (fork MIT de `micromdm/scep`, core, pas `ee/`). Le `Client` qu'on obtient de `scepclient.New()` implémente déjà l'interface `scepserver.Service` en relayant chaque verbe SCEP vers la vraie CA — donc `scepserver.MakeServerEndpoints` + `MakeHTTPHandler` suffisent à ré-exposer ça comme notre propre serveur SCEP. Zéro logique protocolaire écrite à la main.

---

## Architecture

```
device (profil .mobileconfig) --SCEP--> notre scepproxy (domaine public à nous) --SCEP--> vraie CA (NDES/interne)
                                                                                              ↑
                                                                    jamais Fleet, jamais ee/, jamais gaté
```

Fleet n'intervient QUE pour pousser le `.mobileconfig` sur les devices déjà enrollés MDM — via l'API core de gestion de profils, aucun rapport avec la feature CA gatée.

---

## Étape 1 — builder et lancer le proxy

Pas de Go local sur cette machine, on utilise la même image Docker que pour le service ABM/DEP.

```bash
alias fleetgo='docker run --rm -e GOTOOLCHAIN=auto -v /Users/macbookpro/fleet:/src -w /src golang:1.24'
fleetgo go build -o /src/tools/homemade-mdm/scepproxy/scepproxy ./tools/homemade-mdm/scepproxy/
```

Lancer le proxy (à adapter : `-upstream-url` = l'URL SCEP de ta vraie CA) :

```bash
./tools/homemade-mdm/scepproxy/scepproxy \
  -listen :8090 \
  -upstream-url https://<ta-vraie-ca>/certsrv/mscep/mscep.dll
```

- [ ] `-upstream-root-ca /chemin/vers/ca-bundle.pem` si la CA a un cert non reconnu par le store système
- [ ] Ne jamais utiliser `-upstream-insecure` en dehors d'un test local isolé

---

## Étape 2 — exposer le proxy publiquement

Même logique que pour Fleet et Android MDM — Apple/le device doivent pouvoir joindre l'URL en HTTPS public. Un second tunnel ngrok (domaine statique séparé, ou reverse-proxy si tu en as déjà un) :

```bash
ngrok http --url=<ton-second-domaine>.ngrok-free.dev 8090
```

- [ ] Noter l'URL publique — c'est la valeur à mettre dans `__SCEP_PROXY_URL__` à l'étape 3

---

## Étape 3 — remplir le template `.mobileconfig`

Le template est dans [`tools/homemade-mdm/scepproxy/profile/scep-proxy.mobileconfig.tmpl`](tools/homemade-mdm/scepproxy/profile/scep-proxy.mobileconfig.tmpl) (déjà validé `plutil -lint OK`).

```bash
cd tools/homemade-mdm/scepproxy/profile
cp scep-proxy.mobileconfig.tmpl scep-proxy.mobileconfig

sed -i '' "s|__SCEP_PROXY_URL__|https://<ton-domaine-scepproxy>.ngrok-free.dev|" scep-proxy.mobileconfig
sed -i '' "s|__SCEP_CHALLENGE__|<challenge-de-ta-vraie-ca>|" scep-proxy.mobileconfig
sed -i '' "s|__PROFILE_NAME__|Homemade SCEP|" scep-proxy.mobileconfig
sed -i '' "s|__PAYLOAD_UUID__|$(uuidgen)|" scep-proxy.mobileconfig
sed -i '' "s|__ROOT_UUID__|$(uuidgen)|" scep-proxy.mobileconfig

plutil -lint scep-proxy.mobileconfig   # doit dire OK
```

⚠️ **Trade-off de sécurité assumé** (voir commentaire dans le template) : ce v1 est un relais transparent, il n'injecte pas de challenge one-time par device côté serveur comme le fait la feature EE de Fleet. Le challenge est statique et visible par quiconque lit le profil — exactement comme n'importe quel payload SCEP classique pré-Fleet. Acceptable pour un premier test ; à durcir plus tard si besoin (voir "Aller plus loin" en bas).

---

## Étape 4 — uploader le profil via l'API core de Fleet (GitOps)

Fusionner [`tools/homemade-mdm/scepproxy/gitops-snippet.yml`](tools/homemade-mdm/scepproxy/gitops-snippet.yml) dans ton fichier GitOps existant (`controls.macos_settings.custom_settings`), en pointant `path:` vers `scep-proxy.mobileconfig`.

```bash
docker exec fleet-fleet-1 fleetctl gitops -f <ton-fichier-gitops.yml>
```

- [ ] Confirmer côté UI Fleet (**Controls → OS settings → Custom settings**) que le profil apparaît
- [ ] Le profil se pousse automatiquement (reconciler ~30s) sur les hosts déjà enrollés MDM ciblés

---

**Testé le 2026-07-23**, en local, avec `server/mdm/scep/cmd/scepserver` comme CA de test (fait office de "vraie CA" NDES/interne pour le test) et `server/mdm/scep/cmd/scepclient` pointé sur notre proxy (pas sur la CA directement). Résultat : `GetCACaps`/`GetCACert`/`PKIOperation` relayés correctement, certificat émis avec succès (`pkiStatus=SUCCESS`), vérifié via `openssl x509` (subject/issuer/dates cohérents). Confirme que la logique de relais du proxy fonctionne de bout en bout ; reste à valider contre une vraie CA externe (NDES ou autre) en remplaçant `-upstream-url`.

## Étape 5 — vérification

- [ ] Sur le device : **Réglages Système → Profils** → le profil "Homemade SCEP" est installé, un certificat apparaît dans le trousseau peu après
- [ ] Côté vraie CA : vérifier dans ses logs qu'une requête SCEP est bien arrivée, provenant de l'IP de notre `scepproxy` (pas directement du device)
- [ ] `docker logs fleet-fleet-1` : confirmer qu'aucune route `/mdm/scep/proxy/...` (celle de la feature EE) n'a été sollicitée — normal, elle n'existe même pas en Free

---

## Aller plus loin (pas fait dans ce v1)

- **Challenge par device** : possible en interceptant `PKIOperation` dans notre propre `scepserver.Service` (au lieu d'utiliser directement le `Client` comme `Service`), pour réécrire l'attribut challenge du PKCS#7 avant de relayer — plus de code, pas fait ici par souci de rester simple pour un premier test
- **Multi-CA** : le binaire ne gère qu'une seule CA en aval (`-upstream-url` fixe) ; pour plusieurs CAs il faudrait plusieurs instances ou router par path, comme le fait `ee/server/service/scep/scep_proxy.go` (à ne pas copier, à réinventer si besoin)
