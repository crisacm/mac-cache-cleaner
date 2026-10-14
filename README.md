# MCC — Mac Cache Cleaner

Herramienta de línea de comandos escrita en Go para **indexar y consultar el espacio en disco** que ocupan las aplicaciones, sus cachés, los datos de aplicaciones huérfanas y los archivos o directorios pesados (> 1 GB), con el fin de mantener el almacenamiento del equipo bajo control.

A futuro, las mismas capacidades se expondrán como un **servidor MCP** para que agentes de IA puedan escanear el sistema, conocer el peso de cachés, apps y ubicaciones, y proponer qué eliminar (siempre con aprobación humana).

> Estado: **Fase 1 (CLI) implementada**. La TUI (fase 2) y el servidor MCP (fase 3) todavía no.

## Roadmap

| Fase | Entregable | Estado |
|---|---|---|
| 1 | CLI: `scan`, `apps`, `cache`, `orphans`, `large`, `clean`, `report`, `permissions`, `data` | Implementado |
| 2 | TUI interactiva (`mcc tui`) | Pendiente |
| 3 | **Servidor MCP** (stdio): herramientas de solo lectura y de planificación para agentes | Futuro (el código se diseña desde ya para soportarlo) |

Fuera de alcance: demonios en segundo plano, borrado automático, telemetría, servicios de red, elevación de privilegios, lectura del contenido de archivos y otros sistemas operativos.

## Características

- Índice local (SQLite) del uso de disco, con re-escaneo incremental.
- Listado de aplicaciones instaladas con el tamaño de la app y de sus datos asociados.
- Detección de **datos huérfanos** (restos de apps desinstaladas) con nivel de confianza.
- Búsqueda de **archivos y directorios pesados** con umbral configurable.
- Cachés de desarrollo (Gradle, Android SDK/AVD, Xcode DerivedData, npm, pnpm, Go, Homebrew, Docker).
- Salida legible o `--json` para scripts.
- Limpieza segura: plan -> aplicar, *dry-run* por defecto, Papelera y lista blanca de rutas.
- Transparencia: puedes ver qué se accede, qué se guarda y borrarlo (`mcc permissions`, `mcc data`).

## Requisitos

- macOS (el objetivo principal; la capa de plataforma está aislada).
- Go (versión estable más reciente; ver `go.mod`).
- Para escanear `~/Library` por completo conviene conceder **Acceso total al disco** a tu terminal. Sin él, algunas rutas se reportan como inaccesibles (resultado parcial). MCC nunca intenta saltarse los controles de privacidad de macOS.

## Instalación

```bash
git clone https://github.com/crisacm/mac-cache-cleaner.git mcc && cd mcc
make build          # genera ./bin/mcc (sin CGO)
```

## Uso

```bash
mcc scan [--root PATH] [--full] [--explain]   # indexa el disco (incremental por defecto)
mcc apps [--sort size] [--json]               # apps y su espacio total
mcc cache [--app ID] [--json]                 # cachés por aplicación
mcc orphans [--min-confidence high]           # datos de apps desinstaladas
mcc large [--min 1GB] [--path PATH]           # archivos/directorios pesados
mcc clean --plan                              # crea un plan (dry-run), no cambia nada
mcc clean --apply [--plan-id ID]              # ejecuta un plan (pide confirmación)
mcc report [--json|--md] [--redact]           # reporte consolidado
mcc permissions [show|grant|revoke <root>]      # política efectiva y consentimientos
mcc data show | purge                         # qué se guarda, dónde, y cómo borrarlo
mcc tui                                       # interfaz interactiva (fase 2)
```

Los comandos de lectura aceptan `--json` y `--no-color`.

**Códigos de salida:** `0` éxito · `1` error general · `2` uso incorrecto · `3` resultado parcial (rutas inaccesibles) · `4` bloqueado por política / falta consentimiento.

## Seguridad y privacidad

Las reglas completas están en [`SECURITY.md`](./SECURITY.md). Resumen:

- **Solo metadatos**: MCC lee tamaños, fechas y nombres; nunca el contenido de tus archivos (única excepción: el `Info.plist` de las apps para obtener su identificador).
- **Acceso por niveles**:
  - *Nivel 0* (por defecto): apps y cachés (`~/Library/Caches`, `Application Support`, cachés de desarrollo…).
  - *Nivel 1* (datos del usuario: Documentos, Imágenes, Películas…): solo con tu consentimiento por carpeta y **solo para reportar**, nunca se incluyen en planes automáticos.
  - *Nivel 2* (sensibles: Fotos internas, Llaves, iCloud, Mail, Mensajes, `~/.ssh`…): **nunca se acceden**, ni con consentimiento.
  - *Nivel 3* (discos externos, red, Time Machine): solo con `--root` explícito y consentimiento; Time Machine nunca.
- **Consentimiento** por carpeta, solo interactivo, registrado y revocable. `--yes` no concede consentimiento.
- **Limpieza segura**: plan inmutable con vencimiento, revalidación de cada ítem antes de actuar, **Papelera por defecto**, lista blanca de rutas, no se toca SIP ni `com.apple.*`, se omiten cachés de apps en ejecución. `mcc clean --apply` muestra la cantidad y el tamaño total y exige escribir `yes` (el borrado permanente exige además `DELETE`). Las raíces de la lista blanca (p. ej. `~/Library/Caches`) nunca entran en un plan; se ofrecen sus hijos directos.
- **Sin telemetría ni red**: nada sale de tu equipo.
- **Datos locales**: `~/Library/Application Support/mcc/` (permisos `0700`/`0600`). Con `mcc data show` ves qué hay y con `mcc data purge` lo borras.
- No se ejecuta como root ni se piden privilegios.

## Arquitectura

Arquitectura hexagonal ligera: dominio puro y adaptadores en los bordes.

```
cmd/mcc/main.go        # solo wiring (inyección manual de dependencias)
internal/
  domain/              # tipos puros: App, CacheEntry, Orphan, LargeItem, Plan, ScanResult
  policy/              # niveles, lista blanca/negra, consentimiento: ÚNICO lugar que decide accesos
  scanner/             # walker concurrente y cálculo de tamaños
  apps/                # descubrimiento de apps y lectura de Info.plist
  cache/               # registro de ubicaciones de caché (basado en datos)
  orphan/              # detección de huérfanos y scoring de confianza
  large/               # detección de archivos/directorios pesados
  store/               # repositorio SQLite y migraciones embebidas
  cleaner/             # plan (dry-run) -> aplicar -> Papelera
  platform/            # interfaces + implementación darwin (build tags)
  config/              # viper -> struct tipado
  cli/                 # comandos cobra (delgados, sin lógica de negocio)
  tui/                 # modelos bubbletea (fase 2)
migrations/            # SQL embebido
testdata/              # fixtures de un ~/Library falso
```

**Reglas de dependencia**

- `cli`/`tui` -> servicios -> `domain`; `domain` no importa nada del resto.
- `store`, `platform` y `cleaner` se consumen mediante interfaces definidas **en el paquete consumidor**.
- **Toda decisión de acceso pasa por `internal/policy`.**
- La lógica de negocio nunca vive en `cli/`, `tui/` ni `main.go`.

### Preparado para MCP (fase 3)

Para que el servidor MCP sea una adición y no una reescritura:

- Los servicios devuelven valores de dominio estructurados y **nunca imprimen**; el renderizado vive en `cli/` y `tui/`.
- `plan` y `apply` son operaciones separadas; los planes se persisten con `plan_id` estable.
- Las entradas del índice tienen `entry_id` estable; las operaciones de plan/apply usan IDs, no rutas libres.
- Cada resultado lleva su clase de dato (`regenerable_cache`, `app_data`, `user_file`, `application`).

Cuando llegue la fase 3, el MCP usará transporte stdio, comenzará con herramientas de solo lectura y de planificación, y **no podrá aplicar borrados sin confirmación humana fuera del canal del agente** (ver `SECURITY.md` §8).

### Esquema del índice (SQLite)

- `scans(id, started_at, root)`
- `entries(entry_id, scan_id, path, parent, allocated_bytes, logical_bytes, mtime, is_dir, kind, data_class, bundle_id)`
- `consents(root, tier, granted_at, revoked_at)`
- `plans(plan_id, hash, created_at, expires_at, status)` y `plan_items(plan_id, entry_id, path, dev, inode, size, mtime, data_class)`
- `audit_log(id, at, action, path, size, plan_id)` (solo se agrega, nunca se edita)
- Índices en `(scan_id, kind)` y `(scan_id, allocated_bytes DESC)`.

## Decisiones técnicas importantes

- **Tamaño real en disco** = `st_blocks * 512`, no `st_size` (archivos sparse y APFS).
- **Hardlinks** deduplicados por `(dev, inode)`.
- **Symlinks** nunca se siguen (`Lstat`).
- **Dispositivos y firmlinks**: no se cruzan por defecto; se usan raíces de escaneo explícitas.
- **Permisos (TCC)**: `EPERM`/`EACCES` no son fatales; se reportan y el resultado se marca como parcial.
- **Paquetes de biblioteca** (`*.photoslibrary`, etc.): se tratan como un solo ítem opaco, solo tamaño.
- **Rendimiento**: `filepath.WalkDir` con *worker pool* acotado; top-N en memoria para archivos pesados.
- **Incremental**: se reutiliza el `mtime` de directorios para omitir subárboles sin cambios.
- **Huérfanos**: bundle IDs en `~/Library/*` sin app instalada, con confianza *alta* (ID exacto), *media* (prefijo de equipo) o *baja* (solo nombre). Los de confianza baja nunca entran a un plan salvo selección explícita; se degradan si alguna ubicación de apps no es accesible.

## Stack

| Necesidad | Librería |
|---|---|
| CLI / configuración | `spf13/cobra`, `spf13/viper` |
| TUI (fase 2) | `charmbracelet/bubbletea`, `lipgloss`, `bubbles` |
| Base de datos | `modernc.org/sqlite` (sin CGO) |
| Migraciones | SQL embebido + `pressly/goose` |
| Concurrencia | stdlib + `golang.org/x/sync` |
| Plist | `howett.net/plist` |
| Syscalls | `golang.org/x/sys/unix` |
| Logging | `log/slog` |
| Tests | stdlib + `stretchr/testify` |
| Calidad y seguridad | `golangci-lint` (con `depguard`), `gofumpt`, `govulncheck`, `gosec` |
| Release | `goreleaser` |

## Convenciones de código

- `context.Context` como primer parámetro en toda función con I/O.
- Errores envueltos con `%w`; errores centinela o tipados cuando el llamador deba distinguirlos.
- Interfaces pequeñas, definidas en el consumidor.
- Sin estado global ni `init()` con efectos; inyección por constructores.
- Filesystem abstraído (`fs.FS` o interfaz propia) para testear sin tocar el disco real.
- Código específico de plataforma con build tags (`//go:build darwin`) y stub para el resto.
- Logging estructurado con `slog`.
- Rutas limpias, absolutas y con comparación por límite de directorio (`Caches` no coincide con `Caches-evil`).
- Nombres de archivo tratados como datos no confiables: se sanitizan caracteres de control y secuencias ANSI al imprimir.
- SQL siempre parametrizado; subprocesos con ruta absoluta y argumentos explícitos (sin `sh -c`).
- Formato `gofumpt` y lint con `golangci-lint` (`errcheck`, `govet`, `staticcheck`, `gosec`, `revive`, `depguard`).
- Constantes con nombre y configurables (por ejemplo, el umbral de 1 GB).

## Desarrollo

```bash
make fmt      # gofumpt
make lint     # golangci-lint
make vuln     # govulncheck ./...
make test     # go test -race ./...
make build    # CGO_ENABLED=0 go build ./...
make check    # todo lo anterior
```

### Pruebas

- Tests *table-driven* con `testify`; fixtures en `t.TempDir()` y un `~/Library` falso en `testdata/`.
- *Golden files* para la salida del CLI (`internal/cli/testdata/*.golden`).
- Funcionales: dedup de hardlinks, archivos sparse, no seguir symlinks, `EPERM`, scoring de huérfanos, dry-run vs apply.
- **De seguridad** (ver `SECURITY.md` §10): Nivel 2 nunca recorrido, consentimiento para Niveles 1/3, escapes por symlink o `..`, cambio de ítem entre plan y apply (TOCTOU), expiración de planes, negativa a ejecutar como root, cachés de apps en ejecución omitidas, sanitización de nombres hostiles, permisos del almacén, sin imports de red.
- Los tests nunca deben tocar el directorio home real.

### Definición de terminado

Pasan `fmt`, `lint`, `vuln`, `test` (con `-race`) y `build` sin CGO; se cumple el checklist de `SECURITY.md` §12; la documentación/spec está actualizada y el `CHANGELOG.md` tiene su entrada en `[Unreleased]`.

## Flujo de trabajo

- **Spec-driven development** con OpenSpec: una spec por capacidad (`scan`, `apps`, `cache`, `orphans`, `large`, `clean`, `policy`, `tui`).
- **Conventional Commits** (`feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `chore:`) y **SemVer**.
- **Keep a Changelog**, escrito en español.
- Un cambio lógico por commit/PR; no mezclar refactors con cambios de comportamiento.

### Orden de construcción

1. `domain` + `store` + `policy` + migraciones
2. `scanner` (hardlinks, `st_blocks`, `EPERM`)
3. `apps` + `cache`
4. `large`
5. `orphan`
6. `cleaner` (plan primero, luego apply)
7. `cli` (todos los comandos)
8. `tui` (fase 2)

## Trabajo con agentes de IA

Las instrucciones para agentes están en [`AGENTS.md`](./AGENTS.md) y las reglas de seguridad obligatorias en [`SECURITY.md`](./SECURITY.md). Los agentes deben leer ambos antes de modificar código.

## Reportar vulnerabilidades

No abras issues públicos para problemas de seguridad. Consulta el procedimiento en [`SECURITY.md`](./SECURITY.md) §11.

## Licencia

Por definir.