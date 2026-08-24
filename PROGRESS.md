# PROGRESS

Estado del grafo de fases. Una fase no se da por hecha hasta que su verificador
devuelve exit code 0.

| Fase | Qué | Estado | Verificador |
|------|-----|--------|-------------|
| F0 | Base: kind + operador + monorepo | ✅ **PASA** | `make verify-f0` |
| F1 | Backstage base (Postgres, catálogo, discovery) | ⬜ pendiente | `yarn dev` + catálogo persiste reinicio |
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
