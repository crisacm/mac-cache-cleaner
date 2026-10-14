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

### Notas

- El proyecto no hace ninguna llamada de red; `depguard` lo hace cumplir
  (`SEC-COD-07`).
