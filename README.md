# Canvas: SQL/JSON Console para Neovim

## Idea central

Necesito una herramienta para Neovim que me permita trabajar con bases de datos desde un flujo backend real, sin depender de WebStorm/DataGrip. La herramienta no debe intentar reemplazar un IDE completo ni construir un explorador visual de base de datos. Su objetivo principal es ejecutar queries, ver resultados como JSON estructurado, consultar DDLs, guardar snapshots, comparar salidas y detectar queries peligrosos antes de ejecutarlos.

La idea base es: los queries devuelven filas, pero mi herramienta debe devolver documentos JSON inspeccionables.

## Problema

WebStorm/DataGrip son demasiado pesados para mi flujo actual. El IDE puede consumir recursos absurdos, bloquear mi máquina y hacerme depender de una herramienta cara que no siempre se adapta a cómo trabajo. Además, muchas funciones visuales de los clientes SQL tradicionales no me aportan valor: no necesito un árbol de tablas, no necesito navegar visualmente la base y no necesito una tabla gigante con scroll horizontal.

El problema real no es “ver datos en una tabla”; el problema real es entender estados de negocio en sistemas backend: órdenes, paquetes, metadata, payloads, configuraciones, respuestas de APIs, estados de fulfillment, cambios antes/después y errores derivados de datos inconsistentes.

## Usuario principal

El usuario soy yo como backend engineer trabajando con sistemas WMS, Fulfillment, órdenes, pagos, e-commerce, integraciones y datos complejos. Necesito inspeccionar rápido qué pasó antes y después de ejecutar un flujo, validar cambios en varias tablas, leer JSON embebido en columnas y conservar evidencia útil para debugging, issues o PRs.

## No objetivos

No necesito un explorador visual de tablas. No necesito diagramas de base de datos. No necesito autocompletado avanzado en el MVP. No necesito una copia de DataGrip dentro de Neovim. No necesito soportar muchos motores al inicio. No necesito una UI tipo spreadsheet.

La herramienta debe evitar convertirse en un cliente SQL genérico. Debe ser una consola de debugging estructurado.

## Objetivo del MVP

El MVP debe permitir ejecutar una selección SQL desde Neovim, recibir el resultado como JSON pretty, abrirlo en un buffer con `filetype=json`, guardar ese resultado como snapshot, aplicar JSONPath o un path similar sobre el resultado, comparar dos snapshots y pedir el DDL de una tabla concreta.

El primer objetivo práctico es poder inspeccionar una orden real desde Neovim sin abrir WebStorm.

## Arquitectura

La arquitectura ideal es dividir el proyecto en dos partes. El núcleo debe ser un CLI escrito en Go, encargado de conectarse a la base de datos, ejecutar queries, transformar filas en JSON, obtener DDLs, guardar snapshots, aplicar filtros JSONPath, generar diffs y analizar queries peligrosos.

Neovim debe ser solo la interfaz. El plugin en Lua debe tomar la selección actual o el buffer actual, llamar al CLI, recibir salida estructurada por stdout y pintar resultados en buffers, splits o floating windows. Lua no debe cargar con drivers de base de datos ni lógica compleja.

## Motor inicial

El primer motor soportado debería ser MySQL, porque es el caso más cercano al flujo actual y permite obtener DDL con `SHOW CREATE TABLE`. PostgreSQL puede venir después. El diseño debe permitir agregar más motores, pero no debe pagar esa complejidad desde el primer día.

## Formato de salida

La salida principal debe ser JSON, no tabla. Cada fila debe convertirse en un objeto. Las columnas que contengan JSON válido deben parsearse automáticamente para que no aparezcan como strings escapados, sino como objetos reales dentro del documento.

Un query como:

```sql
select id, status, metadata, created_at
from orders
where id = 123;
```

Debe verse como:

```json
[
  {
    "id": 123,
    "status": "pending",
    "metadata": {
      "source": "shopify",
      "fulfillment": {
        "status": "created"
      }
    },
    "created_at": "2026-07-08T17:20:31Z"
  }
]
```

## Snapshots

Un snapshot debe ser una salida JSON normalizada guardada en disco. Debe servir para comparar estados antes y después de una operación. La herramienta debe permitir nombrar snapshots con nombres humanos como `before_split_order`, `after_split_order`, `before_fulfillment_push` o `after_packing`.

Los snapshots deben guardarse por proyecto, probablemente dentro de una carpeta local como `.dbx/snapshots/`. Esto permitiría conservar evidencia de debugging y reutilizarla durante investigación de bugs.

## Diff

El diff debe operar sobre JSON estructurado, no sobre texto plano. Debe mostrar diferencias por path, por ejemplo:

```diff
rows[0].status
- "created"
+ "pending"

rows[0].metadata.fulfillment.status
- null
+ "created"

rows[0].updated_at
- "2026-07-08T17:01:20Z"
+ "2026-07-08T17:04:51Z"
```

Esto debe ayudar a entender cambios de estado sin comparar visualmente tablas enormes.

## JSONPath

La herramienta debe permitir aplicar paths sobre resultados actuales o snapshots. El objetivo es filtrar estructuras grandes sin tener que leer todo el JSON manualmente.

Debe soportar casos como consultar `metadata.fulfillment.status`, extraer valores específicos de arrays, encontrar campos dentro de payloads largos y reducir una salida grande a la parte relevante.

## DDL

La herramienta debe permitir obtener el DDL de una tabla específica sin navegar un explorador visual. El comando ideal sería algo como `:DbDDL orders`, o ejecutar la acción sobre la palabra bajo el cursor.

El DDL debe abrirse en un buffer separado, probablemente read-only, con syntax highlighting SQL.

## Seguridad

La herramienta debe avisar antes de ejecutar queries peligrosos. Desde el MVP debe detectar al menos `UPDATE` sin `WHERE`, `DELETE` sin `WHERE`, `DROP`, `TRUNCATE`, `ALTER`, `CREATE INDEX`, statements múltiples y escrituras sobre conexiones marcadas como producción.

La configuración debe permitir marcar conexiones como `dev`, `staging`, `prod` o `readonly`. En producción, las operaciones peligrosas deben bloquearse o exigir confirmación explícita. Idealmente, las conexiones productivas reales deben usar usuarios read-only para que la seguridad no dependa solo de la herramienta.

## Comandos CLI iniciales

El CLI debe empezar con comandos mínimos:

```bash
dbx query --conn local_wms
dbx ddl --conn local_wms --table orders
dbx snapshot --name before_split_order
dbx diff before_split_order after_split_order
dbx path --snapshot before_split_order 'metadata.fulfillment.status'
dbx danger
```

El comando más importante al inicio es `dbx query`, leyendo SQL por stdin y devolviendo JSON por stdout.

## Comandos Neovim iniciales

El plugin de Neovim debe empezar con pocos comandos:

```vim
:DbRun
:DbDDL
:DbSnapshot
:DbDiff
:DbPath
```

`DbRun` debe ejecutar la selección actual o el statement bajo el cursor. `DbDDL` debe obtener el DDL de la tabla bajo el cursor. `DbSnapshot` debe guardar el resultado actual. `DbDiff` debe comparar snapshots. `DbPath` debe filtrar el resultado actual usando un path.

## UI inicial

La UI no debe ser compleja. Los resultados deben abrirse como JSON pretty en un buffer normal. Los warnings peligrosos pueden mostrarse en un floating window. Los DDLs pueden abrirse en un split. Los snapshots y diffs pueden abrirse como buffers de texto.

No es necesario construir una interfaz visual avanzada en el MVP. La prioridad es que el flujo sea rápido, estable y útil.

## Orden recomendado de construcción

Primero se debe construir el CLI con conexión MySQL y ejecución de `SELECT`. Después convertir resultados a JSON correctamente. Luego agregar parseo automático de columnas JSON. Después implementar DDL con `SHOW CREATE TABLE`. Después agregar detección heurística de queries peligrosos. Luego crear el plugin mínimo de Neovim para ejecutar selección y mostrar resultado JSON. Después agregar snapshots. Luego JSONPath. Luego diff entre snapshots. Al final, mejorar UI, configuración, historial, soporte Postgres y parsers SQL más serios.

## Principio rector

No debo construir un DataGrip para Neovim. Debo construir una herramienta para inspeccionar, comparar y entender estados de backend desde Neovim.

La herramienta debe ser pequeña, rápida, segura y diseñada alrededor de mi flujo real.

## CLI: configuración y `dbx query` (MySQL)

El núcleo CLI ya soporta el primer slice práctico: ejecutar SQL de lectura/inspección contra MySQL y devolver JSON pretty por stdout.

### Ubicación del config

Formato YAML. Se usa el **primer archivo que exista** (sin merge):

1. `--config <path>` (flag)
2. variable de entorno `DBX_CONFIG`
3. `./.dbx/config.yaml` (proyecto; `.dbx/` está en `.gitignore`)
4. `$XDG_CONFIG_HOME/dbx/config.yaml` o, si no hay XDG, `~/.config/dbx/config.yaml`

### Ejemplo de conexión

```yaml
connections:
  local_wms:
    driver: mysql          # por defecto mysql; solo mysql en el MVP
    host: 127.0.0.1
    port: 3306             # opcional; default 3306
    user: root
    # password: "secret"   # inline (opcional si usas password_env)
    password_env: MYSQL_PASSWORD  # preferible: no commitear secretos
    database: wms
    env: dev               # opcional: dev | staging | prod | readonly
    # Alternativa power-user (credenciales embebidas en el DSN):
    # dsn: "user:pass@tcp(127.0.0.1:3306)/wms"
```

**`password_env` (resumen):** si `password_env` está definido, la variable de entorno **debe** existir y ser no vacía; se usa ese valor e **ignora** `password` inline. Si la env falta o está vacía → error (sin fallback silencioso a `password`). Si omites `password_env`, se usa `password` inline (puede estar vacío). Con `dsn` raw, los campos de password no se usan para auth.

### Uso

```bash
# SQL por stdin; JSON pretty solo en stdout si todo sale bien
echo 'select 1 as id, "{\"a\":1}" as metadata' | dbx query --conn local_wms

# Config explícito
echo 'show tables' | dbx query --conn local_wms --config /path/to/config.yaml

# Fallos: exit ≠ 0, mensaje en stderr (`error: …`), sin JSON parcial en stdout
echo 'delete from orders' | dbx query --conn local_wms
```

Flags:

| Flag | Requerido | Descripción |
|------|-----------|-------------|
| `--conn` | sí | Nombre de la conexión en el YAML |
| `--config` | no | Ruta al config (si no, orden de descubrimiento arriba) |

### Política de SQL (MVP)

Solo se permiten statements de **lectura/inspección**. La allowlist es la **barrera de escritura** (`QueryContext` **no** impide DML por sí solo):

- Permitidos: `SELECT`, `WITH` (solo CTE de lectura), `SHOW`, `DESCRIBE`/`DESC`, `EXPLAIN`
- Rechazados: `INSERT`/`UPDATE`/`DELETE`/`DROP`/… y CTE+DML (`WITH … DELETE/UPDATE/INSERT`)
- Un solo statement; un `;` final opcional. Multi-statement se rechaza.
- DSN final siempre fuerza `multiStatements=false` (también si el DSN raw lo pedía en `true`).

### Notas de salida JSON

- Siempre pretty-print (indent 2 espacios + newline final). No hay modo compacto en el MVP.
- Columnas con JSON **objeto o array** válido se anidan automáticamente; primitivos JSON (`true`, `123`) quedan como string.
- **DECIMAL de MySQL se serializa como string JSON** (p. ej. `"12.34"`), no como número, para no perder precisión. Puede diferir de ejemplos numéricos genéricos del README (`"id": 123` sigue siendo número cuando el driver devuelve entero).
- `time.Time` → RFC3339 en UTC (`…Z` cuando aplica).
- Resultado vacío → `[]`.

### Limitaciones conocidas (MVP)

- Detección multi-statement **naive**: un `;` dentro de literales/strings puede dar **falso positivo** y rechazar el query (fail-closed a propósito).
- La allowlist es heurística (no un parser SQL completo). Aún pueden pasar formas con side effects que empiezan por `SELECT` (p. ej. `SELECT … INTO OUTFILE`, `SELECT … FOR UPDATE`, `SELECT … INTO @var`).
- Result sets **sin límite de filas** en el MVP: queries enormes pueden consumir mucha memoria/tiempo.
- Tipos MySQL raros (BIT, GEOMETRY, etc.) se mapean de forma conservadora; binario no-UTF-8 → base64.
- Sin multi-statement, sin PostgreSQL, sin Neovim en este slice.

### Tests de integración opcionales

```bash
# Offline (default; no requiere MySQL):
go test ./...
go build -o /tmp/dbx ./cmd/dbx

# Live MySQL (opcional): exporta un DSN y corre el paquete query
export DBX_MYSQL_TEST_DSN='user:pass@tcp(127.0.0.1:3306)/dbname'
go test ./internal/query/ -count=1 -v -run Integration
```

Si `DBX_MYSQL_TEST_DSN` no está definido, el test de integración se **salta** (no falla el suite offline).
