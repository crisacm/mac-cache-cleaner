# Changelog

Todos los cambios notables de este proyecto se documentan en este archivo.

El formato está basado en [Keep a Changelog](https://keepachangelog.com/es-ES/1.1.0/)
y este proyecto se adhiere a [Versionado Semántico](https://semver.org/lang/es/).

## [No publicado]

### Añadido

- **Versión del binario** (`mcc --version`): se inyecta en el build con
  `-ldflags "-X main.version=…"` (el `Makefile` usa `git describe`), para que
  Homebrew y los usuarios puedan comprobarla.
- **Licencia MIT** (`LICENSE`) e **instalación por Homebrew**
  (`brew tap crisacm/tap && brew install mcc`) desde la fórmula publicada en el
  tap [`crisacm/homebrew-tap`](https://github.com/crisacm/homebrew-tap).
- **Skill distribuible como Agent Skill** con
  `npx skills add crisacm/mac-cache-cleaner --skill mcc-skill`; la skill ahora
  indica cómo instalar el binario (`brew`) cuando `mcc` no está disponible.
- **Servidor MCP** (`internal/mcp`, comando `mcc mcp`): servidor **stdio** (JSON-RPC)
  para agentes, con transporte propio sin dependencias nuevas. Expone 12
  herramientas de **solo lectura + `create_plan`/`get_plan`**; **no existe**
  ninguna herramienta para aplicar, borrar, escanear, conceder consentimiento,
  purgar ni cambiar configuración (`SEC-MCP-02`, `SEC-MCP-04`). Los agentes
  operan con **IDs opacos** (`entry_id`/`plan_id`), nunca con rutas
  (`SEC-MCP-03`); las raíces Nivel 2 se rechazan y las Nivel 1 exigen
  consentimiento existente (`SEC-MCP-09`). Salida JSON con `schema_version`,
  rutas acortadas a `~`, nombres saneados y `"untrusted_data": true`
  (`SEC-MCP-05`); descripciones de herramientas **estáticas** (`SEC-MCP-06`);
  paginación 50/500 (`SEC-MCP-07`); timeout de 30 s, límite de tasa de 120/min y
  **auditoría de cada llamada** en la tabla `mcp_calls` (`SEC-MCP-08`).
  Cada ítem lleva `data_class`, `cleanup_eligibility`, **`plannable`** +
  `plannable_reason`, `may_contain_user_data`, `running` y `mtime`, y hay
  `mcc_explain` (por qué es o no removible) y `mcc_verify_orphan` (re-comprueba
  si la app existe, degradando la confianza).
- **`internal/proc`**: descubrimiento de aplicaciones en ejecución (`ps` +
  `Info.plist`, solo metadatos) compartido por `cleaner` y el MCP.
- **Elegibilidad de limpieza** (`domain.DataClass.Eligibility()`):
  `auto_plan` | `explicit_selection_required` | `report_only`.
- **`orphan.Verify`** y `orphan.Classify`/`ConfidenceReason` exportados.
- **Auditoría MCP** (`migrations/00003_mcp_calls.sql`) y su recuento en
  `mcc data show` y en `data purge`.
- **Arranque de la TUI**: la primera pantalla aparece de inmediato con un
  indicador "Cargando índice…" (paso actual + directorio en lectura, en tiempo
  real); los datos se cargan en segundo plano y en paralelo
  (`apps.ListWithProgress`, `orphan.Options.Progress`).
- **Árbol en la TUI**: Caches/Orphans/Large se navegan como árbol con `→`/`l`
  (desplegar o entrar) y `←`/`h` (plegar o subir al padre); los hijos se cargan
  bajo demanda desde el índice (`store.EntriesByParent`), con tope de 500 y
  "… y N más". No se pueden seleccionar a la vez un directorio y algo dentro.
- **Toggle `f` en la TUI**: alterna entre mostrar solo directorios o también los
  archivos dentro de las carpetas desplegadas (por defecto solo directorios).
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
  ejecución, **mueve a la Papelera** (rename a `~/.Trash`, con respaldo vía
  `osascript`/Finder y argv explícitos), nunca borra del disco, y escribe el
  registro de auditoría.
- **CLI** (`internal/cli`): comandos `scan` (`--root`, `--full`, `--explain`),
  `apps` (`--sort`), `cache` (`--app`), `orphans` (`--min-confidence`), `large`
  (`--min`, `--path`), `clean` (`--plan`, `--apply`, `--plan-id`, `--entry`),
  `report` (`--json`, `--md`, `--redact`), `permissions`
  (`show` | `grant` | `revoke`) y `data` (`show` | `purge`). Los comandos de
  lectura aceptan `--json` y `--no-color`; la salida sanitiza caracteres de
  control y ANSI; códigos de salida 0/1/2/3/4 (3 = resultado parcial). Incluye
  configuración con viper (`~/.config/mcc/config.yaml`, claves cerradas, sin
  perillas de política) y *golden files* en `internal/cli/testdata/`.
- **Render** (`internal/render`): utilidades de presentación compartidas
  (`Sanitize`, `FormatBytes`, `Redact`) extraídas de `cli` para que la TUI use
  una única implementación del saneado (`SEC-COD-02`).
- **Progreso del scanner**: `scanner.Options.Progress` con instantáneas
  estructuradas y limitadas (sin imprimir), reutilizables por la TUI y el futuro MCP.
- **Servicio de escaneo** (`internal/scan`): orquesta un escaneo (valida raíces
  por política, persiste, vuelca entradas, registra inaccesibles y finaliza);
  **revierte** el escaneo en curso ante error o cancelación (`store.DeleteScan`,
  borrado en cascada) para que el último escaneo completo siga siendo el vigente.
- **Servicio de consentimiento** (`internal/consent`): `List`, `Verdict`,
  `Grant` y `Revoke`; `Grant` se rechaza sin TTY, para nivel 2/desconocido, e
  informa `ErrNotNeeded` para raíces por defecto (`SEC-ACC-07`).
- **TUI** (`internal/tui`, `mcc tui`): interfaz interactiva (Bubble Tea +
  Lipgloss + Bubbles) que reutiliza los servicios y no contiene lógica de
  negocio. Pantallas: Overview (totales, última vez, aviso parcial),
  Apps/Caches/Orphans/Large (orden y filtro, clase de dato, confianza),
  Scan (raíces efectivas con veredicto, progreso, cancelación con `Esc`),
  limpieza (selección -> revisión del plan -> escribir la **cantidad exacta**
  -> aplicar; solo Papelera) y Permissions (política efectiva y revocación de
  consentimientos). Los ítems `user_file`/`application` son de solo reporte;
  los nombres se sanitizan; el consentimiento exige confirmación que nombra la
  raíz y escribir `grant`. Incluye pruebas dirigidas por mensajes y vistas
  *golden* (`internal/tui/testdata/`).

### Cambiado

- **Índice de generación única + rollback transaccional** (`internal/store`,
  `internal/scanner`, `internal/scan`). Antes `entries` guardaba **una copia del
  árbol por escaneo**: cada pasada reescribía ~300k filas y borraba en cascada
  la anterior. Ahora `entries` es **una sola vista actual del disco** con clave
  `path`, y un escaneo escribe **una única transacción**: `Commit` publica y
  `Rollback` descarta (así `Ctrl+C` deja intacto el índice anterior, `SEC-COD-04`).
  El **incremental reutiliza sin reescribir**: un directorio con `mtime`
  sin cambios no se recorre ni se escribe, y `PruneDir` elimina solo lo que
  desapareció de los directorios que sí se recorrieron. Los índices se eliminan
  de forma **perezosa** en la primera escritura y se recrean una vez al hacer
  commit (un escaneo totalmente reutilizado no paga nada). La migración `00004`
  conserva la generación más reciente, así que **no hace falta reescanear** tras
  actualizar. Medido con `mcc scan --root /Applications` (296.738 entradas):
  **1ª pasada ~18,6 s → ~4,0 s (~4,6×)** y **repetición ~17,9 s → ~0,25 s
  (~70×)**. `--full` sigue forzando un recorrido completo.
- **Escaneo mucho más rápido** (`internal/store`, `internal/scanner`). Medido con
  `mcc scan --root /Applications` (296.738 entradas): **~18,6 s → ~5,7 s** en una
  primera pasada y **~17,9 s → ~7,8 s** en repeticiones (retención 1). Cambios:
  - escritura en **lotes multi-fila** (`INSERT` con 500 filas por sentencia) en
    lugar de una sentencia por fila, y `INSERT` normal en vez de `OR REPLACE`;
  - **una transacción por lote grande** (el scanner sube el lote por defecto de
    1.000 a 16.384 entradas), reduciendo el número de commits;
  - **índices de reporte diferidos**: se eliminan al iniciar el escaneo y se
    crean una sola vez al terminar; también durante la poda. Si el proceso muere
    a mitad, `Open` los recrea;
  - **la BD ya no se escribe bajo el mutex del walker**: una goroutine escritora
    dedicada consume los lotes, así el walk de los workers no se serializa;
  - **`Progress` se entrega desde un único goroutine** (antes lo invocaban todos
    los workers a la vez, lo que era una carrera de datos para sus consumidores);
  - `CompleteScan` unifica "finalizar + podar" para crear los índices una sola vez;
  - benchmarks nuevos (`BenchmarkScanWalk`, `BenchmarkInsertEntries`) para
    medir walk vs escritura.

### Eliminado

- **Borrado permanente**: se elimina por completo del código y la CLI
  (`cleaner.Permanent`/`ConfirmPermanent` y el flag `mcc clean --apply
  --permanent`). Todo se **mueve a la Papelera**; MCC nunca borra del disco. Si
  mover a la Papelera falla, el ítem se reporta como fallido y queda intacto.
  Endurece `SEC-DEL-04` (`SECURITY.md`).

### Corregido

- **El escaneo completo (`--full`, o cualquier escaneo que recorra directorios
  cambiados) fallaba y se reportaba como cancelación.** El escritor transaccional
  (`store.ScanTx`) usa un único `*sql.Tx`, y el escáner lo llamaba desde varios
  goroutines a la vez (`PruneDir`/`UnchangedDir` desde los workers, `InsertEntries`
  desde el escritor). Un `*sql.Tx` **no es seguro para uso concurrente**: SQLite
  devolvía `database table is locked`. Ahora `ScanTx` serializa sus métodos con un
  mutex. Además, un fallo de escritura ya **no se enmascara** como «cancelado por
  el usuario»: el escáner informa del error real y reserva el mensaje de
  cancelación para una cancelación de verdad (usa el contexto **padre** para
  detectar `Ctrl+C`/`--timeout`). También se comprueba el fallo tras el volcado
  final, que antes se tragaba en escaneos pequeños.
- **`Ctrl+C` ya no se queda sin efecto.** `signal.NotifyContext` desactivaba el
  comportamiento por defecto de `SIGINT` y sólo cancelaba un contexto; como el
  escaneo y el servidor MCP no lo observaban, el proceso quedaba «congelado» y
  era imposible de terminar con `Ctrl+C`. Ahora:
  - el **scanner** comprueba el contexto durante el recorrido (cola de
    directorios cancelable) y `mcc scan` se detiene al instante, revierte el
    índice parcial y sale con **130**;
  - `mcc mcp` observa el contexto y termina al primer `Ctrl+C` (antes se
    bloqueaba para siempre en la lectura de `stdin`);
  - un **segundo `Ctrl+C`** fuerza la salida como red de seguridad (excepto en
    la TUI, que gestiona su propia señal y la restauración de la terminal).
  Se añade el código de salida **`130`** (`128+SIGINT` = cancelado por el
  usuario) al contrato (`AGENTS.md` §4) y a la documentación.

### Notas / decisiones

- `clean --plan` **nunca** incluye las raíces de la lista blanca (p. ej.
  `~/Library/Caches`) por `SEC-DEL-05`; en su lugar ofrece sus **hijos
  directos** (las cachés reales por app) y omite las cachés de apps en
  ejecución. Los datos de app (`app_data`) requieren selección explícita con
  `--entry` (advertencia "puede contener datos del usuario").
- La confirmación de `clean --apply` exige la palabra exacta `yes` (no basta
  Enter); en la TUI la confirmación exige escribir la **cantidad exacta de
  ítems**. El consentimiento de Nivel 1/3 solo se concede de forma interactiva
  en un TTY (`mcc permissions grant <root>` o `g` en la pantalla Scan).
- En la revalidación de `apply`, el tamaño se compara **solo en archivos**: las
  entradas de directorio llevan el tamaño de subárbol, así que un directorio se
  revalida por `mtime` + `(dev, inode)`.
- Al **cancelar un escaneo** (TUI, `Esc`), el escaneo en curso se elimina en
  cascada, de modo que el escaneo completo anterior sigue siendo el vigente.
- Los `fixtures` se generan en `t.TempDir()`; los *golden files* viven en
  `internal/cli/testdata/` y `internal/tui/testdata/`.
- La selección en el árbol impide marcar a la vez un directorio y algo dentro de
  él: al seleccionar un directorio se deseleccionan sus descendientes y no se
  permite seleccionar un hijo mientras un ancestro esté marcado.
- Respecto al esquema del `README.md`, el índice añade `dev`/`inode` a `entries`
  (necesarios para deduplicar hardlinks y revalidar en `apply`), estado
  (`finished_at`, `full`, `partial`) a `scans`, la tabla `inaccessible` para
  resultados parciales (`SEC-ACC-05`) y `total_size` a `plans`.
- El MCP **no expone** `apply_plan`: aplicar ocurre fuera del MCP
  (`mcc clean --apply --plan-id ...`) y **solo mueve a la Papelera**. Se puede
  añadir más adelante con confirmación humana externa al agente (`SEC-MCP-02`).
- El proyecto no hace ninguna llamada de red; `depguard` lo hace cumplir
  (`SEC-COD-07`).


