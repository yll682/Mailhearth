# Arquitectura

[English](architecture.md) · [简体中文](architecture.zh-CN.md) · [繁體中文](architecture.zh-TW.md) · [日本語](architecture.ja.md) · **Español**

## Despliegue y componentes

Mailhearth funciona como un binario Go con archivos Preact incrustados y SQLite (modernc, sin cgo). Los servidores de correo conservan los cuerpos y proporcionan entrega y filtrado. SQLite guarda organización, credenciales cifradas, asociaciones, operaciones, solicitudes de envío y metadatos de colaboración. Node se usa durante la compilación web.

- `cmd/mailhearth`: configuración, clave maestra, base de datos, grupo IMAP, workers y HTTP server.
- `internal/config`, `internal/secrets`: configuración, AES-256-GCM/HKDF, argon2id y token.
- `internal/db`, `internal/model`: migration incrustadas, validación de asociaciones y modelos persistentes.
- `internal/provider`: interfaz común de administración y adaptadores Purelymail, Migadu y manual.
- `internal/purelymail`: transporte API utilizado por su adaptador.
- `internal/core`: conexiones, descubrimiento/importación, organización, ciclo de recursos, Operation y Submission.
- `internal/mailproto/imappool`: límites de conexiones, invalidación de versiones de endpoint y vigilancia IDLE.
- `internal/mailproto/mailops`, `mimeutil`, `sieve`: IMAP/SMTP, saneamiento MIME, compilación Sieve y ManageSieve.
- `internal/httpapi`, `internal/web`, `web/`: permisos, JSON/SSE, archivos incrustados e interfaz de correo/administración/ajustes.

## Organización y pertenencia de recursos

Cada instalación tiene una Organization. Member guarda rol, departamento y estado `invited`, `active`, `disabled` o `departed`. Role contiene permisos con nombre. La propiedad de un Mailbox personal y las concesiones explícitas de uno shared (`full`, `send`, `read`) controlan el acceso al correo; los permisos administrativos no lo conceden.

MailConnection pertenece a la organización y guarda proveedor, nombre, autenticación de administración, ámbito de dominios y tres plantillas de protocolo. Domain es un nombre lógico; DomainBinding lo asocia a una conexión, ajustes del proveedor y estado DNS. Las direcciones de Mailbox son únicas por conexión; la misma dirección en otra conexión permanece independiente.

Address representa `primary`, `alias`, `forward`, `group`, `catchall`, `prefix` y `external_rule` de administración externa. El reenvío del buzón tiene su propia fila en `mailbox_forwardings`; importarlo conserva la dirección primary. Los ajustes de Identity y la autorización SMTP son independientes. Un alias de recepción no autoriza su uso en From. Group calcula todos los buzones personal elegibles y verifica las restricciones de conexión/dominio antes de eliminar destinos duplicados.

ProviderResource asocia cada referencia remota a un objeto local y su propósito, guardando propiedad, estado remoto y observaciones estructuradas seguras. Las restricciones de referencias y revision protegen los límites de conexión/organización y el historial.

## Configuración de protocolos

Cada Mailbox tiene endpoint IMAP, SMTP y ManageSieve independientes. network mode es `inherit`, `override` o `disabled`; cada endpoint habilitado especifica username y Credential cifrada. Las credenciales son `managed` o introducidas explícitamente. Los candidatos deben autenticar todos los protocolos habilitados antes de confirmar la configuración en una transacción. Las versiones de endpoint, conexión, credencial y acceso invalidan conexiones anteriores.

Purelymail: IMAP `imap.purelymail.com:993` TLS, SMTP `smtp.purelymail.com:465` TLS, ManageSieve `mailserver.purelymail.com:4190` STARTTLS. Migadu: IMAP `imap.migadu.com:993` TLS, SMTP `smtp.migadu.com:465` TLS y ManageSieve deshabilitado. Las conexiones manuales empiezan con todos los protocolos deshabilitados. TLS verifica hostname con certificados del sistema o un conjunto CA privado configurado explícitamente.

## Descubrimiento, importación y operaciones

El descubrimiento lee todo el ámbito seleccionado y guarda una instantánea completa con caducidad y revision de conexión. La importación valida pertenencia, versión, caducidad y dependencias en una transacción local. Los buzones personal importados no tienen owner ni credenciales de acceso. La sincronización actualiza observaciones registradas y deja nuevos recursos pendientes de selección. Conserva owner, permisos, colaboración, firmas y credenciales entered.

El reenvío importado conserva origen, destinos seleccionados, referencias remotas y confirmaciones `active`, `pending_confirmation`, `blocked` o `unknown`. Destinos y modos observados se guardan separados de los ajustes deseados. Los modos no verificados permanecen `unverified`; la presencia en API y los informes administrativos no prueban la entrega. La escritura de reenvíos Migadu sigue requiriendo V03.

Operation guarda requestId, resumen del contenido, carga cifrada, ocupación de recursos y pasos individuales. Los duplicados devuelven la misma operación; los cambios de revision y pérdida de permisos impiden ejecutar ajustes antiguos. Las escrituras con resultado desconocido requieren comprobación, y los pasos confirmados no se repiten. Las acciones externas registran informes con `systemVerified=false`. Una respuesta de creación de credencial perdida sin ID remoto exige un informe explícito de limpieza. La suspensión revoca acceso local inmediatamente; el archivo conserva historial. La baja registra transferencia, grupos y revocación de credenciales por separado.

## Correo y envío

1. HTTP valida propiedad/concesiones actuales y resuelve el endpoint del protocolo.
2. IMAP limita por defecto a 24 globales, 8 por conexión y 3 por Mailbox, reservando 4 globales y 2 por conexión para solicitudes normales mientras funcionan los vigilantes.
3. `mailops` usa IMAP para carpetas, paginación, búsqueda y partes MIME. Las carpetas especiales siguen una asignación explícita, SPECIAL-USE único y nombre único.
4. `mimeutil` sanea HTML/CSS, resuelve `cid:` y bloquea imágenes remotas. Un documento independiente con CSP restrictiva se muestra en un iframe aislado sin scripts.
5. Submission guarda requestId, Message-ID estable, resumen y envelope cifrado; el cuerpo permanece en Drafts del servidor. SMTP accepted y copia Sent se registran por separado. El resultado desconocido no se reintenta automáticamente. El reintento de copia conserva SMTP; limpiar borradores requiere UID EXPUNGE.
6. La vigilancia IDLE compartida notifica por SSE. La colaboración usa `mid:<message-id>`; `message_state` guarda responsable/estado y `mail_activity` respuestas, reenvíos, asignaciones y notas, conservadas después de mover carpetas.

## Reglas e interfaz

Las reglas estructuradas se compilan a Sieve con las extensiones anunciadas. ManageSieve usa el endpoint del Mailbox y go-managesieve. La activación verifica hash actual, confirmación de sustitución, lectura del candidato independiente y resultado de activación antes de actualizar ajustes locales. Conserva scripts existentes. Reglas y respuestas automáticas dependen de los endpoint habilitados y extensiones necesarias.

Preact, `@preact/signals` y history router sirven `/mail`, `/admin`, `/settings`, `/login`, `/invite/:token`, `/setup`. Se usan tres paneles por encima de 860 px y un cajón con un panel por debajo. Las claves English tienen diccionarios zh-CN, zh-TW, japonés y español; TypeScript AST valida cobertura e interpolación. La compilación conserva archivos hash anteriores para clientes ya abiertos.

Las comprobaciones locales de base de datos, HTTP y compilación se describen en [pruebas de integración](integration-testing.es.md). La aceptación con proveedores reales sigue pendiente.
