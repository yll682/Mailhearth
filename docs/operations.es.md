# Guía de operaciones y despliegue en producción

[English](operations.md) · [简体中文](operations.zh-CN.md) · [繁體中文](operations.zh-TW.md) · [日本語](operations.ja.md) · **Español**

## Planificación de capacidad y dimensionamiento

Mailhearth está diseñado con una huella operativa excepcionalmente ligera, lo que le permite funcionar con total fluidez incluso en los servidores VPS más económicos y compactos del mercado.

| Recurso | Consumo típico | Notas operativas |
|---|---|---|
| Binario ejecutable | ~25 MB estático | Cero dependencias de cgo; sin máquinas virtuales ni bibliotecas dinámicas externas |
| Memoria residente inactiva (RSS) | 25–40 MB | Protegida por el límite flexible de memoria de Go `GOMEMLIMIT=160MiB` para evitar picos |
| Memoria residente en carga (10 usuarios concurrentes) | 60–120 MB | La memoria se utiliza principalmente en búferes de descarga por lotes IMAP y análisis MIME |
| Uso de CPU | Prácticamente 0% en reposo | La derivación criptográfica de contraseñas con argon2id en el inicio de sesión es el único proceso intensivo |
| Crecimiento en disco | Crece solo con los metadatos | SQLite guarda la organización, permisos, historial y notas internas; los cuerpos de los correos residen en el proveedor |
| Paquete web frontend | ~100 KB comprimido con gzip | Incluye cliente webmail y consola de gestión completos, con nombres basados en hash para almacenamiento en caché prolongado |

### Ajuste del grupo de conexiones IMAP

Configura el grupo de conexiones simultáneas mediante la variable de entorno `MAILHEARTH_IMAP_MAX_CONNS` (valor predeterminado: 24). Fórmula orientativa recomendada: `usuarios concurrentes estimados × 2 + total de buzones que el equipo mantiene abiertos en pestañas`.

El sistema implementa cuotas estrictas de protección por niveles:
- **Límite global de conexiones**: 24 conexiones simultáneas por defecto;
- **Límite por conexión de proveedor**: Cada conexión de correo puede usar como máximo 8 conexiones IMAP simultáneas;
- **Límite por buzón**: Cada buzón puede reutilizar un máximo de 3 conexiones simultáneas;
- **Reserva de vigilantes IDLE**: Los escuchas IDLE en segundo plano para notificaciones push operan bajo una cuota reservada, garantizando que queden libres al menos 4 ranuras globales y 2 por conexión para las peticiones interactivas de los usuarios.

> **Nota**: Al ajustar las conexiones, asegúrate de comprobar los límites máximos de conexiones concurrentes por IP establecidos por tu proveedor de correo (como Purelymail o Migadu).

## Referencia de configuración

Todas las opciones son variables de entorno. Los valores mal formados o fuera de rango detienen el arranque con un error que indica la variable; los valores ajustados se registran como advertencias, y al arrancar se registra la configuración efectiva (sin secretos).

| Variable | Valor por defecto | Uso |
| --- | --- | --- |
| `MAILHEARTH_BASE_URL` | vacío | URL pública, usada en los enlaces de invitación. Debe ser `https://` salvo que `MAILHEARTH_SECURE_COOKIES=false`. |
| `MAILHEARTH_LISTEN` | `:8080` | Dirección de escucha HTTP. |
| `MAILHEARTH_DATA_DIR` | `./data` (`/data` en el contenedor) | Base de datos SQLite, clave maestra y archivos subidos. |
| `MAILHEARTH_MASTER_KEY` | generada en `<data>/master.key` | Clave de 32 bytes (base64 o hex) que cifra las credenciales guardadas. |
| `MAILHEARTH_SETUP_TOKEN` | generado al arrancar | Necesario para crear la organización en una instancia nueva. Si no se define, en cada arranque se genera un token aleatorio hasta que exista la organización y se escribe en el registro (busca `FIRST-TIME SETUP`, campo `setupToken`). Un token configurado nunca se registra. |
| `MAILHEARTH_LOG_LEVEL` | `info` | `debug`, `info`, `warn` o `error`. |
| `MAILHEARTH_TRUST_PROXY` | `false` | Leer `X-Forwarded-For` / `X-Forwarded-Host` de los proxies de confianza. |
| `MAILHEARTH_TRUSTED_PROXIES` | loopback y redes privadas | CIDR o direcciones de tus proxies inversos, separados por comas. Las cabeceras de reenvío de cualquier otro par se ignoran. |
| `MAILHEARTH_SECURE_COOKIES` | `true` (`false` con `MAILHEARTH_DEV_STACK`) | Atributo `Secure` de las cookies de sesión. Ponlo a `false` solo en una instancia de prueba con HTTP sin cifrar. |
| `MAILHEARTH_ALLOWED_MAIL_NETWORKS` | vacío | CIDR donde pueden estar los servidores de correo. Las conexiones a direcciones privadas, loopback y de enlace local se rechazan salvo que aparezcan aquí; configúralo si tu servidor IMAP/SMTP/ManageSieve está en una red privada. |
| `MAILHEARTH_CA_BUNDLES_FILE` | vacío | Archivo JSON que asocia identificadores de paquetes de CA con archivos PEM, por ejemplo `{"1": "/etc/mailhearth/ca.pem"}`. Los endpoints de protocolo usan ese identificador para confiar en una CA privada. |
| `MAILHEARTH_IMAP_MAX_CONNS` | `24` | Límite de conexiones IMAP. El pool admite de 4 a 24; otros valores se ajustan con una advertencia. |
| `MAILHEARTH_IMAP_IDLE_SECONDS` | `90` | Segundos tras los que se cierran las conexiones IMAP inactivas (10 a 3600). |
| `MAILHEARTH_MAX_UPLOAD_MB` | `25` | Tamaño máximo de un adjunto subido. |
| `MAILHEARTH_MAX_MESSAGE_MB` | `40` | Tamaño máximo del mensaje codificado. Los adjuntos aumentan aproximadamente un tercio al codificarse para el envío. |
| `MAILHEARTH_UPLOAD_QUOTA_MB` | `120` | Tamaño total de las subidas no enviadas de un miembro. Siempre se aplica; debe ser al menos `MAILHEARTH_MAX_UPLOAD_MB` (1 a 1048576). Auméntalo para relajar el límite. |
| `MAILHEARTH_SESSION_DAYS` | `14` | Caducidad deslizante de la sesión (1 a 365). |
| `MAILHEARTH_SESSION_MAX_DAYS` | `30` | Duración absoluta de la sesión (1 a 3650); no puede ser menor que `MAILHEARTH_SESSION_DAYS`. Usa un valor alto para levantar el límite en la práctica. |
| `MAILHEARTH_INVITE_DAYS` | `7` | Validez de los enlaces de invitación (1 a 90). |
| `MAILHEARTH_DEV_STACK` | `false` | Solo desarrollo: proveedor, IMAP y SMTP simulados en el propio proceso. |
| `GOMEMLIMIT` | `160MiB` si no se define | Límite del heap de Go. |

Los valores por defecto están pensados para un despliegue de producción nuevo: el ajuste más protector es el predeterminado, y relajarlo siempre requiere un ajuste explícito.

## Copias de seguridad y recuperación ante desastres

Todo el estado operativo de Mailhearth reside en el directorio de datos montado como volumen persistente (por defecto `/data`):

| Archivo / Directorio | Función y descripción | Requisitos de respaldo |
|---|---|---|
| `/data/mailhearth.db` | Base de datos SQLite principal (en modo de registro anticipado WAL) | Datos esenciales |
| `/data/mailhearth.db-wal` | Búfer de registro anticipado que contiene las transacciones recientes | Debe copiarse junto con `.db` |
| `/data/master.key` | Clave maestra de cifrado utilizada para proteger tokens de API y contraseñas | Secreto crítico; guardar en una ubicación externa segura |
| `/data/uploads/` | Adjuntos temporales preparados durante la redacción de mensajes | Temporales; se recrean automáticamente si es necesario |

### Copia de seguridad en caliente (sin detener el servicio)
Aprovecha la función de copia de seguridad en línea de SQLite para generar una instantánea coherente sin interrumpir el servicio:
```bash
docker compose exec mailhearth sqlite3 /data/mailhearth.db ".backup /data/backup_$(date +%Y%m%d_%H%M%S).db"
```

### Copia de seguridad en frío
Detén el contenedor y comprime directamente el volumen de datos:
```bash
docker compose stop
tar -czvf mailhearth-backup-$(date +%Y%m%d).tar.gz /data/mailhearth.db /data/mailhearth.db-wal /data/master.key
docker compose start
```

### Procedimiento de recuperación en un nuevo servidor
Restaurar Mailhearth en una máquina limpia se reduce a tres sencillos pasos:
1. Prepara el entorno Docker Compose en el nuevo servidor;
2. Copia la base de datos `mailhearth.db` respaldada (o renombra la copia en caliente a `mailhearth.db`) y el archivo `master.key` dentro del directorio de datos del host;
3. Ejecuta `docker compose up -d`. El sistema validará la clave maestra, ejecutará las comprobaciones necesarias y reanudará el servicio sin fricción.

## Actualizaciones y esquema de la base de datos

El esquema se define en `internal/db/migrations/0001_init.sql`. En el primer arranque con un directorio de datos vacío se aplica en una única transacción; si alguna sentencia falla, la transacción se revierte y la base de datos queda vacía. La versión del esquema se guarda en `PRAGMA user_version` de SQLite.

Mailhearth se niega a arrancar sobre una base de datos que no haya creado (por ejemplo, una que haya dejado una versión preliminar) y sobre una base de datos cuyo esquema sea más reciente que el binario en ejecución. En el primer caso, arranca con un directorio de datos vacío; en el segundo, ejecuta la versión correspondiente.

Los cambios de esquema posteriores se publicarán como archivos numerados adicionales, cada uno aplicado en su propia transacción.

1. **Antes de actualizar**: realiza una copia de seguridad (ver más arriba) y verifica la copia de `master.key`.
2. **Aplicar la actualización**:
   ```bash
   docker compose pull
   docker compose up -d
   ```
3. **Reversión**: los binarios antiguos no se ejecutan sobre un esquema más reciente. Para volver atrás, restaura la copia de seguridad previa a la actualización junto con la imagen anterior.

## Proxy inverso y configuración de TLS

En entornos de producción, Mailhearth debe situarse detrás de un proxy inverso que gestione el tráfico público y la terminación automática de certificados TLS (como Caddy o Nginx).

### Caddy (Recomendado)
Caddy aprovisiona y renueva automáticamente certificados TLS con una configuración sumamente limpia:
```caddyfile
mail.example.com {
    reverse_proxy 127.0.0.1:8080 {
        # Desactiva el búfer para que el flujo de eventos en tiempo real (SSE) fluya al instante
        flush_interval -1
    }
}
```

### Nginx
Si utilizas Nginx, debes desactivar el almacenamiento en búfer de las respuestas para las conexiones SSE y configurar tiempos de espera adecuados:
```nginx
server {
    listen 443 ssl http2;
    server_name mail.example.com;

    ssl_certificate /path/to/fullchain.pem;
    ssl_certificate_key /path/to/privkey.pem;

    # Permitir subir adjuntos de gran tamaño
    client_max_body_size 50m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # Desactivar búfer para conexiones push en tiempo real
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

Asegúrate además de definir `MAILHEARTH_TRUST_PROXY=true` en tu archivo `.env` para que los registros de auditoría y los límites de tasa usen la IP real del cliente. Si el proxy no se conecta desde loopback o una red privada, añade su dirección a `MAILHEARTH_TRUSTED_PROXIES`.

## Resolución de incidencias y diagnóstico

### 1. Error de conexión con el proveedor: `provider_auth_failed`
- **Causa**: El token de API de administración ha caducado, ha sido revocado o carece de los permisos requeridos.
- **Solución**: Accede a Consola de administración → Conexiones de correo, edita la conexión afectada y proporciona un nuevo token de gestión. Hasta que el nuevo token no se valide con éxito, la configuración anterior continuará funcionando sin cortes.

### 2. El buzón indica «Protocolos sin configurar»
- **Causa**: La importación de recursos solo transfiere metadatos remotos; las contraseñas nunca se importan de forma automática.
- **Solución**: Entra en los ajustes del buzón y configura los endpoints de IMAP, SMTP y ManageSieve con las credenciales correspondientes. El sistema ejecutará una prueba real de conexión antes de confirmar los cambios. Desactiva los protocolos que no vayas a utilizar.

### 3. Error de autenticación de protocolo: `mailbox_auth_failed`
- **Causa**: El servidor remoto ha rechazado el usuario, la contraseña principal o la contraseña de aplicación configurada.
- **Solución**: En modo manual (`entered`), actualiza las credenciales en la ventana de configuración de protocolos; en modo gestionado (`managed`), inicia una operación de rotación de credenciales desde la consola de administración.

### 4. No se pueden guardar las reglas de correo
- **Causa**: El endpoint ManageSieve no está configurado, la conexión TLS falla o el servidor carece de las extensiones Sieve necesarias.
- **Solución**:
  - Purelymail utiliza por defecto `mailserver.purelymail.com:4190` con STARTTLS;
  - Migadu desactiva ManageSieve por defecto; actívalo e introduce credenciales válidas si tu plan lo contempla;
  - Si ya existe un script activo en el servidor, el sistema pedirá confirmación expresa para asumir su control y evitar sobreescrituras accidentales;
  - Las respuestas automáticas de vacaciones exigen que el servidor admita la extensión `vacation`.

### 5. Una tarea asíncrona (Operation) queda en estado `unknown`
- **Causa**: Una llamada remota al proveedor agotó el tiempo de espera o se interrumpió la red sin una respuesta definitiva.
- **Solución**:
  - Examina el registro paso a paso de la operación; los bloqueos de recursos se mantienen para evitar inconsistencias;
  - Si la creación de credenciales se cortó sin devolver un ID remoto, accede al panel del proveedor para verificar y eliminar contraseñas de aplicación huérfanas;
  - A continuación, haz clic en «Enviar informe de limpieza» en Mailhearth. La operación se marcará como `external_reported` con `systemVerified=false`, cancelando la tarea pendiente mientras se conservan los recursos ya creados.

### 6. El estado del envío muestra `sent_copy_failed`
- **Causa**: El servidor SMTP aceptó el mensaje para su entrega, pero falló el almacenamiento de la copia en la carpeta Enviados (Sent) por un fallo puntual de red en IMAP.
- **Solución**: ¡No temas enviar correos duplicados! El sistema separa de manera estricta el envío SMTP del archivado de la copia. Haz clic en «Reintentar guardado de copia»; el sistema únicamente reintentará la operación IMAP sin volver a enviar el mensaje a los destinatarios.

### 7. No hay actualizaciones en vivo y es necesario recargar manualmente
- **Causa**: El proxy inverso está almacenando en búfer el flujo de eventos Server-Sent Events (SSE).
- **Solución**: Revisa la configuración del proxy inverso: asegúrate de contar con `flush_interval -1` en Caddy o de desactivar `proxy_buffering` en `/api/mail/` con Nginx.

### 8. Apertura inicial lenta en carpetas con un gran volumen de mensajes
- **Explicación**: Al abrir una carpeta, Mailhearth realiza una única llamada FETCH por rango de secuencia para recuperar las envolturas y estructuras MIME de 50 mensajes. En Purelymail, el índice de búsqueda del servidor se activa de forma automática en los buzones creados para mantener una respuesta ágil permanente.

## Registros y observabilidad

Mailhearth escribe registros de texto estructurado en `stderr`, fácilmente accesibles mediante las herramientas de contenedores:
```bash
docker compose logs -f --tail=100 mailhearth
```

- Nivel de registro predeterminado: `info`;
- Para diagnosticar detalles de bajo nivel en conexiones de red o reconexiones de escuchas IMAP IDLE, define `MAILHEARTH_LOG_LEVEL=debug` en tu `.env`;
- Cada respuesta de la API incluye la cabecera `X-Request-ID`, y las líneas de error del registro incluyen el mismo `requestId`. Los errores del servidor se registran como `error` y los demás errores del cliente como `info`;
- **Seguridad garantizada**: Todas las salidas de registro se desinfectan automáticamente; jamás se imprimen contraseñas en claro, tokens de API ni credenciales descifradas.

## Comandos de comprobación y verificación

```powershell
# Ejecutar pruebas base
go test ./... -run '^$'

# Pruebas de esquema de base de datos, modelo de dominio, API HTTP e instalación nueva
go test -count=1 ./internal/db ./internal/core ./internal/httpapi ./cmd/mailhearth

# Análisis estático de código
go vet ./...

# Pruebas automatizadas y verificación de empaquetado del frontend
npm --prefix web run test
npm --prefix web run build
```

Para ejecutar pruebas de integración reales frente a proveedores externos, desactiva `MAILHEARTH_DEV_STACK`, configura las credenciales en `data/integration/multi-provider/`, asigna `MAILHEARTH_MULTIPROVIDER_TEST_CONFIG` y ejecuta `go test -count=1 -v ./internal/integration/multiprovider`.
