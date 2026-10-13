# Fleet — frontend Nuxt 4 (POC)

Prototype de réécriture du front Fleet avec **Nuxt 4** et **Nuxt UI 4**.
Il parle à la même API REST que le front React (`/api/latest/fleet/*`).

Périmètre actuel :

- Connexion email / mot de passe (`POST /login`), déconnexion, routes protégées
- Liste des hosts avec recherche, filtre par statut, tri et pagination côté serveur
  (`GET /hosts`, `GET /hosts/count`)

Hors périmètre pour l'instant : MFA par email, SSO, détail d'un host, les autres pages.

## Lancer en local

> Guide complet pas à pas (Docker, compilation de Fleet, migrations, compte admin, machines
> simulées) : [LANCEMENT-LOCAL.md](./LANCEMENT-LOCAL.md).

Le projet utilise **pnpm** (version fixée par `packageManager`). Si pnpm n'est pas installé :
`corepack enable pnpm`.

```bash
cd frontend-nuxt
cp .env.example .env   # puis ajuster FLEET_API_URL
pnpm install
pnpm dev
```

`FLEET_API_URL` pointe vers un serveur Fleet (par défaut `https://localhost:8080`,
le serveur de dev lancé par `make serve` / `fleet serve --dev`). En dev, Nuxt proxifie `/api/**`
vers ce serveur : le navigateur reste sur la même origine, donc pas de CORS. Le certificat
auto-signé du serveur de dev est accepté.

## Structure

```
app/
  app.config.ts            couleurs du thème (branding)
  composables/useAuth.ts   session : login, logout, utilisateur courant
  composables/useFleetApi.ts  client $fetch avec le token Bearer, logout sur 401
  middleware/auth.global.ts   protège toutes les routes sauf /login
  layouts/default.vue      layout dashboard (UDashboardGroup + sidebar)
  layouts/auth.vue         layout de la page de connexion
  pages/login.vue
  pages/hosts/index.vue
  types/fleet.ts           types API (sous-ensemble de frontend/interfaces/)
  utils/hosts.ts           statut, icône de plateforme, dates relatives
```

## Authentification

Le token est stocké dans le **même cookie que le front React** (`__Host-token` en HTTPS,
`token` en HTTP ; voir `frontend/utilities/auth_token/index.ts`). Si les deux fronts sont servis
sur la même origine, la session est partagée : on peut migrer page par page.

## Déploiement (pistes)

L'app tourne en mode SPA (`ssr: false`). Deux options :

1. **Statique derrière un reverse proxy** : `pnpm generate`, servir `.output/public`, et router
   `/api/*` (et les websockets des live queries) vers Fleet. C'est l'option la plus simple.
2. **Embarquée dans le binaire Go** : remplacer les assets webpack servis par
   `server/service/frontend.go`. Il faudra gérer le nonce CSP et `urlPrefix` du template
   `frontend/templates/react.tmpl`.

## Ajouter une page

1. Copier les types utiles depuis `frontend/interfaces/` vers `app/types/`.
2. Repérer l'appel API dans `frontend/services/entities/` et l'appeler via `useFleetApi()`.
3. Créer la page dans `app/pages/` avec les composants dashboard de Nuxt UI
   (`UDashboardPanel`, `UDashboardNavbar`, `UTable`, …), puis activer l'entrée dans
   `layouts/default.vue`.
