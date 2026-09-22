# Arquitectura

[English](architecture.md) · [简体中文](architecture.zh-CN.md) · [繁體中文](architecture.zh-TW.md) · [日本語](architecture.ja.md) · **Español**

## Objetivos y no objetivos

Mailhearth envuelve la infraestructura de correo fiable y económica de Purelymail
en un producto que una organización pequeña puede desplegar, administrar y usar a
diario. De forma deliberada **no** ejecuta un servidor SMTP, no almacena la copia
principal del correo, no filtra el spam ni implementa DLP / eDiscovery / MDM.
Tampoco es una nueva apariencia del portal de Purelymail ni otro Roundcube: la
unidad de administración es una persona de una organización, no un "usuario" de
una cuenta.

Restricciones que dieron forma al diseño:

- **Hosts diminutos.** Los despliegues objetivo funcionan en el VPS más barato
  disponible. El servidor es un único binario estático de Go con SQLite, un grupo
  de conexiones IMAP acotado dentro del proceso y una interfaz de Preact de 51 KB
  (comprimida con gzip). Sin Redis, sin Postgres, sin Node en tiempo de ejecución,
  sin procesos en segundo plano más allá de unas pocas goroutines.
- **Purelymail es la fuente de verdad del correo.** Los mensajes nunca se copian en
  la base de datos de Mailhearth. Todo lo que muestra el cliente se obtiene por
  IMAP bajo demanda; la base de datos solo guarda el modelo de la organización y
  pequeños metadatos de colaboración indexados por `Message-ID`.
- **Sin secretos en el navegador.** El token de la API y las contraseñas de
  aplicación de los buzones viven cifrados en SQLite. El navegador habla solo con
  Mailhearth.

## Componentes

| Paquete | Responsabilidad |
|---|---|
| `cmd/mailhearth` | Punto de entrada: configuración, clave maestra, base de datos, grupo de IMAP, servidor HTTP |
| `internal/config` | Configuración por variables de entorno |
| `internal/db` | SQLite (modernc, sin cgo) y migraciones incrustadas |
| `internal/secrets` | Caja AES-256-GCM (HKDF a partir de la clave maestra), argon2id, tokens |
| `internal/purelymail` | Cliente de API con tipos; `fake/` es un Purelymail en memoria |
| `internal/model` | Tipos de organización compartidos por los servicios y la API |
| `internal/core` | Servicios: configuración inicial/importación, miembros, roles, dominios, buzones, direcciones, grupos, baja de personal, estado del equipo |
| `internal/mailproto/imappool` | Grupo de conexiones IMAP acotado y vigilantes IDLE |
| `internal/mailproto/mailops` | Carpetas, listado, renderizado, acciones, redacción, SMTP |
| `internal/mailproto/mimeutil` | Saneador de HTML, conversión texto/HTML, decodificación |
| `internal/mailproto/sieve` | Compilador del modelo de reglas a Sieve; cliente ManageSieve |
| `internal/httpapi` | API JSON, sesiones, CSRF, subidas, SSE, vista de mensajes en entorno aislado |
| `internal/web` | SPA incrustada con gzip y caché inmutable |
| `internal/devstack` | Purelymail, IMAP y SMTP falsos para desarrollo y pruebas |
| `web/` | Aplicación de una sola página con Preact + Vite (correo, administración, ajustes) |

## Modelo de organización

| Concepto | Significado | Respaldado por |
|---|---|---|
| **Organization** | El único inquilino de una instalación. | `organizations` |
| **Member** | Una persona real que inicia sesión en Mailhearth. Tiene un rol, un estado (invited/active/disabled/departed), un puesto y un departamento. | `members` |
| **Role** | Conjunto con nombre de permisos (`members.manage`, `shared.manage`, …). Integrados: owner, admin, member; se permiten roles personalizados. | `roles` |
| **Domain** | Un dominio de la cuenta de Purelymail, con el estado de su DNS. | `domains` ↔ dominio de Purelymail |
| **Mailbox** | Una cuenta de inicio de sesión que almacena correo. `personal` (propiedad de un miembro) o `shared` (propiedad de la organización, atendido por varios miembros). | `mailboxes` ↔ usuario de Purelymail |
| **Address** | Algo que recibe correo: la dirección propia del buzón (`primary`), un `alias` a un buzón, un `forward` a destinos arbitrarios, una dirección de distribución `group`, una regla `catchall` o `prefix`. | `addresses` ↔ regla de enrutamiento de Purelymail |
| **Identity** | Una dirección From + un nombre visible + una firma con los que un buzón puede enviar. | `identities` |
| **Group** | Un conjunto de miembros, opcionalmente con una dirección de distribución cuyos destinos siguen a la pertenencia. | `groups`, `group_members` |
| **Access grant** | Miembro → buzón con nivel `full`/`send`/`read`. | `mailbox_access` |

Un miembro puede ser propietario de varios buzones; un buzón puede tener varias
direcciones; una dirección puede llegar a varios miembros (mediante un reenvío o un
grupo). Cuando las personas cambian de rol o se marchan, los buzones y las
direcciones permanecen en la organización: la propiedad se reasigna y nunca se
elimina de forma implícita.

## Correspondencia con Purelymail

| Acción de Mailhearth | Llamadas a la API de Purelymail |
|---|---|
| Conectar | `checkAccountCredit` (valida el token) |
| Importar / sincronizar | `listDomains`, `listUser`, `listRoutingRules` — solo lectura, idempotentes |
| Crear un buzón | `createUser` (contraseña aleatoria, sin correo de bienvenida) + `createAppPassword` |
| Conectar un buzón importado | `createAppPassword` (nunca se necesita la contraseña existente) |
| Rotar la credencial | `createAppPassword` y después `deleteAppPassword` de la antigua |
| Restablecer la contraseña para clientes externos | `modifyUser{newPassword}` + rotación |
| Suspensión / bloqueo por salida | `modifyUser{newPassword}` + `deleteAppPassword` |
| Dirección de alias / reenvío / catch-all / prefijo / grupo | `createRoutingRule` / `deleteRoutingRule` |
| Reenvío en un buzón | regla de enrutamiento sobre la propia dirección del buzón (semántica de Purelymail: la regla gana a la entrega) |
| Añadir dominio / recomprobar el DNS / ajustes | `addDomain`, `updateDomainSettings`, `getOwnershipCode` |

Mailhearth guarda exactamente una contraseña de aplicación por buzón, llamada
"Mailhearth". Los miembros nunca la ven; el servidor la usa en su nombre para IMAP,
SMTP y ManageSieve, después de comprobar que el miembro es propietario del buzón o
tiene una autorización. Los permisos de administración no conceden acceso al
correo: leer un buzón compartido siempre requiere una autorización explícita.

## Ruta del correo

1. `httpapi` resuelve el buzón del miembro que ha iniciado sesión
   (`core.ResolveMailbox`) y obtiene una credencial.
2. `imappool.Get` devuelve una conexión del grupo (como máximo
   `MAILHEARTH_IMAP_MAX_CONNS` en total, 2 inactivas por credencial, recogidas tras
   90 s de inactividad).
3. `mailops` ejecuta los comandos IMAP: `LIST` con `LIST-STATUS` para las carpetas,
   `FETCH` de rangos de secuencia de envelope + flags + `BODYSTRUCTURE` para la
   paginación, `UID SEARCH` para las consultas, `BODY.PEEK[part]` para el
   renderizado, y `MOVE`/`UIDPLUS` cuando están disponibles, con alternativas.
4. Los cuerpos HTML pasan por `mimeutil.SanitizeHTML` (lista de permitidos de
   bluemonday, limpieza de CSS, resolución de `cid:`, bloqueo de imágenes remotas)
   y se sirven en un documento aparte con CSP `default-src 'none'`, mostrado en un
   iframe en entorno aislado.
5. El envío construye el RFC 5322 con go-message, lo entrega por SMTP con la misma
   credencial y después lo añade a Enviados y marca el original como
   respondido/reenviado.
6. Los vigilantes IDLE (uno por buzón + carpeta, compartido por todas las pestañas
   abiertas) envían los cambios a los navegadores mediante Server-Sent Events.

## Colaboración en buzones compartidos

El estado de colaboración está indexado por `mid:<message-id>`, de modo que
sobrevive a los movimientos entre carpetas. `message_state` guarda el responsable y
el estado abierto/resuelto; `mail_activity` es un registro de solo adición
(respondido, reenviado, asignado, nota …) que se escribe tanto por acciones
explícitas del equipo como automáticamente cuando un miembro envía desde el buzón
compartido. La lista de mensajes decora las filas con "respondido por", el
responsable y las insignias de estado.

## Reglas

Los miembros editan las reglas como condiciones y acciones estructuradas.
`sieve.Compile` las convierte (junto con la respuesta automática de vacaciones) en
un script de Sieve con las extensiones que anuncia Purelymail
(`fileinto imap4flags copy body vacation`). El script se sube como `mailhearth` por
ManageSieve (`mailserver.purelymail.com:4190`, STARTTLS) y se activa. La forma
estructurada se guarda en `mailboxes.settings_json`.

## Interfaz

Preact + `@preact/signals`, un enrutador de historial de 60 líneas y ningún
framework de UI. Rutas: `/mail/:mailbox/:folder/:uid`, `/admin/:section/:id`,
`/settings/:tab`, `/login`, `/invite/:token`, `/setup`. Las cadenas son claves en
inglés con un diccionario zh-CN. La disposición es un cliente de correo de tres
paneles por encima de 860 px y un cajón + un solo panel por debajo.
