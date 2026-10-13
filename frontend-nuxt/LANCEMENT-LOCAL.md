# Lancer l'environnement local (Fleet + front Nuxt)

Guide pas à pas pour faire tourner le backend Fleet et le front Nuxt sur sa machine.
Toutes les commandes se lancent depuis la **racine du repo** (`fleet/`), sauf indication contraire.

## Prérequis

- Docker Desktop
- Go (version du `go.mod`)
- Node 24
- pnpm (version fixée par `packageManager` dans `frontend-nuxt/package.json`). Le plus simple est
  de l'activer via corepack, fourni avec Node : `corepack enable pnpm`

## 1. Démarrer la base de données et le cache

Ouvrir Docker Desktop, puis :

```bash
docker compose up -d mysql redis
```

## 2. Compiler Fleet

À refaire seulement quand le code Go change. La première compilation prend plusieurs minutes.

```bash
make fleet fleetctl
```

## 3. Mettre la base à jour (migrations)

La première fois, puis après chaque `git pull` qui ajoute des migrations.

```bash
./build/fleet prepare db --dev
```

## 4. Lancer le serveur Fleet

Garder ce terminal ouvert.

```bash
./build/fleet serve --server_address=localhost:8080 --dev --dev_license
```

L'ancien front React est alors sur https://localhost:8080. Accepter l'avertissement de certificat :
en dev, le certificat est auto-signé.

> `make serve` fait la même chose (compilation + lancement) mais ne démarre **pas** MySQL/Redis
> et n'applique **pas** les migrations : les étapes 1 et 3 restent nécessaires.

> `--dev_license` active une licence Premium de développement. Elle est autorisée pour le
> développement et les tests uniquement, pas en production.

## 5. Créer le compte admin (une seule fois)

Sur une base neuve, ouvrir https://localhost:8080 et suivre l'assistant (utilisateur → organisation
→ URL Fleet → **Confirm**). Tant que ce compte n'existe pas, l'API répond 404 partout, login compris.

À refaire seulement si la base est remise à zéro.

## 6. Ajouter des machines simulées

Sans machine inscrite, la liste des hosts est vide. Fleet fournit un simulateur d'agents
(`cmd/osquery-perf`). Dans un **nouveau terminal** :

```bash
# Connecter fleetctl au serveur (une seule fois)
./build/fleetctl config set --address https://localhost:8080 --tls-skip-verify
./build/fleetctl login

# Récupérer le secret d'inscription (valeur "secret")
./build/fleetctl get enroll_secret

# Lancer 30 machines : 10 macOS, 10 Windows, 10 Ubuntu (remplacer TON_SECRET)
go run ./cmd/osquery-perf -enroll_secret TON_SECRET -host_count 30 \
  -os_templates macos_14.1.2:10,windows_11:10,ubuntu_22.04:10
```

Garder ce terminal ouvert : les machines n'apparaissent « en ligne » que tant que le simulateur tourne.
La somme des nombres dans `-os_templates` doit être égale à `-host_count`.

Modèles disponibles : voir les fichiers `*.tmpl` dans `cmd/osquery-perf/`
(`macos_14.1.2`, `windows_11`, `ubuntu_22.04`, `rhel_9`, `iphone_17`, `android`, …).

## 7. Lancer le front Nuxt

Dans un **nouveau terminal** :

```bash
cd frontend-nuxt
pnpm install       # première fois seulement
pnpm dev           # ajouter "--port 3001" si le port 3000 est déjà pris
```

Ouvrir l'URL affichée (par défaut http://localhost:3000) et se connecter avec le compte admin.

Pas besoin de fichier `.env` : par défaut, le front pointe sur `https://localhost:8080`.
Pour viser un autre serveur, définir `FLEET_API_URL` (voir `.env.example`).

## Tout arrêter

1. **Ctrl-C** dans chaque terminal (serveur Fleet, simulateur, front Nuxt).
2. Arrêter la base et le cache :

```bash
docker compose stop
```

## Au quotidien

Une fois la mise en place faite :

1. Docker Desktop, puis `docker compose up -d mysql redis`
2. Serveur Fleet (étape 4)
3. Simulateur, si besoin de machines en ligne (dernière commande de l'étape 6)
4. Front Nuxt : `cd frontend-nuxt && pnpm dev`

Les deux fronts tournent côte à côte : l'ancien sur `:8080`, le nouveau sur `:3000`.
C'est pratique pour comparer pendant la migration.

## Problèmes fréquents

| Symptôme                                                   | Cause / solution                                                       |
| ---------------------------------------------------------- | ---------------------------------------------------------------------- |
| L'API répond `404 page not found`                          | Le compte admin n'a pas été créé (étape 5).                            |
| `fleet serve` échoue au démarrage sur la base              | MySQL/Redis non démarrés (étape 1) ou migrations manquantes (étape 3). |
| `address already in use` sur 8080                          | Un autre `fleet serve` tourne déjà : l'arrêter avec Ctrl-C.            |
| Le front Nuxt affiche une page noire au premier chargement | Normal en dev : Vite compile à la demande, attendre quelques secondes. |
| Liste des hosts vide                                       | Aucune machine inscrite : lancer le simulateur (étape 6).              |
