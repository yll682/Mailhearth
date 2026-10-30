# Guía de arquitectura del sistema

[English](architecture.md) · [简体中文](architecture.zh-CN.md) · [繁體中文](architecture.zh-TW.md) · [日本語](architecture.ja.md) · **Español**

## Diseño general y subsistemas

Mailhearth está diseñado con una arquitectura monolítica ágil, eficiente y autónoma. El backend está escrito en Go, la interfaz de usuario web está construida con el framework reactivo y ligero Preact, y el almacenamiento persistente se basa en SQLite integrado mediante el controlador Go puro `modernc.org/sqlite` (completamente libre de dependencias cgo). Todos los recursos compilados de la aplicación web se incrustan directamente en el binario final mediante el mecanismo `embed` de Go.

La división de responsabilidades del sistema es nítida:
- **Servidores de correo ascendentes**: Se centran en el transporte de protocolos de red, enrutamiento y entrega de correo, estructura física de directorios y almacenamiento de los cuerpos completos de los mensajes bajo la norma RFC 822.
- **Núcleo de Mailhearth**: Opera como capa de gestión y aplicación superior, manteniendo la estructura de la organización, cuentas y roles, almacenamiento cifrado de credenciales, mapeo de recursos, tareas asíncronas de control, registros de entrega y metadatos de colaboración en equipo.

Responsabilidades de los componentes principales:
- `cmd/mailhearth`: Punto de entrada del servicio; gestiona la carga de configuración, derivación y validación de la clave maestra, inicialización de la base de datos, inicialización del grupo de conexiones IMAP, orquestación de subprocesos en segundo plano y vinculación del servidor HTTP/SSE.
- `internal/config`, `internal/secrets`: Validación de variables de entorno; cifrado AES-256-GCM con derivación HKDF; cálculo seguro de hash de contraseñas con argon2id y ciclo de vida de tokens de sesión.
- `internal/db`, `internal/model`: Esquema integrado aplicado en una única transacción, restricciones de clave foránea y de ámbito de inquilino, entidades de dominio y modelos persistentes.
- `cmd/apitypes`, `internal/apitypes`: Genera `web/src/lib/api-types.ts` a partir de los tipos de respuesta de Go (reflexión según las reglas de `encoding/json`, con etiquetas `tstype` para uniones de cadenas). El cliente web importa únicamente estos tipos generados; `go test` y la CI fallan cuando el archivo confirmado está desactualizado.
- `internal/provider`: Interfaz abstracta unificada para proveedores de correo y adaptadores dedicados para Purelymail, Migadu y conexiones manuales estándar IMAP/SMTP.
- `internal/purelymail`: Cliente de transporte de API de bajo nivel y mapeo de errores estructurados para la API de gestión de Purelymail.
- `internal/core`: Lógica de negocio central que gestiona conexiones, descubrimiento e importación de recursos, permisos organizativos, ciclo de vida de buzones, operaciones persistentes asíncronas (Operation) y seguimiento de envíos (Submission).
- `internal/mailproto/imappool`: Grupo de conexiones IMAP acotado con límites estrictos de cuota, invalidación proactiva de conexiones activada por versiones (Revision) y monitorización IDLE en tiempo real.
- `internal/mailproto/mailops`, `mimeutil`, `sieve`: Operaciones de protocolo IMAP/SMTP; desinfección exhaustiva de HTML MIME y transmisión segura de adjuntos; compilación de reglas según estándares RFC y cliente ManageSieve.
- `internal/httpapi`, `internal/web`, `web/`: Middleware de autenticación basado en cookies de sesión, API RESTful JSON, flujo de eventos en tiempo real con Server-Sent Events (SSE); recursos web integrados y consola de administración y webmail.

## Modelo organizativo y propiedad de recursos

Cada instancia instalada de Mailhearth da servicio a una única organización independiente (`Organization`).

1. **Miembros y roles (Member & Role)**:
   - Los miembros del equipo conservan sus datos de perfil (nombre, departamento), roles asignados y un estado de ciclo de vida explícito (`invited` pendiente de activación, `active` activo, `disabled` deshabilitado, `departed` desvinculado).
   - El control de acceso utiliza permisos nombrados detallados (como `mailboxes.manage` o `domains.manage`) agrupados en roles flexibles.
   - **Invariante de seguridad**: Los permisos administrativos jamás otorgan acceso implícito al contenido de los buzones. La titularidad de un buzón personal (Personal Mailbox) y los permisos explícitos en buzones compartidos (Shared Mailbox: `full` acceso total, `send` lectura y envío, `read` solo lectura) constituyen los únicos límites de autorización para leer correos.

2. **Conexiones de correo y vinculación de dominios (MailConnection & DomainBinding)**:
   - Una conexión `MailConnection` pertenece a la organización y contiene el tipo de proveedor, nombre, tokens/claves de API de gestión, ámbito de dominios autorizados y plantillas predeterminadas para los tres protocolos.
   - Un `Domain` representa un dominio lógico; `DomainBinding` lo asocia a una conexión específica, supervisando los ajustes del proveedor y el estado de salud DNS en tiempo real (MX, SPF, DKIM, DMARC).
   - La unicidad de las direcciones de buzón se limita estrictamente a la conexión a la que pertenecen; la misma dirección configurada en conexiones distintas permanece totalmente aislada e independiente.

3. **Topología de direcciones e identidades de envío (Addresses & Identities)**:
   - Admite direcciones de tipo principal (`primary`), alias (`alias`), reenvío (`forward`), grupo (`group`), captura global (`catchall`), prefijo (`prefix`) y reglas administradas externamente (`external_rule`).
   - Las reglas de reenvío se guardan de forma aislada en `mailbox_forwardings`, conservando la referencia a su buzón principal al importarse.
   - La visualización de la identidad de remitente (Identity) y la autorización de envío por SMTP están estrictamente desacopladas: poseer un alias de recepción no concede automáticamente permiso para usarlo como remitente (`From:`) al enviar; debe autorizarse explícitamente.
   - Los grupos de distribución resuelven dinámicamente todos los buzones personales que cumplen los criterios, validando límites de dominio y conexión y eliminando destinos duplicados antes de la entrega.

4. **Mapeo de recursos remotos (ProviderResource)**:
   - Asocia cada referencia remota del proveedor (ID de buzón remoto, ID de alias) con un objeto local y un propósito comercial específico.
   - Las comprobaciones de revisión (`Revision`) y las restricciones de integridad relacional impiden fugas entre conexiones u organizaciones y protegen el historial de auditoría contra modificaciones indebidas.

## Configuración de protocolos y aislamiento de credenciales

Cada buzón dispone de tres endpoints de protocolo independientes: IMAP, SMTP y ManageSieve:
- **Modo de red (Network Mode)**: Cada endpoint se puede configurar como `inherit` (hereda la plantilla de la conexión), `override` (servidor y puerto personalizados) o `disabled` (protocolo desactivado).
- **Modo de credenciales (Credential Mode)**: Admite `managed` (credenciales generadas y administradas por la plataforma) o `entered` (contraseñas introducidas manualmente). Cada protocolo activo almacena sus contraseñas cifradas de forma individual.
- **Garantía atómica de activación**: Antes de confirmar cambios en la configuración de protocolos, el sistema ejecuta una prueba real de autenticación en segundo plano con todos los protocolos habilitados. La transacción local solo se confirma si todas las pruebas son satisfactorias.
- **Invalidación limpia de conexiones**: Cualquier modificación en los ajustes de un endpoint, credenciales o permisos cierra de inmediato las conexiones antiguas del grupo IMAP, forzando una reconexión segura en la siguiente solicitud.
- **Valores predeterminados por proveedor**:
  - Purelymail: IMAP (`imap.purelymail.com:993` TLS), SMTP (`smtp.purelymail.com:465` TLS) y ManageSieve (`mailserver.purelymail.com:4190` STARTTLS) activos por defecto.
  - Migadu: IMAP (`imap.migadu.com:993` TLS) y SMTP (`smtp.migadu.com:465` TLS) activos por defecto; ManageSieve desactivado.
  - Conexión manual: Todos los protocolos permanecen desactivados hasta que el administrador los configure explícitamente.
  - Validación de certificados TLS: Utiliza el almacén de certificados CA del sistema y permite montar colecciones de certificados CA privados, validando estrictamente el nombre de host (SNI).

## Descubrimiento, importación y operaciones persistentes

1. **Descubrimiento e importación selectiva**:
   - El descubrimiento (Discovery) examina los recursos remotos del proveedor y genera una instantánea completa con fecha de caducidad vinculada a la revisión actual de la conexión.
   - La importación se ejecuta dentro de una única transacción local, comprobando dependencias, plazos de caducidad y revisiones. Los buzones personales importados comienzan en estado no asignado (sin propietario ni credenciales de inicio de sesión locales) para su posterior asignación ordenada.
   - La sincronización actualiza de forma segura el estado observado de los recursos registrados, dejando las novedades en la lista de pendientes; conserva intactos los propietarios locales asignados, permisos de acceso, notas internas, firmas y credenciales introducidas.
   - La importación de reenvíos conserva buzones de origen, estados de confirmación (`active`, `pending_confirmation`, `blocked`, `unknown`) y referencias remotas. Los modos sin verificar permanecen en estado `unverified`, y las escrituras en Migadu están protegidas por comprobaciones estrictas de entrega.

2. **Motor de operaciones persistentes asíncronas (Operation Engine)**:
   - Las tareas administrativas prolongadas o con proveedores externos se ejecutan bajo la entidad `Operation`, registrando un `requestId` único, resumen de contenido, cargas cifradas, bloqueos exclusivos de recursos y progreso detallado paso a paso.
   - **Idempotencia estricta**: Si se vuelve a enviar una solicitud con el mismo `requestId`, el sistema devuelve de forma idempotente el estado de la operación existente sin duplicar el trabajo. Si cambia la versión de configuración o se revocan los permisos, se aborta la ejecución antigua.
   - **Aislamiento ante incertidumbre**: Si una llamada de escritura remota agota el tiempo de espera sin confirmación clara, la operación pasa a estado de verificación pendiente; los pasos completados nunca se repiten a ciegas.
   - **Informes de acciones externas**: Cuando un paso deba realizarse manualmente en la consola del proveedor, el sistema registra el informe del administrador bajo la marca `systemVerified=false`. Si la creación de credenciales se interrumpe sin un ID remoto devuelto, es obligatorio presentar un informe explícito de limpieza.
   - **Flujo guiado de salida (Offboarding)**: Registra paso a paso el traspaso o archivado de buzones, desvinculación de grupos, revocación de sesiones web activas e invalidación de credenciales externas.

## Procesamiento de correo y canalización segura de renderizado

1. **Resolución de permisos y asignación de conexiones**:
   - Al recibir una solicitud HTTP, el middleware resuelve la identidad, sesión y permisos de acceso sobre el buzón de destino para seleccionar el endpoint correspondiente.
   - El grupo de conexiones IMAP aplica límites en tres niveles: máximo global predeterminado de 24, 8 por conexión y 3 por buzón. Con los vigilantes IDLE en segundo plano activos, se reservan de forma garantizada 4 ranuras globales y 2 por conexión para las peticiones interactivas del usuario.

2. **Recuperación de mensajes e identificación de carpetas especiales**:
   - El paquete `mailops` ejecuta comandos IMAP eficientes para navegar por las carpetas, paginar listas, buscar en el servidor y descomponer la estructura MIME.
   - Las carpetas especiales se reconocen mediante una prioridad estricta: Mapeo explícito configurado > Atributo RFC 6154 `SPECIAL-USE` > Nombre único estándar o traducido.

3. **Aislamiento estricto en sandbox de contenido**:
   - El HTML del mensaje es desinfectado a fondo en el servidor mediante `mimeutil`, eliminando scripts maliciosos, hojas de estilo peligrosas y balizas de rastreo. Los adjuntos incrustados se reescriben a endpoints seguros de tipo `cid:`.
   - El correo se renderiza dentro de un `<iframe>` aislado en sandbox sin scripts, protegido por una Política de Seguridad de Contenido (CSP) muy estricta. Las imágenes remotas externas se bloquean por defecto para impedir rastreos de lectura y ubicación IP hasta que el usuario decida cargarlas.

4. **Canalización de envío con doble garantía (Submission Pipeline)**:
   - Las peticiones de envío se guardan en la base de datos con un `Message-ID` estable e inmutable, resumen de contenido y sobre cifrado. El cuerpo del mensaje se prepara temporalmente en la carpeta Drafts (Borradores) del servidor.
   - **Pasos totalmente desacoplados**: La aceptación por parte del servidor SMTP y el almacenamiento de la copia en la carpeta Enviados (Sent) se gestionan de forma independiente. Si el resultado de la entrega SMTP es desconocido por un corte de red, jamás se reintenta automáticamente para evitar envíos duplicados al cliente; si falla el guardado de la copia en Enviados, el reintento solo vuelve a guardar la copia sin reenviar por SMTP. La limpieza de borradores se realiza mediante un `UID EXPUNGE` preciso.

5. **Colaboración en tiempo real**:
   - En buzones compartidos, el vigilante IMAP IDLE detecta al instante los nuevos mensajes y los transmite al navegador mediante Server-Sent Events (SSE).
   - Los metadatos de colaboración se vinculan a la clave `mid:<message-id>`. La tabla `message_state` gestiona responsables y estado de resolución, mientras que `mail_activity` registra respuestas, reenvíos, asignaciones y notas internas privadas que permanecen intactas aunque el correo se mueva entre carpetas.

## Sistema de reglas de correo y arquitectura frontend

1. **Filtros Sieve y respuestas automáticas**:
   - Las reglas estructuradas se compilan dinámicamente a scripts estándar de Sieve basándose en las extensiones declaradas por el servidor de correo a través de ManageSieve.
   - Antes de aplicar las reglas, el sistema comprueba el hash del script activo en el servidor. Si se requiere sustituir un script existente, la interfaz pide confirmación expresa para evitar sobreescrituras accidentales.

2. **Aplicación web moderna de página única (SPA)**:
   - Desarrollada con Preact y `@preact/signals` para una gestión de estado reactiva y eficiente, navegada con un History Router ligero para las rutas `/mail`, `/admin`, `/settings`, `/login`, `/invite/:token` y `/setup`.
   - Diseño adaptable: Ofrece una interfaz de trabajo en tres paneles en pantallas de más de 860px (carpetas, lista de mensajes, visor de correo), adaptándose a una sola columna con menú lateral desplegable en dispositivos móviles y pantallas compactas.
   - **Localización multilingüe**: Con el inglés como idioma base, se incluyen diccionarios completos en español (es), chino simplificado (zh-CN), chino tradicional (zh-TW) y japonés (ja). Las pruebas automatizadas inspeccionan el árbol de sintaxis abstracta (AST) de TypeScript para asegurar la cobertura total de claves y la correspondencia exacta de variables en las plantillas.
