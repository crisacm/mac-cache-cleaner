# MCC — Mac Cache Cleaner

Herramienta de línea de comandos escrita en Go para **indexar y consultar el espacio en disco** que ocupan las aplicaciones, sus cachés, los datos de aplicaciones huérfanas y los archivos o directorios pesados (> 1 GB).

Tiene una **TUI** interactiva y un **servidor MCP** para agentes de IA. Nada se elimina nunca: la limpieza **siempre mueve a la Papelera**, tras un plan revisado y confirmado por una persona.

## Características

- Índice local (SQLite) del uso de disco, con re-escaneo incremental.
- Listado de aplicaciones instaladas con el tamaño de la app y de sus datos asociados.
- Detección de **datos huérfanos** (restos de apps desinstaladas) con nivel de confianza.
- Búsqueda de **archivos y directorios pesados** con umbral configurable.
- Cachés de desarrollo (Gradle, Android SDK/AVD, Xcode DerivedData, npm, pnpm, Go, Homebrew, Docker).
- Salida legible o `--json` para scripts.
- Limpieza segura: plan -> aplicar, *dry-run* por defecto, **solo Papelera** y lista blanca de rutas.
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
mcc permissions [show|grant|revoke <root>]    # política efectiva y consentimientos
mcc data show | purge                         # qué se guarda, dónde, y cómo borrarlo
mcc tui                                       # interfaz interactiva
mcc mcp                                       # servidor MCP (stdio) para agentes
```

Los comandos de lectura aceptan `--json` y `--no-color`.

**Códigos de salida:** `0` éxito · `1` error general · `2` uso incorrecto · `3` resultado parcial (rutas inaccesibles) · `4` bloqueado por política / falta consentimiento.

### Interfaz interactiva (`mcc tui`)

Requiere una terminal. Reutiliza los mismos servicios que la CLI y **no decide accesos por su cuenta** (todo pasa por `internal/policy`).

- **Overview**: totales, última vez escaneada y aviso de resultado parcial.
- **Carga inicial**: la primera pantalla aparece al instante con un indicador "Cargando índice…" que muestra el paso actual y el directorio que se está leyendo en tiempo real; los datos se cargan en segundo plano y en paralelo.
- **Apps / Caches / Orphans / Large**: listas ordenables (`s`) y filtrables (`/`), con clase de dato y, en huérfanos, nivel de confianza. Caches, Orphans y Large son **árboles**: `→`/`l` despliega un directorio (o entra en él), `←`/`h` lo pliega (o vuelve al padre), y los hijos se cargan bajo demanda desde el índice. `f` alterna entre mostrar solo directorios o todos los elementos dentro de las carpetas desplegadas. No se puede seleccionar a la vez un directorio y algo dentro de él.
- **Scan**: muestra primero las raíces efectivas y su veredicto; escanea con progreso y se cancela con `Esc` (el índice parcial se revierte). Los niveles 1 y 3 requieren consentimiento ya registrado; `g` abre la confirmación que **nombra la raíz** y exige escribir `grant`.
- **Limpieza**: selecciona ítems (`espacio`) → `p` crea el plan (dry-run) → pantalla de revisión (cantidad, tamaño total, avisos "puede contener datos del usuario") → `c` y escribir la **cantidad exacta** para aplicar. Todo se mueve a la **Papelera**.
- **Permissions**: tabla de política efectiva y consentimientos registrados; `x` revoca el seleccionado.
- Los ítems `user_file` y `application` se muestran pero **no se pueden seleccionar** para un plan.
- Navegación: `tab`/`shift+tab`, `q` para salir. Los nombres de archivos/apps se sanitizan (ANSI y caracteres de control) al renderizar.

### Agentes y MCP

`mcc mcp` expone el índice a agentes por **stdio** (solo lectura + `create_plan`); **no existe** herramienta para aplicar, borrar, escanear, conceder consentimiento ni cambiar configuración. El agente propone un plan y **la persona lo aplica** fuera del MCP con `mcc clean --apply --plan-id <id>`, que **solo mueve a la Papelera**.

- Cómo usarlo y cómo conectar un cliente (genérico, Claude Desktop, opencode): [`INSTRUCTIONS.md`](./INSTRUCTIONS.md).
- Skill para agentes: [`skills/mcc-skill/SKILL.md`](./skills/mcc-skill/SKILL.md).
- Reglas de seguridad del servidor: [`SECURITY.md`](./SECURITY.md) §8.

## Seguridad y privacidad

Las reglas completas están en [`SECURITY.md`](./SECURITY.md). Resumen:

- **Solo metadatos**: MCC lee tamaños, fechas y nombres; nunca el contenido de tus archivos (única excepción: el `Info.plist` de las apps para obtener su identificador).
- **Acceso por niveles**:
  - *Nivel 0* (por defecto): apps y cachés (`~/Library/Caches`, `Application Support`, cachés de desarrollo…).
  - *Nivel 1* (datos del usuario: Documentos, Imágenes, Películas…): solo con tu consentimiento por carpeta y **solo para reportar**, nunca se incluyen en planes automáticos.
  - *Nivel 2* (sensibles: Fotos internas, Llaves, iCloud, Mail, Mensajes, `~/.ssh`…): **nunca se acceden**, ni con consentimiento.
  - *Nivel 3* (discos externos, red, Time Machine): solo con `--root` explícito y consentimiento; Time Machine nunca.
- **Consentimiento** por carpeta, solo interactivo, registrado y revocable. `--yes` no concede consentimiento.
- **Limpieza segura**: plan inmutable con vencimiento, revalidación de cada ítem antes de actuar, **siempre a la Papelera** (nunca se borra del disco), lista blanca de rutas, no se toca SIP ni `com.apple.*`, se omiten cachés de apps en ejecución. `mcc clean --apply` muestra la cantidad y el tamaño total y exige escribir `yes`. Si mover a la Papelera falla, el ítem se reporta y se deja intacto. Las raíces de la lista blanca (p. ej. `~/Library/Caches`) nunca entran en un plan; se ofrecen sus hijos directos.
- **Sin telemetría ni red**: nada sale de tu equipo.
- **Datos locales**: `~/Library/Application Support/mcc/` (permisos `0700`/`0600`). Con `mcc data show` ves qué hay y con `mcc data purge` lo borras.
- No se ejecuta como root ni se piden privilegios.

## Reportar vulnerabilidades

No abras issues públicos para problemas de seguridad. Consulta el procedimiento en [`SECURITY.md`](./SECURITY.md) §11.

## Licencia

Por definir.
