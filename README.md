# idp-backstage

Internal Developer Platform sobre Backstage cuyo scaffolder no se queda en crear un
repositorio: aplica un recurso `WebApp` en Kubernetes que reconcilia un operador
propio ([Mampiz/webapp-operator](https://github.com/Mampiz/webapp-operator)).

Estado del proyecto y decisiones de diseño: [PROGRESS.md](PROGRESS.md).
(README definitivo en F6.)

## Arranque local

```bash
make bootstrap     # kind + cert-manager + webapp-operator
make verify-f0     # verificador de la fase 0
```

`make help` lista todos los targets.

## Estructura

```
backstage/              app Backstage (TypeScript, lo mínimo imprescindible)
services/status-api/    [GO] lee los WebApp CRs con client-go y los expone por REST
services/scaffolder/    [GO] crea el repo en GitHub y aplica el WebApp CR
plugins/webapp-status/  [TS] plugin de frontend que pinta el estado
infra/                  cluster kind, instalación del operador, verificadores
docs/                   TechDocs
```
