# Operaciones

[English](operations.md) · [简体中文](operations.zh-CN.md) · [繁體中文](operations.zh-TW.md) · [日本語](operations.ja.md) · **Español**

## Dimensionamiento

Mailhearth está pensado para el host más pequeño que se pueda comprar.

| Recurso | Valor típico | Notas |
|---|---|---|
| Binario | ~25 MB estático | sin cgo, sin dependencias en tiempo de ejecución |
| RSS inactivo | 25–40 MB | `GOMEMLIMIT` es 160 MiB de forma predeterminada |
| RSS ocupado (10 usuarios) | 60–120 MB | dominado por los búferes de descarga IMAP |
| CPU | insignificante | argon2id en el inicio de sesión es el paso más pesado |
| Disco | aumenta con los datos de la organización | SQLite guarda operaciones, envíos y colaboración; el correo permanece en su servicio IMAP |
| Interfaz | unos 100 KB comprimidos con gzip | JS y CSS compilados el 2026-10-09; nombres con hash de contenido |

Ajusta `MAILHEARTH_IMAP_MAX_CONNS` (24 de forma predeterminada) a aproximadamente `2 × usuarios simultáneos + número de buzones que la gente mantiene abiertos`. Cada vigilante IDLE ocupa una conexión. El máximo global predeterminado es 24; cada connection admite 8 y cada mailbox 3. IDLE conserva capacidad para solicitudes ordinarias. Respeta los límites reales del proveedor.

## Copias de seguridad

Todo el estado es el directorio de datos:

| Ruta | Contenido |
|---|---|
| `/data/mailhearth.db` | Base de datos SQLite (modo WAL) |
| `/data/mailhearth.db-wal` | Registro de escritura anticipada; cópialo junto con la base de datos |
| `/data/master.key` | Clave de cifrado de las credenciales almacenadas |
| `/data/uploads/` | Archivos adjuntos del redactor en espera de envío (transitorios) |

Haz la copia de seguridad con el contenedor detenido, o usa `sqlite3 mailhearth.db ".backup out.db"` para obtener una copia en línea coherente. Guarda `master.key` en una ubicación segura aparte. Para restaurar en un host nuevo: coloca ambos archivos y arranca el binario.

## Actualizaciones

Las migraciones se ejecutan al arrancar e incluyen conversiones de tablas y comprobaciones de referencias. Guarda una copia coherente de la base de datos y master key antes de actualizar. Descarga la imagen nueva y ejecuta `docker compose up -d`. No se admite volver a una versión anterior que cruce una migración; restaura una copia de seguridad en su lugar.

## Proxy inverso

Caddy:

```caddyfile
mail.example.com {
    reverse_proxy 127.0.0.1:8080 {
        flush_interval -1      # required for Server-Sent Events
    }
}
```

nginx: define `proxy_buffering off;` y `proxy_read_timeout 3600s;` en `/api/mail/` para que los flujos SSE no se almacenen en búfer, y `client_max_body_size 50m` para los archivos adjuntos.

## Resolución de problemas

**`provider_auth_failed`** — actualiza la autenticación administrativa de la conexión identificada en Administración → Conexiones. Un candidato rechazado conserva la configuración activa. La autenticación del correo se comprueba por separado.

**Protocolos sin configurar** — la importación registra los recursos elegidos. Configura IMAP, SMTP, ManageSieve y credenciales entered, o usa la capacidad managed de esa conexión. Los protocolos activos deben autenticarse antes de guardar la configuración. SMTP y ManageSieve pueden desactivarse independientemente.

**`mailbox_auth_failed`** — comprueba username y password del protocolo afectado. Actualiza entered en la configuración de protocolos y managed mediante rotación. Los fallos de red, rechazos temporales y respuestas ManageSieve sin clasificación mantienen el estado sin confirmar. Verifica la revocación mediante una conexión nueva.

**Las reglas no se pueden guardar** — comprueba endpoint, TLS y extensiones de ManageSieve del buzón. Purelymail usa `mailserver.purelymail.com:4190` con STARTTLS por defecto. La plantilla Migadu comienza desactivada; utiliza la configuración real de la cuenta. Tomar control de un active script requiere confirmación y su hash actual. La respuesta automática también requiere la extensión `vacation`.

**Operation `unknown`** — consulta los pasos y verifica los resultados remotos antes de reintentar explícitamente. Se mantienen los bloqueos de recursos. Si se pierde una respuesta de creación de credenciales sin guardar el ID remoto, resuelve las credenciales en el portal y presenta un informe administrativo de limpieza. El informe cancela la operación y conserva los recursos creados: `external_reported`, `systemVerified: false`.

**Submission `sent_copy_failed`** — SMTP ya aceptó el mensaje; reintenta solamente la copia Sent. Si SMTP o APPEND queda `unknown`, consulta Submission y su marcador único. Conserva el requestId original.

**Nuevos recursos durante la sincronización** — selecciona los recursos en el resultado de esa conexión. Se conservan credenciales, permisos e historial. Un fallo de lectura no demuestra que el recurso haya sido eliminado.

**Sin actualizaciones en vivo** — un proxy está almacenando SSE en búfer; consulta lo anterior. El cliente recurre a la actualización manual y aun así consulta las carpetas cuando ocurren acciones.

**Primera página lenta en una carpeta enorme** — el listado es un único FETCH de rango de secuencia de 50 envolturas más las estructuras del cuerpo. El índice de búsqueda del lado del servidor de Purelymail (habilitado por usuario) hace que las búsquedas sean rápidas; está activado en los buzones que crea Mailhearth.

## Registros

Registros de texto estructurado en stderr. `MAILHEARTH_LOG_LEVEL=debug` añade líneas por solicitud y reconexiones del vigilante IMAP. Nada en los registros contiene credenciales.

## Comprobaciones

```powershell
go test ./... -run '^$'
go test -count=1 ./internal/db ./internal/core ./internal/httpapi ./internal/mailproto/sieve ./internal/provider -run '^TestMultiProvider'
go vet ./...
npm --prefix web run test
npm --prefix web run build
```

Desactiva `MAILHEARTH_DEV_STACK` para las pruebas reales. Guarda la configuración de autenticación en `data/integration/multi-provider/` dentro del proyecto, define `MAILHEARTH_MULTIPROVIDER_TEST_CONFIG` y ejecuta `go test -count=1 -v ./internal/integration/multiprovider`. La ausencia de configuración produce un fallo explícito. Registra las comprobaciones locales y la aceptación real por separado; los casos reales no ejecutados permanecen incompletos.
