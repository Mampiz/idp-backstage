# PROGRESS

Estado del grafo de fases. Una fase no se da por hecha hasta que su verificador
devuelve exit code 0.

| Fase | Qué | Estado | Verificador |
|------|-----|--------|-------------|
| F0 | Base: kind + operador + monorepo | ✅ **PASA** | `make verify-f0` |
| F1 | Backstage base (Postgres, catálogo, discovery) | 🟡 **verificador PASA**, discovery bloqueado | `make verify-f1` |
| F2 | [GO] status-api | ⬜ pendiente | `go test ./...` + `curl :8081/api/webapps` |
| F3 | [GO] scaffolder service | ⬜ pendiente (checkpoint) | curl crea repo real + CR + pods Ready |
| F4 | Software Template de Backstage | ⬜ pendiente | formulario en `/create` → repo + CR + pods |
| F5 | Plugin frontend webapp-status | ⬜ pendiente (checkpoint) | tab refleja `kubectl scale` real |
| F6 | Cierre: TechDocs, README, diagrama, GIF | ⬜ pendiente | — |

---

## F0 · BASE — ✅ PASA

**Verificador:** `make verify-f0` → exit 0.
Reconstrucción completa desde cero (`make cluster-down && make bootstrap && make verify-f0`)
verificada: **75 segundos**, sin pasos manuales.

### Qué hice

- Cluster `kind` dedicado **`idp-local`** (`infra/kind/idp-local.yaml`), node image
  pinneada por digest (`kindest/node:v1.36.1@sha256:21c46cf6…`), con
  `extraPortMappings` 30080/30081 reservados para exponer WebApps en demos (F5/F6).
- **cert-manager v1.21.1** instalado antes del operador (ver decisión 2).
- **webapp-operator v1.0.0** instalado desde su `dist/install.yaml` a tag fijo.
- RBAC suplementario para eventos del operador (`infra/operator/events-rbac.yaml`, ver decisión 4).
- Monorepo: `go.work` + módulos Go esqueleto (`services/status-api`, `services/scaffolder`),
  `Makefile` raíz con targets autodocumentados, `.gitignore` y `.env.example` desde el commit 1.
- Verificador `infra/scripts/verify-f0.sh`: 6 asserts, exit code como veredicto.

### Decisiones de diseño

**1. Cluster nuevo `idp-local`, no reutilizar `webapp-dev`.**
Ya existía un cluster `webapp-dev` (del desarrollo del operador) con el CRD suelto,
sin cert-manager ni el operador desplegado. Reutilizarlo habría hecho el arranque
dependiente de estado manual previo, no reproducible. `idp-local` se levanta entero
desde el Makefile. `webapp-dev` queda intacto.

**2. cert-manager es prerequisito obligatorio del operador — no está documentado upstream.**
`dist/install.yaml` incluye `Certificate`/`Issuer` de `cert-manager.io/v1` y ambas
webhook configurations llevan la anotación `cert-manager.io/inject-ca-from`. Sin
cert-manager, el `caBundle` queda vacío y, como las webhooks tienen
`failurePolicy: fail`, **todo `kubectl apply` de un WebApp se rechaza**. El README del
operador solo lista "un cluster y kubectl" como prerequisitos. Lo asumo en mi
`make bootstrap` y el verificador comprueba explícitamente que el `caBundle` está
inyectado, porque es el fallo más probable y el más opaco de diagnosticar.

**3. Ninguna imagen de este repo usará `:latest`.**
El validating webhook del operador (`validateImageTag`) rechaza tanto `:latest` como
los tags implícitos. Nota: `config/samples/platform_v1_webapp.yaml` y el quick-start
del README del operador usan `nginx:latest`, o sea que **el ejemplo oficial no pasa su
propia validación**. Consecuencias aguas abajo, ya anotadas para F3/F4:
- el scaffolder Go debe validar el tag *antes* de aplicar el CR, y devolver un 400
  legible en vez de dejar que reviente en admisión;
- el formulario de Backstage debe llevar validación en el campo `image`.
El verificador F0 incluye un assert negativo (paso 5) que confirma que la webhook
efectivamente rechaza `:latest`: así queda probado que la admisión funciona, no solo
que el camino feliz funciona.

**4. RBAC de eventos: parche aditivo por mi lado, no toco el operador.**
El controller usa un `EventRecorder` (`DeploymentReconciled`, `ServiceReconciled`,
`HPAReconciled`, `ReconcileFailed`) pero `webapp-operator-manager-role` no tiene regla
para `events`, así que la API los rechazaba todos con `events is forbidden`. La
reconciliación funcionaba igual, pero `kubectl describe webapp` no mostraba nada y F5
no podría pintar eventos. Como no modifico el repo del operador, añado un
ClusterRole+Binding suplementario en `infra/operator/events-rbac.yaml`, aplicado por
`make operator-install` y documentado con la razón y la condición de borrado.
**Es un bug del operador**: la solución correcta es añadir la regla a
`config/rbac/role.yaml` upstream y regenerar `dist/install.yaml`.

**5. Convención de nombres de los hijos: `<webapp>-deployment` / `-service` / `-autoscaler`.**
El operador no llama a los objetos hijos igual que al WebApp, les pone sufijo. Está
codificado en el verificador y es un dato que **F2 (status-api) y F5 (plugin) necesitan**
para resolver un WebApp a su workload real. Anotado aquí para no volver a descubrirlo.

**6. Todo `kubectl` lleva `--context=kind-idp-local` explícito.**
No dependo del contexto activo. Además el verificador aborta si el API server no es
`127.0.0.1`/`localhost`: es imposible que estas herramientas toquen un cluster remoto
por accidente.

### Qué queda / notas para las siguientes fases

- `yarn` no está instalado en la máquina; F1 lo habilitará vía `corepack` (Node v22.22.1, OK).
- No hay `metrics-server` en el cluster, así que el HPA muestra `cpu: <unknown>`. No
  afecta a F0 (el HPA se crea y el operador lo reconcilia). Si en F5/F6 quiero enseñar
  autoescalado real habrá que instalar `metrics-server` con `--kubelet-insecure-tls`.
- El scaffolder (F3) necesitará permisos sobre `webapps` desde fuera del cluster:
  el operador ya expone los ClusterRoles `webapp-operator-webapp-editor-role` y
  `-viewer-role`, reutilizables para el ServiceAccount de mis servicios Go.


---

## F1 · BACKSTAGE BASE — 🟡 verificador PASA, con un bloqueo abierto

**Verificador:** `make verify-f1` → exit 0. Comprueba que Backstage arranca contra
Postgres (no SQLite), que frontend y backend responden, que el catálogo sirve el
Component `webapp-operator` por API, y que el dato sigue en Postgres **con Backstage
apagado** después de reiniciar el contenedor.

### Qué hice

- `npx @backstage/create-app` → Backstage **1.54.0**, que ya trae el New Backend
  System (`backend.add(import(...))` en `packages/backend/src/index.ts`). No hay
  `createRouter` ni backend legacy en ningún punto.
- **Postgres 17.6** por `docker-compose.yaml` con volumen nombrado `idp-pgdata`,
  sustituyendo el `better-sqlite3 :memory:` de la plantilla.
- Entidades propias en `catalog/webapp-operator.yaml`: el Component
  `webapp-operator`, el System `idp` y el Group `platform`, registradas como
  static location en `app-config.yaml`.
- `@backstage/plugin-catalog-backend-module-github` instalado y añadido al backend.
- Targets `db-up` / `db-down` / `db-nuke` / `dev` / `verify-f1` en el Makefile.

### Decisiones de diseño

**1. El token de GitHub no vive en `.env`, vive en el entorno del shell.**
Tu regla dice "por env var, nunca en disco". `.env` está gitignoreado pero sigue
siendo disco, así que `.env` solo guarda configuración no secreta (Postgres, owner)
y `GITHUB_TOKEN` hay que exportarlo: `export GITHUB_TOKEN=$(gh auth token)`.
El Makefile tiene un guard (`require-github-token`) que falla con instrucciones
claras si no está. Backstage no arranca sin él: `integrations.github[0].token`
valida el tipo y una cadena vacía es un error de arranque, no un warning.

**2. La verificación de persistencia se hace con Backstage APAGADO.**
Reiniciar el contenedor y volver a preguntar por la API no prueba nada: la entidad
viene de una static location y Backstage la re-ingestaría igualmente. El verificador
mata Backstage, reinicia el contenedor y lee la fila directamente de
`final_entities` por `psql`. Si el dato sigue ahí es porque está en el volumen.

**3. Token estático de acceso externo para los verificadores.**
`backend.auth.externalAccess` de tipo `static` con `BACKSTAGE_VERIFY_TOKEN`, para
que los scripts llamen a la API del catálogo sin sesión de navegador. Es la vía
soportada; la alternativa (`dangerouslyDisableDefaultAuthPolicy`) apaga la
autenticación de todo el backend y no la quiero ni en local.

### 🚧 BLOQUEO: GitHub discovery no funciona con una cuenta personal

`Mampiz` es una cuenta de **usuario**, no una organización, y el
`GithubEntityProvider` de Backstage solo sabe descubrir organizaciones. Comprobado
en el código instalado y contra la API real:

```
$ grep -c 'organization(login: $org)' node_modules/@backstage/plugin-catalog-backend-module-github/dist/lib/github.cjs.js
5                       # todas las queries GraphQL del provider son organization(login:)
$ gh api graphql -f query='{ organization(login: "Mampiz") { login } }'
NOT_FOUND: Could not resolve to an Organization with the login of 'Mampiz'
```

No hay `user(login:)` ni `repositoryOwner` en el paquete: no es cuestión de
configuración, la funcionalidad no existe. Por eso `catalog.providers.github` está
sin rellenar y el módulo queda cargado pero inerte. **Pendiente de decisión tuya**,
las opciones están en la conversación. Afecta también a F3/F4 (dónde aterrizan los
repos scaffoldeados).

### Qué queda

- Decidir el approach de discovery y aplicarlo.
- El `examples/` de la plantilla (org.yaml, entities.yaml, template.yaml) sigue
  registrado; lo limpiaré en F4 cuando el template propio lo sustituya.
