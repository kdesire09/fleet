# Notebook de configuration — activer Apple MDM et enregistrer un premier device

Objectif : partir de la stack Docker actuelle (`docker-compose.prod.yml` + `.env.prod`, déjà up) et arriver à un premier device macOS enrollé dans le MDM Fleet.

Chaque étape est **bloquante** pour la suivante — Apple ne négocie pas avec un serveur mal configuré (pas de fallback silencieux, ça échoue net).

---

## 0. État actuel constaté

D'après `.env.prod` et l'inspection des conteneurs :

| Point | État |
|---|---|
| `FLEET_SERVER_PRIVATE_KEY` | ✅ déjà renseigné (requis pour toutes les features MDM — voir `cmd/fleet/serve.go:837`, `len(config.Server.PrivateKey) > 0` conditionne l'enregistrement des services MDM Apple) |
| `FLEET_SERVER_TLS` | ❌ `false` — le serveur sert du **HTTP en clair** sur `0.0.0.0:1337` |
| Exposition réseau | ❌ le port `1337` n'est mappé que sur la machine hôte (`docker port` → `0.0.0.0:1337`), pas de nom de domaine, pas de certificat public |
| Licence | Free tier (`FLEET_LICENSE_KEY` vide) → **ABM/DEP nécessite Fleet Premium**. L'enrollment MDM manuel fonctionne en Free ; l'enrollment automatique (zero-touch) est premium only |

➡️ Le point 0 le plus urgent : **Apple MDM exige HTTPS avec un certificat public valide, atteignable depuis Internet** (le device ET les serveurs Apple/APNs doivent pouvoir joindre l'URL). Un binding `0.0.0.0:1337` en HTTP sur ta machine ne suffit pas — c'est l'étape 1.

---

## 1. Exposer le serveur en HTTPS public

Deux options, à choisir selon ce que tu veux faire ensuite :

### Option A — ngrok (rapide, pour tester, non pérenne) ✅ FAIT

- [x] ngrok installé (`brew install --cask ngrok`)
- [x] Compte créé, authtoken configuré (`ngrok config add-authtoken ...`)
- [x] Domaine statique gratuit réservé : **`boundless-arson-versus.ngrok-free.dev`**
- [x] Tunnel lancé : `ngrok http --url=boundless-arson-versus.ngrok-free.dev 1337` (process en arrière-plan)
- [x] Vérifié : `curl https://boundless-arson-versus.ngrok-free.dev/healthz` → `200`

**URL publique du serveur Fleet : `https://boundless-arson-versus.ngrok-free.dev`**

⚠️ Le tunnel ngrok doit rester actif en permanence tant que le device est enrollé (sinon Apple/le device ne peuvent plus joindre le serveur). Si le process ngrok est tué et relancé, l'URL `boundless-arson-versus.ngrok-free.dev` reste la même (domaine statique réservé) — pas besoin de refaire les étapes suivantes, contrairement à un domaine aléatoire.

⚠️ Le sous-domaine ngrok free change à chaque redémarrage sauf abonnement payant ou domaine réservé — si l'URL change, il faut refaire l'étape 2 (le certificat APNs et le token ABM référencent cette URL).

### Option B — reverse proxy + nom de domaine réel (Caddy/nginx/Traefik + Let's Encrypt)
Pour une install plus durable. Pointer un vrai domaine vers ta machine, un reverse proxy termine le TLS et forward vers `localhost:1337`.
- [ ] DNS `A`/`AAAA` du domaine → IP publique de la machine qui héberge Docker
- [ ] Reverse proxy avec cert Let's Encrypt, `proxy_pass http://127.0.0.1:1337`
- [ ] Garder `FLEET_SERVER_TLS=false` (le proxy gère le TLS, pas Fleet)

### Ce qu'il faut retenir dans tous les cas
- [x] L'URL publique choisie devient **`FLEET_SERVER_ADDRESS`... non** — attention, `FLEET_SERVER_ADDRESS`/`PORT` restent l'adresse d'écoute interne. L'URL publique doit être configurée dans Fleet via **Settings → Organization settings → Server URL** dans l'UI (ou `server_settings.server_url` en YAML/API) — c'est cette valeur que Fleet grave dans les profils de configuration envoyés aux devices et dans le service discovery Apple
  → **fait** : Server URL = `https://boundless-arson-versus.ngrok-free.dev`
- [ ] Ne change plus cette URL une fois des devices enrollés dessus (ré-enrollment nécessaire sinon)

---

## 2. Activer Apple MDM (obligatoire, même pour l'enrollment manuel)

Le check-in MDM Apple repose sur APNs (Apple Push Notification service) — sans certificat APNs valide, **aucun** enrollment Apple n'est possible, manuel ou automatique.

- [x] Se connecter à l'UI Fleet (`https://<ton-url-publique>`) en tant que **global admin**
- [x] Aller dans **Settings → Integrations → Mobile device management (MDM) → Apple**
- [x] CSR générée par Fleet, signée sur **https://identity.apple.com/pushcert**, certificat APNs uploadé
- [x] Statut **"MDM turned on"** confirmé

Une fois cette étape faite, `cmd/fleet/serve.go:881` (`service.RegisterAppleMDMProtocolServices`) monte réellement les endpoints MDM Apple (check-in, command, SCEP) sur le serveur — avant, ils sont enregistrés mais Apple ne peut rien y faire sans APNs valide.

⚠️ Le certificat APNs a une **durée de vie d'un an** — garder une note de la date de renouvellement (voir `docs/Contributing/guides/rollover-apple-mdm-ca-cert.md` pour la procédure de renouvellement le moment venu).

---

## 3. (Optionnel — nécessite Fleet Premium) Apple Business Manager pour l'enrollment automatique

À ne faire QUE si tu veux du zero-touch (le device s'enrolle tout seul au premier boot). Pour un premier test, **saute cette étape** et va directement à la section 4 (enrollment manuel) — c'est plus rapide et fonctionne en Free tier.

Si tu veux quand même le faire :
- [ ] Avoir accès à un compte [Apple Business Manager](https://business.apple.com) pour ton organisation (démarche administrative côté Apple — DUNS number, vérification d'entreprise — pas instantané)
- [ ] Dans ABM : créer un serveur MDM pointant vers ton URL Fleet publique, générer un **token chiffré** (`.p7m`)
- [ ] Dans Fleet UI : **Settings → Integrations → MDM → Apple Business Manager**, uploader ce token
- [ ] Dans ABM : assigner le numéro de série du device cible à ce serveur MDM (**"Edit MDM Server"**)
- [ ] Nécessite `FLEET_LICENSE_KEY` valide (Premium) — voir `config.MDM` et les checks `license.IsPremium()` dans `serve.go`

---

## 4. Enrollment manuel (le chemin le plus rapide pour un premier device)

Fonctionne en Free tier, pas besoin d'ABM.

- [ ] Récupérer un **enroll secret** Fleet (créé automatiquement, visible dans **Hosts → Add hosts** dans l'UI)
- [ ] Builder un package `fleetd` avec Fleet Desktop activé, pointant vers ton URL publique :
  ```bash
  ./build/fleetctl package --type=pkg --fleet-desktop \
    --fleet-url=https://<ton-url-publique> \
    --enroll-secret=<ton-enroll-secret>
  ```
  (la commande exacte, avec ton URL et ton secret, est aussi affichée directement dans l'UI sous **Add hosts**)
- [ ] Installer ce `.pkg` sur le device macOS cible (VM ou machine réelle)
- [ ] Le host apparaît dans Fleet comme host osquery normal (enrollment osquery/orbit — indépendant du MDM à ce stade)
- [ ] Ouvrir **Fleet Desktop** sur le device → page **My device** → une bannière propose d'activer MDM
- [ ] Cliquer, accepter le profil de configuration MDM proposé par macOS (Réglages Système → Profils)
- [ ] Retour dans Fleet UI → le host doit maintenant afficher **MDM: On (manual)**

---

## 5. Vérification finale

- [ ] Dans **Hosts → [ton host] → détails**, le champ MDM status passe à "On"
- [ ] Tester une commande MDM basique depuis l'UI (ex. lock, ou appliquer un profil de configuration) et vérifier qu'elle arrive sur le device
- [ ] Si rien ne se passe : vérifier côté device que l'URL Fleet est bien joignable en HTTPS (`curl -v https://<url>/healthz` depuis le device), et vérifier les logs du conteneur `fleet-fleet-1` (`docker logs fleet-fleet-1 --tail 100`) pour des erreurs APNs/SCEP

---

## Pièges classiques (mémo)

- **HTTP au lieu de HTTPS public** → Apple refuse silencieusement, aucun message d'erreur clair côté Fleet
- **URL serveur changée après enrollment** (ex. redémarrage ngrok avec un nouveau sous-domaine) → les devices déjà enrollés perdent la communication, il faut ré-enrôler
- **Certificat APNs expiré (1 an)** → tous les devices MDM cessent de recevoir des commandes ; pas de suppression automatique des données, mais plus aucun push
- **ABM sans Premium** → l'upload du token ABM échoue ou la fonctionnalité reste masquée ; pas la peine d'insister en Free tier, utiliser l'enrollment manuel (section 4)
