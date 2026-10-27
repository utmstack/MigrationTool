# UTMStack Migration Tool — Guía del operador

Esta guía te lleva paso a paso por la migración de una instalación UTMStack V11 a V12.

> **Buscás la versión en inglés (la que ven los clientes)?** Está en [`USER_GUIDE.md`](USER_GUIDE.md).

---

## Antes de empezar

Necesitás:

- Una instalación de **UTMStack V11** funcionando
- Un servidor Linux (Ubuntu, Debian o similar)
- Acceso **`sudo` / root** en ese servidor
- Acceso a Internet (la herramienta descarga el agente de migración desde Google Cloud Storage)
- Para migraciones servidor a servidor: una **instalación V12 ya corriendo** en el destino (mirá `migrate` más abajo)

También conviene tener claro:

- Si vas a hacer un upgrade **en el mismo servidor** (in-place) o si vas a **mover todo a un servidor nuevo** (dos servidores)
- La IP y las credenciales SSH del servidor V11 (solo si corrés la herramienta desde otra máquina)

---

## Instalación

1. Descargá el binario desde la página oficial de releases.
2. Hacelo ejecutable:

   ```bash
   chmod +x utmstack_migration_tool
   ```

3. Verificá que arranca:

   ```bash
   ./utmstack_migration_tool --help
   ```

Si ves la lista de comandos, está listo.

---

## Workflow recomendado

La mayoría de los casos solo necesita estos dos comandos, en este orden exacto:

```bash
# Paso 1: pre-deploy del agente de migración a TODOS los agentes UTMStack.
sudo ./utmstack_migration_tool prepare-agents

# Paso 2: upgrade del backend en sí.
sudo ./utmstack_migration_tool upgrade
```

**Primero `prepare-agents`, después `upgrade`.** El `upgrade` chequea que
`prepare-agents` haya corrido y se niega a arrancar si no encuentra evidencia.
Es a propósito: evita que los agentes queden huérfanos en V11 después de que
el backend pase a V12.

Si no tenés agentes, pasale `--skip-agent-check` a `upgrade` o `migrate`.

---

## Comandos

### `prepare-agents` — Pre-deploy del agente de migración

Antes de hacer el upgrade del backend, cada agente UTMStack tiene que recibir
el binario `migrate_agent`. Este comando se encarga de eso y guarda su progreso,
así podés cortarlo (Ctrl+C) y retomarlo más tarde sin perder lo hecho.

Los agentes usan `migrate_agent` para actualizarse solos del agente V11 al
agente V12 después del upgrade del backend, y para instalar el forwarder V12
cuando lo necesitan los puertos del forwarder que tenés habilitados.

#### Cómo ejecutarlo

En el mismo servidor V11 (no hace falta archivo de config):

```bash
sudo ./utmstack_migration_tool prepare-agents
```

Desde una máquina aparte, con archivo de config:

```bash
sudo ./utmstack_migration_tool prepare-agents --config config.yaml
```

#### Flags

| Flag | Descripción | Default |
|------|-------------|---------|
| `--config`, `-c` | Path al archivo de configuración. Obligatorio en remoto. | (ninguno) |
| `--db-path` | Path al archivo SQLite de estado. | `./agents-migration.db` |
| `--yes`, `-y` | No interactivo: no pregunta la confirmación del skip al hacer Ctrl+C. | `false` |

#### Qué vas a ver

El comando arranca un loop que:

- Consulta al AgentManager cada 60 segundos
- Detecta agentes nuevos que volvieron online y les hace deploy
- Reintenta los que fallaron con backoff (30 s, 1 m, 2 m, 4 m, 8 m, después se mantiene)
- Marca como `gone` los agentes que desaparecen del AgentManager por varios ciclos
- Imprime una línea de progreso por ciclo, por ejemplo:

  ```
  Progress: 47 deployed | 3 pending | 2 offline | 0 failed | 0 skipped | 0 gone | total 52
  ```

El loop termina de tres formas:

1. Todos los agentes llegan a un estado terminal (`deployed`, `skipped` o `gone`).
2. Apretás **Ctrl+C** y aceptás marcar los que faltan como `skipped`.
3. El AgentManager queda inalcanzable demasiado tiempo (el comando aborta con un error claro).

#### Cortar el loop a propósito

Si no querés esperar a que vuelvan los agentes offline, apretá **Ctrl+C**.
La herramienta:

- Corta el loop
- Te muestra cuántos agentes quedaron en pending / offline / failed
- Te pregunta si querés marcarlos como `skipped`

Si aceptás, esos agentes quedan registrados como **skipped**. Vas a tener que
instalarles el agente de migración a mano después del upgrade. Si decís que no,
el estado se preserva y podés volver a correr `prepare-agents` más adelante.

#### Reanudar

Volvé a correr el mismo comando desde el mismo directorio. El archivo SQLite
(`agents-migration.db`) recuerda qué agentes ya están listos — no se redeployan.

> **Importante:** corré el comando siempre desde el **mismo directorio**, así
> encuentra el mismo `agents-migration.db`.

---

### `upgrade` — Upgrade in-place V11 → V12

Este comando hace el upgrade del backend en el mismo servidor. Es **destructivo**:
V11 se elimina y V12 se instala en su lugar. No hay rollback automático.

#### Cómo ejecutarlo

En el servidor V11 (recomendado, no hace falta config):

```bash
sudo ./utmstack_migration_tool upgrade
```

Desde otra máquina:

```bash
sudo ./utmstack_migration_tool upgrade --config config.yaml
```

#### Flags

| Flag | Descripción | Default |
|------|-------------|---------|
| `--config`, `-c` | Path al archivo de configuración. Opcional para upgrade local. | (ninguno) |
| `--data-file` | Nombre custom para el archivo de backup temporal. | `utmstack-upgrade-YYYYMMDD-HHMMSS.yml` |
| `--with-data` | Streamiea además los logs y alertas de los **últimos 7 días** desde el volumen de OpenSearch preservado al ClickHouse de V12. Sin esto, esa data de búsqueda se pierde. | `false` |
| `--skip-agent-check` | Skipea la verificación de `prepare-agents`. **Cuidado.** | `false` |

#### Qué hace

1. Te pide confirmación de la operación destructiva (tenés que tipear `yes`)
2. Verifica que `agents-migration.db` exista (prueba de que `prepare-agents` corrió)
3. Exporta toda la configuración de V11 a un archivo de backup
4. Elimina la instalación de V11
5. Descarga e instala UTMStack V12
6. Reimporta los datos en V12
7. Streamiea los últimos 7 días de logs y alertas desde el volumen de OpenSearch preservado al ClickHouse nuevo (solo con `--with-data`)

Toda la corrida queda chequeada en `migration-state.json`: si se corta a mitad
(un step que falla, un reboot), volvés a correr el mismo comando y retoma desde
el último step completado, en vez de empezar de cero. Una corrida exitosa borra
el archivo.

#### Sobre `--with-data`

V11 guarda sus datos de búsqueda en OpenSearch; V12 usa ClickHouse, y el upgrade
elimina OpenSearch. Sin `--with-data`, todos los logs y alertas anteriores a la
migración se pierden. Con el flag, la herramienta preserva el volumen de
OpenSearch durante la eliminación de V11, y una vez instalado V12 streamea los
logs y alertas de los últimos 7 días al ClickHouse nuevo y verifica las
cantidades de filas en el destino antes de dar por hecho el step.

#### Sobre `--skip-agent-check`

`upgrade` se niega a correr si no encuentra `agents-migration.db` en el
directorio actual. Es un seguro para que no te olvides de preparar los agentes.

Si tenés una razón legítima para skipearlo (entorno de test, no hay agentes,
agentes desplegados por otra vía), pasá `--skip-agent-check`. Cualquier agente
que no haya recibido el `migrate_agent` va a quedar en V11 y se va a desconectar
después del upgrade.

---

### `migrate` — Mover V11 a un servidor V12 nuevo

Usá este cuando querés mover de un servidor a otro distinto (por ejemplo,
hardware nuevo). El V11 source queda intacto hasta que la migración termine.

> **Importante — instalá V12 en el destino primero.**
> Este comando **no** instala V12. El destino tiene que ser una instancia V12
> ya funcionando (stack Docker arriba, `utmstack_postgres` corriendo) antes de
> lanzar `migrate`. La herramienta solo lleva la data.

#### Cómo ejecutarlo

```bash
sudo ./utmstack_migration_tool migrate --config config.yaml
```

#### Flags

| Flag | Descripción | Default |
|------|-------------|---------|
| `--config`, `-c` | Path al archivo de configuración. **Obligatorio.** | (ninguno) |
| `--data-file` | Nombre custom para el archivo de datos. | `utmstack-migration-YYYYMMDD-HHMMSS.yml` |
| `--with-data` | Streamiea además los logs y alertas de los **últimos 7 días** desde el OpenSearch de V11 al ClickHouse de V12. | `false` |
| `--skip-agent-check` | Skipea la verificación de `prepare-agents`. **Cuidado.** | `false` |

El archivo de config tiene que tener `source` (V11) y `destination` (V12).
Mirá [Archivo de configuración](#archivo-de-configuración) más abajo.

#### Qué hace

1. Conecta al servidor V11 source por SSH
2. Exporta todos los datos de V11 a un archivo YAML
3. Conecta al servidor V12 destination por SSH
4. Importa los datos en V12
5. Sincroniza la clave de seguridad al destino (archivo de credenciales, env del compose y el pipeline del events-engine)
6. Streamiea los últimos 7 días de logs y alertas al ClickHouse de V12 (solo con `--with-data`)
7. Te pregunta si querés borrar el archivo temporal de datos

Como `upgrade`, la corrida queda chequeada en `migration-state.json` y retoma
desde el último step completado si se corta a mitad.

---

### `export` — Guardar datos de V11 a un archivo

Guarda los datos de V11 en un YAML que podés conservar. Va de la mano de `import`.

```bash
sudo ./utmstack_migration_tool export --config config.yaml
sudo ./utmstack_migration_tool export --config config.yaml --output mi-backup.yml
```

| Flag | Descripción | Default |
|------|-------------|---------|
| `--config`, `-c` | Archivo de configuración. **Obligatorio.** | (ninguno) |
| `--output`, `-o` | Nombre del archivo de salida. | `utmstack-export-YYYYMMDD-HHMMSS.yml` |

---

### `import` — Cargar un archivo guardado en V12

Lee un YAML producido por `export` y mete su contenido en un servidor V12.

```bash
sudo ./utmstack_migration_tool import --config config.yaml --input mi-backup.yml
```

| Flag | Descripción | Default |
|------|-------------|---------|
| `--config`, `-c` | Archivo de configuración. **Obligatorio.** | (ninguno) |
| `--input`, `-i` | Archivo de entrada. **Obligatorio.** | (ninguno) |

---

## Archivo de configuración

El archivo de config es YAML. La forma cambia según el comando:

- Para **`upgrade`** corriendo localmente en el V11: no hace falta archivo.
- Para **`upgrade`** / **`prepare-agents`** / **`export`** en remoto: solo `source`.
- Para **`migrate`** / **`import`**: hace falta `destination` (y `source` para `migrate`).

### Ejemplo mínimo para operaciones remotas

```yaml
source:
  host: "192.168.1.100"          # IP o hostname del servidor V11
  ssh_user: "utmstack"           # Usuario SSH
  ssh_password: "tu-ssh-pass"    # Password SSH
  # ssh_private_key: "/home/yo/.ssh/id_rsa"   # Alternativa a ssh_password
```

### Ejemplo mínimo para `migrate`

```yaml
source:
  host: "192.168.1.100"
  ssh_user: "utmstack"
  ssh_password: "ssh-pass-source"

destination:
  host: "192.168.1.200"
  ssh_user: "utmstack"
  ssh_password: "ssh-pass-destination"
  # integrations_config_dir: "/utmstack/events-engine-workdir/pipeline"   # opcional
```

### Todos los campos disponibles

```yaml
source:
  host: "192.168.1.100"      # Obligatorio para operaciones remotas
  port: 5432                 # Puerto PostgreSQL (default 5432)
  user: "postgres"           # Usuario PostgreSQL (default postgres)
  password: ""               # Auto-detectado desde compose.yml si está vacío
  sslmode: "disable"         # Modo SSL de PostgreSQL (default disable)
  ssh_user: "utmstack"       # Usuario SSH (obligatorio en remoto)
  ssh_password: ""           # Password SSH
  ssh_private_key: ""        # Path a un archivo de clave privada (alternativa a ssh_password)
  ssh_port: 22               # Puerto SSH (default 22)

destination:
  # Mismos campos que source. Lo usan migrate e import.
  integrations_config_dir: ""   # Dónde V12 lee los YAML de integraciones migrados (default: /utmstack/events-engine-workdir/pipeline)
```

> **Tip:** en general solo necesitás `host`, `ssh_user` y o bien `ssh_password`
> o bien `ssh_private_key`. El resto se auto-detecta.

---

## Workflows típicos

### Workflow 1 — Upgrade local in-place (el más simple)

Estás sentado en el servidor V11 y querés actualizarlo a V12 ahí mismo.

```bash
ssh utmstack@servidor-v11
sudo ./utmstack_migration_tool prepare-agents
# esperá a que se desplieguen todos los agentes (o Ctrl+C y aceptás skip)
sudo ./utmstack_migration_tool upgrade --with-data
```

### Workflow 2 — Upgrade remoto in-place

Manejás el servidor V11 desde otra máquina.

```bash
cat > config.yaml <<EOF
source:
  host: "192.168.1.100"
  ssh_user: "utmstack"
  ssh_password: "tu-ssh-pass"
EOF

sudo ./utmstack_migration_tool prepare-agents --config config.yaml
sudo ./utmstack_migration_tool upgrade --config config.yaml --with-data
```

### Workflow 3 — Mover a un servidor nuevo

Tenés un **V12** limpio y querés llevarte los datos del V11 sin tocarlo.

```bash
cat > config.yaml <<EOF
source:
  host: "192.168.1.100"
  ssh_user: "utmstack"
  ssh_password: "v11-ssh-pass"

destination:
  host: "192.168.1.200"
  ssh_user: "utmstack"
  ssh_password: "v12-ssh-pass"
EOF

sudo ./utmstack_migration_tool prepare-agents --config config.yaml
sudo ./utmstack_migration_tool migrate --config config.yaml --with-data
```

### Workflow 4 — Exportar ahora, importar después

Cuando querés hacer un backup del V11 e importar los datos más adelante (o a varios V12).

```bash
# Export
sudo ./utmstack_migration_tool export --config config.yaml --output backup.yml

# (Movés backup.yml a donde necesites, lo revisás, etc.)

# Import del lado del V12
sudo ./utmstack_migration_tool import --config config.yaml --input backup.yml
```

---

## Qué migra, qué no

**Migra:** usuarios y roles, configuración de aplicación, los valores de
integraciones que configuraste (escritos como YAML de V12), agentes y
collectors, tag rules e incidentes (con sus alertas vinculadas, historial y
notas), la clave de conexión de agentes, y la clave de encriptación interna.
Con `--with-data`, los últimos 7 días de logs y alertas se streamian al
ClickHouse de V12.

**No migra** (V12 los provee o los rediscovera): data de búsqueda más vieja
que 7 días, statistics, el catálogo de integraciones, dashboards y
visualizations, assets y resultados de scans, correlation rules y logstash
filters, e incident response / SOAR.

---

## Troubleshooting

### "permission denied" leyendo `/root/utmstack.yml` o `/root/compose.yml`

Tenés que correr la herramienta con `sudo`. Los archivos de configuración viven
en `/root/` en UTMStack V11, y solo root los puede leer.

```bash
sudo ./utmstack_migration_tool prepare-agents
```

### "State database 'agents-migration.db' not found" al hacer `upgrade`

Te olvidaste de correr `prepare-agents` antes de `upgrade`, o estás corriendo
`upgrade` desde otro directorio.

Opciones:

- Corré `prepare-agents` primero desde el mismo directorio, o
- Pasá `--skip-agent-check` si sabés lo que hacés.

### "AgentManager unreachable after 5 consecutive failures"

La herramienta no llega al servicio AgentManager del servidor V11. Verificá:

- Que el servidor V11 esté arriba y `docker service ls` muestre `utmstack_agentmanager` corriendo
- Que el puerto `9000` sea alcanzable desde donde corrés la herramienta
- Que la `internal_key` en `/root/utmstack.yml` no haya sido modificada a mano

### "compose.yml not found"

La herramienta busca `compose.yml` en estas ubicaciones, en orden:

- `~/compose.yml` (directorio home del usuario que corre la herramienta)
- `/root/compose.yml`
- `/home/utmstack/compose.yml`

Si tu `compose.yml` vive en otro lado, copialo o symlinkealo a alguno de esos
paths, o corré la herramienta desde un directorio donde sí lo encuentre.

### "failed to connect via SSH"

Verificá:

- Que las credenciales SSH del `config.yaml` estén bien
- Que SSH (puerto 22 por default) sea alcanzable desde tu máquina
- Que puedas conectarte a mano: `ssh utmstack@<host>`
- Si usás `ssh_private_key`, que el path sea correcto y la clave no esté encriptada

### El loop corrió horas pero varios agentes siguen `offline`

Es esperable si esos agentes están físicamente apagados, en una laptop cerrada,
o detrás de una red inalcanzable. Tenés dos opciones:

- Dejar `prepare-agents` corriendo y esperar a que vuelvan
- Apretar Ctrl+C, aceptar marcarlos como `skipped`, correr `upgrade`, e instalar
  el agente de migración a mano en esas máquinas más adelante

### El step de `import` falla en una tabla específica

La herramienta usa `ON CONFLICT DO UPDATE`, así que reejecutar el import es
seguro — no duplica datos. Arreglá el problema de fondo (por ejemplo, una
constraint en V12) y volvé a correr `import`.

### Después de `migrate`, el destino sigue mostrando credenciales de integraciones viejas

Los plugins del events-engine cachean la clave interna en memoria al arrancar.
La herramienta hace re-deploy del stack del destino al final del key sync
exactamente por eso; si ese step se skipió o falló, reiniciá el stack del
destino a mano.

---

## Archivos que crea la herramienta

| Archivo | Lo crea | Para qué sirve |
|---------|---------|----------------|
| `agents-migration.db` | `prepare-agents` | Lleva el track de qué agentes ya recibieron el agente de migración. Conservalo hasta que termine `upgrade`/`migrate`. |
| `utmstack-upgrade-<timestamp>.yml` | `upgrade` | Backup temporal de los datos de V11. La herramienta lo reusa durante el upgrade. |
| `utmstack-migration-<timestamp>.yml` | `migrate` | Archivo de datos temporal. Te va a preguntar si lo borrás al final. |
| `utmstack-export-<timestamp>.yml` | `export` | Tu dump de los datos de V11. Conservalo el tiempo que necesites. |
| `migration-state.json` | `upgrade` / `migrate` | Checkpoint de los steps completados. Se borra solo al terminar; borralo a mano si querés forzar un restart desde cero. |

---

## Para pedir ayuda

- Cualquier comando aceptá `--help` para ver todas sus opciones.
- Abrí un issue en el repo de GitHub si encontrás un bug real.
