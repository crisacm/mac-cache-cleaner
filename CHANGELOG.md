# Changelog

Todos los cambios notables de este proyecto se documentan en este archivo.

El formato está basado en [Keep a Changelog](https://keepachangelog.com/es-ES/1.1.0/)
y este proyecto se adhiere a [Versionado Semántico](https://semver.org/lang/es/).

## [No publicado]

### Añadido

- Bootstrap del proyecto: módulo Go, esqueleto de paquetes según `AGENTS.md` §3
  (`domain`, `policy`, `scanner`, `apps`, `cache`, `orphan`, `large`, `store`,
  `cleaner`, `platform`, `config`, `cli`), `Makefile` (`fmt`, `lint`, `vuln`,
  `test`, `build`, `run`, `clean`, `check`), `.golangci.yml` (incl. `depguard`
  contra paquetes de red), `.gitignore` y esqueleto de `testdata/` y `specs/`.
- **Dominio** (`internal/domain`): clases de dato (`regenerable_cache`,
  `app_data`, `user_file`, `application`), niveles de acceso (0–3), entradas con
  `entry_id` estable (`ComputeEntryID`), tipos de `Scan`/`ScanResult`, `App`,
  `CacheEntry`, `Orphan`, `LargeItem`, `Plan`/`PlanItem`, `Consent`,
  `AuditEntry` y errores centinela con los códigos de salida.
- **Store** (`internal/store`): repositorio SQLite sin CGO
  (`modernc.org/sqlite`) con migraciones embebidas (`pressly/goose`, directorio
  `migrations/`), directorio `0700` y archivo `0600`. Incluye escaneos, entradas,
  rutas inaccesibles, consentimientos, planes e ítems, y registro de auditoría
  de solo-anexado; además `Info` y `Purge` para `mcc data`.
- **Policy** (`internal/policy`): único lugar que decide accesos. Tabla de
  niveles 0–3 compilada en el binario, comprobación por **límite de ruta**
  (`Caches` ≠ `Caches-evil`), rechazo de `..`/NUL/rutas relativas, resolución de
  symlinks sin escapar de la raíz, consentimiento por raíz para niveles 1 y 3,
  negativa a ejecutar como root (`euid == 0`) y verificaciones de borrado (nivel
  allowlisted, SIP, `com.apple.*`, niveles 2/3). Incluye las pruebas de
  seguridad de `SECURITY.md` §10.
- **Scanner** (`internal/scanner`): recorrido concurrente con *worker pool*
  acotado (cola sin *deadlocks*), tamaño real `st_blocks*512` junto al lógico,
  deduplicación de hardlinks por `(dev, inode)`, sin seguir symlinks, sin cruzar
  dispositivos, paquetes de biblioteca como ítem opaco, `EPERM`/`EACCES` como
  resultado parcial y escaneo incremental por `mtime` de directorios. La
  persistencia incremental vive en `store` (`NewIncremental`, `CopySubtree`).
- **Platform** (`internal/platform`): capa `stat` con build tags (`darwin` y
  stub para el resto).
- **Apps** (`internal/apps`): descubrimiento de aplicaciones y lectura del
  `Info.plist` (única lectura de contenido permitida, con tope de 1 MiB,
  `SEC-ACC-02`), con tamaño de la app y de sus datos asociados desde el índice.
- **Cache** (`internal/cache`): registro de ubicaciones de caché basado en datos
  (sistema y desarrollo: Gradle, Android, Xcode, npm, pnpm, Go, Homebrew,
  CocoaPods, Yarn, Cargo, Docker) con su clase de dato.
- **Large** (`internal/large`): detección de archivos/directorios pesados con
  umbral por defecto de 1 GiB, top-N acotado con *min-heap* y resultados de
  Nivel 1 solo para reportar (`user_file`).
- **Orphan** (`internal/orphan`): detección de datos huérfanos en `~/Library`
  con confianza `high` (ID completo), `medium` (prefijo de equipo) o `low`
  (solo nombre); excluye `com.apple.*` y Nivel 2 y **degrada** la confianza si
  alguna ubicación de apps no es accesible.
- **Cleaner** (`internal/cleaner`): flujo plan -> aplicar. El plan es inmutable
  con ID, *hash* y vencimiento (por defecto 1 h); `apply` exige confirmación
  interactiva con cantidad y tamaño total, revalida cada ítem (ruta permitida,
  no symlink, mismo `(dev, inode)`, tamaño y `mtime`), omite cachés de apps en
  ejecución, mueve a la Papelera por defecto (rename a `~/.Trash`, con respaldo
  vía `osascript`/Finder y argv explícitos), admite borrado permanente solo con
  bandera + confirmación extra, y escribe el registro de auditoría.
- **CLI** (`internal/cli`): comandos `scan` (`--root`, `--full`, `--explain`),
  `apps` (`--sort`), `cache` (`--app`), `orphans` (`--min-confidence`), `large`
  (`--min`, `--path`), `clean` (`--plan`, `--apply`, `--plan-id`, `--permanent`,
  `--entry`), `report` (`--json`, `--md`, `--redact`), `permissions`
  (`show` | `grant` | `revoke`) y `data` (`show` | `purge`). Los comandos de
  lectura aceptan `--json` y `--no-color`; la salida sanitiza caracteres de
  control y ANSI; códigos de salida 0/1/2/3/4 (3 = resultado parcial). Incluye
  configuración con viper (`~/.config/mcc/config.yaml`, claves cerradas, sin
  perillas de política) y *golden files* en `internal/cli/testdata/`.

### Notas / decisiones

- `clean --plan` **nunca** incluye las raíces de la lista blanca (p. ej.
  `~/Library/Caches`) por `SEC-DEL-05`; en su lugar ofrece sus **hijos
  directos** (las cachés reales por app) y omite las cachés de apps en
  ejecución. Los datos de app (`app_data`) requieren selección explícita con
  `--entry` (advertencia "puede contener datos del usuario").
- La confirmación de `clean --apply` exige la palabra exacta `yes` (no basta
  Enter); el borrado permanente exige además `DELETE`. El consentimiento de
  Nivel 1/3 solo se concede de forma interactiva en un TTY (`mcc permissions
  grant <root>`).
- En la revalidación de `apply`, el tamaño se compara **solo en archivos**: las
  entradas de directorio llevan el tamaño de subárbol, así que un directorio se
  revalida por `mtime` + `(dev, inode)`.
- Los `fixtures` se generan en `t.TempDir()`; los *golden files* del CLI viven
  en `internal/cli/testdata/`.
- Respecto al esquema del `README.md`, el índice añade `dev`/`inode` a `entries`
  (necesarios para deduplicar hardlinks y revalidar en `apply`), estado
  (`finished_at`, `full`, `partial`) a `scans`, la tabla `inaccessible` para
  resultados parciales (`SEC-ACC-05`) y `total_size` a `plans`.
- El proyecto no hace ninguna llamada de red; `depguard` lo hace cumplir
  (`SEC-COD-07`).

