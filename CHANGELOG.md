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



### Notas

- El proyecto no hace ninguna llamada de red; `depguard` lo hace cumplir
  (`SEC-COD-07`).
- Respecto al esquema del `README.md`, el índice añade `dev`/`inode` a `entries`
  (necesarios para deduplicar hardlinks y revalidar en `apply`), estado
  (`finished_at`, `full`, `partial`) a `scans` y la tabla `inaccessible` para
  resultados parciales (`SEC-ACC-05`).

