# UTMStack Migration Tool — Guía del operador

Esta guía te lleva paso a paso por la migración de una instalación UTMStack V10 a V11.

> **Buscás la versión en inglés (la que ven los clientes)?** Está en [`USER_GUIDE.md`](USER_GUIDE.md).

---

## Antes de empezar

Necesitás:

- Una instalación de **UTMStack V10** funcionando
- Un servidor Linux (Ubuntu, Debian o similar)
- Acceso **`sudo` / root** en ese servidor
- Acceso a Internet (la herramienta descarga el agente de migración desde Google Cloud Storage)

También conviene tener claro:

- Si vas a hacer un upgrade **en el mismo servidor** (in-place) o si vas a **mover todo a un servidor nuevo** (dos servidores)
- La IP y las credenciales SSH del servidor V10 (solo si corrés la herramienta desde otra máquina)

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
Es a propósito: evita que los agentes queden huérfanos en V10 después de que
el backend pase a V11.

---

## Comandos

### `prepare-agents` — Pre-deploy del agente de migración

Antes de hacer el upgrade del backend, cada agente UTMStack tiene que recibir
el binario `migrate_agent`. Este comando se encarga de eso y guarda su progreso,
así podés cortarlo (Ctrl+C) y retomarlo más tarde sin perder lo hecho.

#### Cómo ejecutarlo

En el mismo servidor V10 (no hace falta archivo de config):

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

### `upgrade` — Upgrade in-place V10 → V11

Este comando hace el upgrade del backend en el mismo servidor. Es **destructivo**:
V10 se elimina y V11 se instala en su lugar. No hay rollback automático.

#### Cómo ejecutarlo

En el servidor V10 (recomendado, no hace falta config):

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
| `--skip-agent-check` | Skipea la verificación de `prepare-agents`. **Cuidado.** | `false` |

#### Qué hace

1. Te pide confirmación de la operación destructiva (tenés que tipear `Yes`)
2. Verifica que `agents-migration.db` exista (prueba de que `prepare-agents` corrió)
3. Abre el puerto de la base de datos
4. Exporta toda la configuración de V10 a un archivo de backup
5. Elimina la instalación de V10
6. Descarga e instala UTMStack V11
7. Reimporta los datos en V11
8. Cierra el puerto de la base de datos

#### Sobre `--skip-agent-check`

`upgrade` se niega a correr si no encuentra `agents-migration.db` en el
directorio actual. Es un seguro para que no te olvides de preparar los agentes.

Si tenés una razón legítima para skipearlo (entorno de test, no hay agentes,
agentes desplegados por otra vía), pasá `--skip-agent-check`. Cualquier agente
que no haya recibido el `migrate_agent` va a quedar en V10 y se va a desconectar
después del upgrade.

---

### `migrate` — Mover V10 a un servidor V11 nuevo

Usá este cuando querés mover de un servidor a otro distinto (por ejemplo,
hardware nuevo). El V10 source queda intacto hasta que la migración termine.

#### Cómo ejecutarlo

```bash
sudo ./utmstack_migration_tool migrate --config config.yaml
```

#### Flags

| Flag | Descripción | Default |
|------|-------------|---------|
| `--config`, `-c` | Path al archivo de configuración. **Obligatorio.** | (ninguno) |

El archivo de config tiene que tener `source` (V10) y `destination` (V11).
Mirá [Archivo de configuración](#archivo-de-configuración) más abajo.

#### Qué hace

1. Conecta al servidor V10 source por SSH
2. Exporta todos los datos de V10 a un archivo YAML
3. Conecta al servidor V11 destination por SSH
4. Importa los datos en V11
5. Sincroniza la clave de seguridad entre ambos servidores
6. Te pregunta si querés borrar el archivo temporal de datos

---

### `export` — Guardar datos de V10 a un archivo

Guarda los datos de V10 en un YAML que podés conservar. Va de la mano de `import`.

```bash
sudo ./utmstack_migration_tool export --config config.yaml
```

| Flag | Descripción | Default |
|------|-------------|---------|
| `--config`, `-c` | Archivo de configuración. **Obligatorio.** | (ninguno) |
| `--output`, `-o` | Nombre del archivo de salida. | `utmstack-export-YYYYMMDD-HHMMSS.yml` |

---

### `import` — Cargar un archivo guardado en V11

Lee un YAML producido por `export` y mete su contenido en un servidor V11.

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

- Para **`upgrade`** corriendo localmente en el V10: no hace falta archivo.
- Para **`upgrade`** / **`prepare-agents`** / **`export`** en remoto: solo `source`.
- Para **`migrate`** / **`import`**: hace falta `destination` (y `source` para `migrate`).

### Ejemplo mínimo para operaciones remotas

```yaml
source:
  host: "192.168.1.100"          # IP o hostname del servidor V10
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
```

> **Tip:** en general solo necesitás `host`, `ssh_user` y o bien `ssh_password`
> o bien `ssh_private_key`. El resto se auto-detecta.

---

## Workflows típicos

### Workflow 1 — Upgrade local in-place (el más simple)

Estás sentado en el servidor V10 y querés actualizarlo a V11 ahí mismo.

```bash
ssh utmstack@servidor-v10
sudo ./utmstack_migration_tool prepare-agents
# esperá a que se desplieguen todos los agentes (o Ctrl+C y aceptás skip)
sudo ./utmstack_migration_tool upgrade
```

### Workflow 2 — Upgrade remoto in-place

Manejás el servidor V10 desde otra máquina.

```bash
cat > config.yaml <<EOF
source:
  host: "192.168.1.100"
  ssh_user: "utmstack"
  ssh_password: "tu-ssh-pass"
EOF

sudo ./utmstack_migration_tool prepare-agents --config config.yaml
sudo ./utmstack_migration_tool upgrade --config config.yaml
```

### Workflow 3 — Mover a un servidor nuevo

Tenés un V11 limpio y querés llevarte los datos del V10 sin tocarlo.

```bash
cat > config.yaml <<EOF
source:
  host: "192.168.1.100"
  ssh_user: "utmstack"
  ssh_password: "v10-ssh-pass"

destination:
  host: "192.168.1.200"
  ssh_user: "utmstack"
  ssh_password: "v11-ssh-pass"
EOF

sudo ./utmstack_migration_tool prepare-agents --config config.yaml
sudo ./utmstack_migration_tool migrate --config config.yaml
```

### Workflow 4 — Exportar ahora, importar después

Cuando querés hacer un backup del V10 e importar los datos más adelante (o a varios V11).

```bash
# Export
sudo ./utmstack_migration_tool export --config config.yaml --output backup.yml

# (Movés backup.yml a donde necesites, lo revisás, etc.)

# Import del lado del V11
sudo ./utmstack_migration_tool import --config config.yaml --input backup.yml
```

---

## Troubleshooting

### "permission denied" leyendo `/root/utmstack.yml` o `/root/compose.yml`

Tenés que correr la herramienta con `sudo`. Los archivos de configuración viven
en `/root/` en UTMStack V10, y solo root los puede leer.

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

La herramienta no llega al servicio AgentManager del servidor V10. Verificá:

- Que el servidor V10 esté arriba y `docker service ls` muestre `utmstack_agentmanager` corriendo
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
constraint en V11) y volvé a correr `import`.

---

## Archivos que crea la herramienta

| Archivo | Lo crea | Para qué sirve |
|---------|---------|----------------|
| `agents-migration.db` | `prepare-agents` | Lleva el track de qué agentes ya recibieron el agente de migración. Conservalo hasta que termine `upgrade`. |
| `utmstack-upgrade-<timestamp>.yml` | `upgrade` | Backup temporal de los datos de V10. La herramienta lo reusa durante el upgrade. |
| `utmstack-migration-<timestamp>.yml` | `migrate` | Archivo de datos temporal. Te va a preguntar si lo borrás al final. |
| `utmstack-export-<timestamp>.yml` | `export` | Tu dump de los datos de V10. Conservalo el tiempo que necesites. |

---

## Para pedir ayuda

- Cualquier comando aceptá `--help` para ver todas sus opciones.
- Abrí un issue en el repo de GitHub si encontrás un bug real.
