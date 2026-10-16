---
name: mcc-skill
description: "Usa MCC (Mac Cache Cleaner) para analizar qué ocupa disco en macOS y proponer limpieza segura. Actívala cuando el usuario pregunte qué ocupa espacio, qué cachés se pueden borrar, qué apps pesan más, qué datos son huérfanos, qué archivos grandes hay, o quiera liberar espacio en este Mac. Cubre la CLI (mcc scan/apps/cache/orphans/large/clean/report/permissions/data), la TUI (mcc tui) y el servidor MCP para agentes (mcc mcp). MCC NUNCA borra: solo crea un plan y mueve a la Papelera tras confirmación humana."
---

# MCC — Mac Cache Cleaner

MCC indexa en disco local (SQLite) qué ocupan las apps, sus cachés, los datos de
apps desinstaladas (**huérfanos**) y los archivos/directorios pesados (> 1 GB), y
permite **planificar** limpieza. Todo es local: **sin red ni telemetría**.

> **Regla de oro: NADA se elimina.** La limpieza crea un *plan* inmutable y, al
> aplicarlo, **mueve a la Papelera** (`~/.Trash`). No existe borrado permanente.

## Si `mcc` no está instalado

Esta skill **no instala** el binario. Antes de usar cualquier comando, comprueba
que existe (`mcc --version`); si falta, pide al usuario instalarlo (acción humana,
con su confirmación):

```bash
brew tap crisacm/tap && brew install mcc   # Homebrew (recomendado)
```

Alternativas: descargar el binario de los
[releases](https://github.com/crisacm/mac-cache-cleaner/releases) o compilar con
`make build` desde el código. No instales nada sin permiso del usuario.

## Cuándo usar esta skill

- "¿Qué ocupa espacio en mi Mac?", "¿por qué se llenó el disco?"
- "¿Qué cachés puedo borrar sin riesgo?", "quiero liberar X GB"
- "¿Qué apps son las más pesadas?", "¿qué datos quedaron de apps que desinstalé?"
- "¿Qué archivos grandes tengo?" / quiere limpiar de forma **segura**.

## Cómo funciona (modelo mental)

1. **Escaneo** (`mcc scan`) construye el índice. Es **incremental** (reutiliza el
   `mtime` de directorios) y **solo metadatos**: tamaños (`st_blocks*512`),
   fechas y nombres; nunca el contenido (única excepción: `Info.plist` ≤ 1 MiB).
2. Cada ítem lleva su **clase de dato**, que determina su elegibilidad:
   | Clase | Qué es | ¿Se puede planificar? |
   |---|---|---|
   | `regenerable_cache` | cachés/logs que la app regenera | Sí (automático) |
   | `app_data` | datos de app (incl. **huérfanos**) | Solo con **selección explícita** y aviso "puede contener datos del usuario" |
   | `user_file` | datos de usuario (Nivel 1) | **Nunca** (solo reportar) |
   | `application` | apps instaladas | **Nunca** (MCC no desinstala) |
3. **Niveles de acceso** (compilados, no configurables):
   - **Nivel 0**: apps y cachés (por defecto).
   - **Nivel 1**: Documentos, Escritorio, Descargas… → requieren **consentimiento
     por carpeta** (interactivo, en TTY) y son **solo reporte**.
   - **Nivel 2**: Llaves, iCloud, Mail, Mensajes, `~/.ssh`… → **nunca se accede**.
   - **Nivel 3**: volúmenes externos/red → `--root` explícito + consentimiento.
4. **Limpieza = plan → aplicar.** El plan es inmutable (ID, hash, vencimiento);
   al aplicar se **revalida** cada ítem (ruta permitida, no symlink, mismo
   `(dev,inode)`, tamaño/`mtime` sin cambios) y se **omiten** los ítems inválidos
   y las cachés de apps **en ejecución**.

## Cómo usarlo

### CLI (rápido y scriptable)

```bash
mcc scan [--root PATH] [--full] [--explain]   # indexa (--full ignora el incremental)
mcc apps [--sort size] [--json]               # apps y su peso (app + datos)
mcc cache [--app ID] [--json]                 # cachés por ubicación
mcc orphans [--min-confidence high|medium|low]
mcc large [--min 1GB] [--path PATH]           # pesados
mcc report [--json|--md] [--redact]           # informe consolidado
mcc clean --plan                              # dry-run: crea el plan, no borra
mcc clean --apply [--plan-id ID]              # aplica (exige escribir "yes") -> Papelera
mcc permissions [show|grant|revoke <root>]    # política efectiva y consentimientos
mcc data show | purge                         # qué guarda y cómo borrarlo
```

Códigos de salida: `0` ok · `1` error · `2` uso · `3` parcial (rutas inaccesibles) · `4` bloqueado/consentimiento · `130` cancelado por el usuario (Ctrl+C).

`Ctrl+C` cancela de forma limpia (`mcc scan` revierte el índice parcial; `mcc mcp` termina). Un segundo `Ctrl+C` fuerza la salida.

### TUI (`mcc tui`)

`tab`/`shift+tab` para moverte. En Apps/Caches/Orphans/Large: `s` ordena, `/` filtra,
`→`/`l` despliega directorio (o entra), `←`/`h` pliega (o sube al padre), `f` muestra
u oculta archivos dentro de carpetas, `espacio` selecciona, `p` crea el plan, `c` +
**escribir la cantidad exacta** aplica. En Scan, `Esc` cancela (revierte el índice
parcial). `x` en Permissions revoca consentimiento.

### MCP (para agentes)

`mcc mcp` (stdio). Herramientas: `mcc_get_status`, `mcc_list_apps`, `mcc_list_caches`,
`mcc_list_orphans`, `mcc_list_large`, `mcc_list_children`, `mcc_get_entry`,
`mcc_explain`, `mcc_verify_orphan`, `mcc_get_report`, `mcc_create_plan`, `mcc_get_plan`.

**El MCP no escanea, no aplica y no borra.** Flujo: `get_status` → `list_*` →
`explain`/`verify_orphan` → `create_plan`; el humano aplica con
`mcc clean --apply --plan-id <id>`. Detalle y config de cliente: `INSTRUCTIONS.md`.

## Cómo decidir bien (lo importante)

- **Solo propone lo que tenga `plannable=true`.** Si `plannable=false`, lee
  `plannable_reason`:
  - `allowlisted_root` → es una **raíz** de la lista blanca (p. ej. `~/Library/Caches`):
    no se planifica la raíz; **baja a sus hijos** (`mcc_list_children` / `→`) y
    planifica esos (p. ej. `~/Library/Caches/Homebrew`).
  - `running` → la app está abierta; sus cachés se omiten. Cierra la app y repite.
  - `report_only` (`user_file`/`application`) → **solo informar**, nunca planificar.
  - `policy_denied` → Nivel 2/3 o fuera de la lista blanca; no se puede.
- **Huérfano ≠ basura.** Un "high" es un ID reverse-DNS sin app instalada; confírmalo
  con `mcc_verify_orphan` (`app_found`) y recuerda que son `app_data`: **pueden
  contener datos del usuario** → selección explícita y avisar al usuario.
- **Caché realmente borrable** = `regenerable_cache` + `plannable=true` + app no
  corriendo. Si dudas, `mcc_explain` explica el porqué.
- **Nunca prometas borrar datos de usuario.** MCC no toca Nivel 2, no borra Nivel 1
  y no desinstala apps.

## Recetas

- *"¿Qué está llenando el disco?"* → `mcc scan` → `mcc report` (o `mcc large`).
- *"Libera espacio sin riesgo"* → `mcc cache` → plan de las cachés `plannable` → aplicar.
- *"¿Quedó basura de apps que borré?"* → `mcc orphans --min-confidence high` →
  `mcc_verify_orphan`/`mcc_explain` → plan con selección explícita.
- *"No tengo permisos para escanear todo"* → resultado **parcial** (exit 3); ver
  `mcc permissions` y conceder Acceso total al disco a la terminal (no lo hace MCC).

## Límites y avisos

- `~/Library/Caches` u otras **raíces** no entran en un plan; se ofrecen sus hijos.
- Escanear `~/Library` completo puede requerir Acceso total al disco; sin él hay
  rutas inaccesibles (parcial).
- El plan **vence** (1 h por defecto) y cada ítem se revalida al aplicar.
- Datos locales en `~/Library/Application Support/mcc/` (`0700`/`0600`); ver y borrar
  con `mcc data show|purge`.

## Más información

- `README.md` — visión de usuario, instalación y uso.
- `INSTRUCTIONS.md` — uso mediante agentes y config del cliente MCP.
- `SECURITY.md` — reglas obligatorias (niveles, consentimiento, limpieza).
- `AGENTS.md` — arquitectura, convenciones y flujo de trabajo del proyecto.
