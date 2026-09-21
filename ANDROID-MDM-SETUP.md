# Notebook de configuration — activer Android MDM (Android Enterprise) et enregistrer un premier device

Suite de [MDM-SETUP.md](MDM-SETUP.md) (Apple MDM). Android est un sous-système **complètement différent** côté Fleet (`server/mdm/android/`, endpointer `androidAuthenticatedEndpointer` dédié, protocole Google Android Management API + Pub/Sub au lieu d'APNs/SCEP) — pas de recoupement avec la config Apple, à part l'URL publique du serveur.

Référence interne : `docs/Contributing/product-groups/mdm/android-mdm.md`.

> **Mise à jour (Phase 4 du plan homemade-mdm)** : cette instance est finalement passée sur son **propre projet Google Cloud** plutôt que le proxy fleetdm.com décrit comme option par défaut ci-dessous — nécessaire pour que l'outil maison `androidcmd` (lock/wipe/clear passcode) puisse fonctionner. Voir [HOMEMADE-ANDROID-CMD.md](HOMEMADE-ANDROID-CMD.md) pour le détail de cette bascule (activation `--dev`, activation des API Google Android Management + Pub/Sub, etc.). La section "Alternative" plus bas dans ce fichier est donc devenue la config **active**, pas juste une option.

---

## 0. Bonne nouvelle : le terrain est déjà préparé

- ✅ **URL publique HTTPS déjà en place** : `https://boundless-arson-versus.ngrok-free.dev` (fait pour Apple MDM) — c'est exactement la même contrainte bloquante pour Android (*"Android MDM can't be turned on with localhost server URL, so your server can receive `STATUS_REPORT` via PubSub notifications"*), donc rien à refaire ici
- ✅ **Pas besoin de créer un projet Google Cloud** — par défaut, Fleet passe par le proxy hébergé de fleetdm.com vers l'API Android Management de Google (`defaultProxyEndpoint = "https://fleetdm.com/api/android/"`, voir `server/mdm/android/service/androidmgmt/proxy_client.go:24`). Ce n'est utile que si tu veux ton propre projet GCP (voir section "Alternative" en bas) — **pas nécessaire pour un premier test**

---

## 1. Contraintes à connaître AVANT de cliquer sur "Connect"

- ⚠️ **1 seule Android Enterprise par URL de serveur Fleet** (limitation du proxy fleetdm.com) — si tu dois recommencer, il faut d'abord désactiver Android MDM avant de changer d'URL ou de reset la DB
- ⚠️ **L'URL ne doit plus changer une fois Android MDM activé** — le Pub/Sub Google est lié à cette URL précise, un changement casse tout (pas de fix simple à ce jour, issue connue [#29878](https://github.com/fleetdm/fleet/issues/29878)). Cohérent avec le choix déjà fait d'un **domaine ngrok statique** plutôt qu'aléatoire
- ⚠️ **Ne pas utiliser un compte Gmail personnel** pour le signup Android Enterprise — les comptes perso n'ont généralement pas le quota nécessaire pour enroller des devices. Utilise un compte **Google Workspace** ou **Cloud Identity** si possible

---

## 2. Activer Android MDM dans Fleet

- [ ] Ouvrir une fenêtre de navigation **privée/incognito** (pour ne pas signer avec un autre compte Google déjà connecté)
- [ ] Dans Fleet : **Settings → Integrations → MDM → Turn on Android → Connect**
- [ ] Fleet redirige vers le flow de signup Google Enterprise — choisir **"Sign-up for Android only"**
- [ ] Le nom de domaine demandé n'a pas d'importance réelle pour un test (ex. `test.com`)
- [ ] Sections "Data protection officer" / "EU representative" : rien à remplir, juste cocher la case de confirmation
- [ ] Valider — Fleet reçoit la confirmation via un callback + Server-Sent Events, l'UI doit afficher **"Android enabled"**

Séquence technique en arrière-plan (pour comprendre ce qui se passe) : Fleet demande une URL de signup à `fleetdm.com`, qui la demande à Google → tu t'authentifies chez Google → Google notifie Fleet en callback → Fleet (via `fleetdm.com`) crée l'enterprise + la policy + l'abonnement Pub/Sub côté Google.

---

## 3. Choisir : device réel (BYOD) ou émulateur

Les deux fonctionnent.

- **Émulateur** : passer par **Android Studio**, créer un émulateur avec une image système **Google Play** (⚠️ pas "Google APIs" ni AOSP — seules les images Google Play embarquent la stack Google Mobile Services complète nécessaire à Android Enterprise)
- **Device réel (BYOD)** : doit supporter la création de profils (work profile) — c'est le cas de la quasi-totalité des Android modernes

---

## 4. Enroller un device (flow BYOD / work profile)

C'est le flow documenté par défaut dans Fleet (voir diagramme "Enroll BYOD Android device" dans `android-mdm.md`) :

- [ ] Dans Fleet, générer/récupérer le **lien de signup** pour l'enrollment Android (équivalent de l'enroll secret côté Apple, mais le flow passe par une page web dédiée)
- [ ] Ouvrir ce lien **sur le device Android** (ou l'émulateur) → une page d'enroll Fleet s'affiche
- [ ] Cliquer sur **"Enroll"** → Fleet récupère un token d'enrollment (Fleet → fleetdm.com → Google) → redirection automatique vers le flow d'enrollment Android natif de Google
- [ ] Suivre les écrans natifs Android jusqu'à la confirmation "Device enrolled"
- [ ] En arrière-plan, Google notifie Fleet via **Pub/Sub** : d'abord un événement `ENROLLMENT`, puis des `STATUS_REPORT` réguliers
- [ ] Le host apparaît dans Fleet (**Hosts**) avec la plateforme Android

---

## 5. Vérification

- [ ] **Hosts** → filtrer par plateforme Android → le device doit apparaître avec statut MDM **"On"**
- [ ] Si l'enrollment plante avec `DPM_PRECONDITION_CHECK_FAILED_FOR_PROFILE_OWNER` → un ancien work profile traîne, à nettoyer :
  ```bash
  adb shell pm list users                     # repérer le "Work profile" existant
  adb shell pm remove-user <id-du-profil>      # le supprimer
  ```
- [ ] Si l'activation échoue avec **"This enterprise is already enrolled with another EMM"** (arrive si un compte Gmail perso a déjà servi à un test précédent) → aller sur **https://play.google.com/work** → "Admin settings" → supprimer l'ancienne organisation créée, puis réessayer l'étape 2

---

## Alternative — projet Google Cloud dédié (pas nécessaire pour un premier test)

À ne considérer que si la limite "1 enterprise par URL" du proxy fleetdm.com devient bloquante, ou pour un usage prod indépendant de fleetdm.com :

- Créer un projet GCP + service account avec les rôles **Android Management User** et **Pub/Sub Admin** (voir liens dans `docs/Contributing/product-groups/mdm/android-mdm.md`)
- Configurer côté serveur Fleet :
  ```bash
  export FLEET_DEV_ANDROID_GOOGLE_CLIENT=1
  export FLEET_DEV_ANDROID_GOOGLE_SERVICE_CREDENTIALS=$(cat credentials.json)
  ```
- Ces variables sont lues directement via `os.Getenv` dans `server/mdm/android/service/service.go:115` et `androidmgmt/google_client.go` — pas besoin de passer par le fichier `.env.prod`/docker-compose habituel, juste que le process `fleet serve` les ait dans son environnement

---

## Pièges classiques (mémo)

- **Compte Gmail personnel** pour le signup → quota d'enrollment insuffisant, échec silencieux ou limité
- **Server URL en localhost** → Android MDM refuse de s'activer (Pub/Sub ne peut pas joindre le serveur)
- **Changement d'URL après activation** → Pub/Sub cassé, pas de procédure de migration simple à ce jour
- **Image émulateur "Google APIs" au lieu de "Google Play"** → l'enrollment Android Enterprise échoue, la stack Google Mobile Services est incomplète
- **Reset de la DB dev sans désactiver Android MDM avant** → l'enterprise Google reste "accrochée" à l'URL du serveur, complique une réactivation propre
