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
| Disco | unos pocos MB | SQLite: modelo de organización y metadatos de colaboración; el correo permanece en Purelymail |
| Interfaz | 51 KB comprimido con gzip | un fragmento JS, un archivo CSS, en caché inmutable |

Ajusta `MAILHEARTH_IMAP_MAX_CONNS` (24 de forma predeterminada) a
aproximadamente `2 × usuarios simultáneos + número de buzones que la gente
mantiene abiertos`. Cada vigilante IDLE ocupa una conexión. Purelymail permite
muchas conexiones IMAP por usuario, pero no hay razón para mantener más de las
necesarias.

## Copias de seguridad

Todo el estado es el directorio de datos:

| Ruta | Contenido |
|---|---|
| `/data/mailhearth.db` | Base de datos SQLite (modo WAL) |
| `/data/mailhearth.db-wal` | Registro de escritura anticipada; cópialo junto con la base de datos |
| `/data/master.key` | Clave de cifrado de las credenciales almacenadas |
| `/data/uploads/` | Archivos adjuntos del redactor en espera de envío (transitorios) |

Haz la copia de seguridad con el contenedor detenido, o usa
`sqlite3 mailhearth.db ".backup out.db"` para obtener una copia en línea
coherente. Guarda `master.key` en una ubicación segura aparte. Para restaurar en
un host nuevo: coloca ambos archivos y arranca el binario.

## Actualizaciones

Las migraciones se ejecutan automáticamente al arrancar y son aditivas. Descarga
la imagen nueva y ejecuta `docker compose up -d`. No se admite volver a una
versión anterior que cruce una migración; restaura una copia de seguridad en su
lugar.

## Proxy inverso

Caddy:

```caddyfile
mail.example.com {
    reverse_proxy 127.0.0.1:8080 {
        flush_interval -1      # required for Server-Sent Events
    }
}
```

nginx: define `proxy_buffering off;` y `proxy_read_timeout 3600s;` en
`/api/mail/` para que los flujos SSE no se almacenen en búfer, y
`client_max_body_size 50m` para los archivos adjuntos.

## Resolución de problemas

**«Purelymail rechazó este token de API»** — el token se revocó o se escribió
mal. Sustitúyelo en Administración → Conexión.

**Un buzón muestra «no conectado»** — los buzones importados solo reciben una
contraseña de aplicación cuando alguien los vincula (incorporación) o cuando un
administrador pulsa *Conectar*. Si Purelymail lo rechaza (falla
`createAppPassword`), comprueba que el usuario siga existiendo en la cuenta y
ejecuta *Sincronizar ahora*.

**«el servidor de correo rechazó esta credencial del buzón»** — la contraseña de
aplicación se eliminó en el portal de Purelymail o la contraseña del usuario se
restableció fuera de Mailhearth. Usa *Rotar credencial* en el buzón.

**Las reglas no se pueden guardar** — ManageSieve en
`mailserver.purelymail.com:4190` debe ser accesible desde el host (STARTTLS).
Algunos proveedores de VPS bloquean los puertos de salida; pruébalo con
`openssl s_client -starttls sieve -connect
mailserver.purelymail.com:4190`.

**Sin actualizaciones en vivo** — un proxy está almacenando SSE en búfer;
consulta lo anterior. El cliente recurre a la actualización manual y aun así
consulta las carpetas cuando ocurren acciones.

**Primera página lenta en una carpeta enorme** — el listado es un único FETCH de
rango de secuencia de 50 envolturas más las estructuras del cuerpo. El índice de
búsqueda del lado del servidor de Purelymail (habilitado por usuario) hace que
las búsquedas sean rápidas; está activado en los buzones que crea Mailhearth.

## Registros

Registros de texto estructurado en stderr. `MAILHEARTH_LOG_LEVEL=debug` añade
líneas por solicitud y reconexiones del vigilante IMAP. Nada en los registros
contiene credenciales.

## Entorno de desarrollo

`MAILHEARTH_DEV_STACK=1` sustituye Purelymail, IMAP y SMTP por implementaciones
simuladas en el proceso. `-seed-demo` añade dos dominios, cinco usuarios, reglas
de enrutamiento y correo de ejemplo. El token de API simulado es `dev-token`;
los usuarios se autentican con `<name>-pass`. Las pruebas de Go usan las mismas
implementaciones simuladas, así que `go test ./...` recorre todas las rutas,
incluidos IMAP IDLE, el envío por SMTP y la sanitización de HTML.
