# Guía de estándares de seguridad y mitigación de amenazas

[English](security.md) · [简体中文](security.zh-CN.md) · [繁體中文](security.zh-TW.md) · [日本語](security.ja.md) · **Español**

## Modelo de amenazas y activos protegidos

En el esquema de red, Mailhearth actúa como puente entre los navegadores de los colaboradores y los proveedores de correo ascendentes (Purelymail, Migadu o servidores propios). Como plataforma centralizada para la administración y la gestión diaria de correo, el sistema define y protege los siguientes activos esenciales, clasificados por sensibilidad:

1. **Credenciales de API de gestión por conexión**: Otorgan control administrativo total sobre dominios, cuentas de correo y reglas de enrutamiento;
2. **Credenciales de protocolos de buzón**: Contraseñas maestras y de aplicación utilizadas para las operaciones de IMAP, SMTP y ManageSieve;
3. **Datos de correo y directorio del equipo**: Historial de mensajes, borradores, notas internas de buzones compartidos y plantilla de colaboradores;
4. **Sesiones activas de miembros**: Estado en el navegador de los usuarios con sesión iniciada y tokens de autenticación temporales.

### Vectores de amenaza y perímetro de defensa
El diseño de seguridad de Mailhearth aplica defensa en profundidad frente a los principales vectores de ataque:
- **Atacantes externos desde Internet**: Intentos de fuerza bruta sobre contraseñas, ataques de falsificación de peticiones en sitios cruzados (CSRF) o manipulación indebida de parámetros de API;
- **Remitentes maliciosos o correos de phishing**: Inyección de JavaScript malicioso (XSS) en cuerpos HTML especialmente manipulados, capas superpuestas de phishing CSS o rastreo de lectura mediante imágenes remotas;
- **Empleados dados de baja o usuarios internos no autorizados**: Intentos de mantener accesos revocados o inspeccionar buzones personales o compartidos confidenciales sin autorización explícita;
- **Clientes externos no autorizados**: Intercepción o apropiación no permitida de contraseñas de aplicación generadas para clientes de correo de terceros.

> **Alcance del perímetro**: La vulneración física del servidor o la toma de control root del sistema operativo quedan fuera del alcance del software. Sin embargo, Mailhearth limita al mínimo el radio de impacto (Blast Radius) mediante el **cifrado completo en reposo**: incluso si un atacante obtiene una copia directa de la base de datos, todas las credenciales permanecen completamente ilegibles sin la clave `master.key` almacenada de forma aislada.

## Controles de seguridad principales

### 1. Cifrado fuerte de credenciales en reposo (Secrets at Rest)
- **Estándar criptográfico**: Implementado a través del paquete `internal/secrets` utilizando cifrado autenticado AES-256-GCM.
- **Jerarquía de derivación de claves**: La clave maestra del servidor (cargada desde `MAILHEARTH_MASTER_KEY` o leída desde `<data>/master.key` con permisos estrictos `0600`) se expande mediante HKDF-SHA256 en subclaves dedicadas y aisladas por contexto.
- **Cero fugas en texto plano**: Las consultas habituales de API jamás devuelven contraseñas ni tokens en texto plano, limitándose a mostrar valores enmascarados seguros.
- **Flujo de reclamo único de contraseñas (One-Time Claim Flow)**: Al generar contraseñas de aplicación para clientes externos, los administradores autorizados deben reclamarlas explícitamente a través de un canal seguro con caducidad; la respuesta incluye obligatoriamente `Cache-Control: no-store` y el secreto en claro se destruye permanentemente de la memoria del servidor de inmediato.

### 2. Autenticación y protección de sesiones (Authentication & Sessions)
- **Hash de contraseñas de última generación**: Las contraseñas de los miembros se protegen con el algoritmo argon2id recomendado por RFC 9106 (`m=19MiB, t=2`), ofreciendo máxima resistencia frente a ataques de fuerza bruta asistidos por GPU/ASIC.
- **Tokens de sesión de alta entropía**: Las sesiones se basan en tokens pseudoaleatorios criptográficamente seguros de 256 bits y se almacenan exclusivamente como hashes SHA-256 en la base de datos. Las cookies aplican `HttpOnly`, `SameSite=Lax` y `Secure` (bajo HTTPS) con ventana de caducidad deslizante de 30 días.
- **Límites de tasa contra ataques de fuerza bruta**: Se aplican restricciones de tasa multinivel por dirección IP y cuenta sobre los puntos de acceso de inicio de sesión y activación de invitaciones.
- **Invalidación inmediata en desvinculaciones**: En cuanto un usuario es deshabilitado (`disabled`) o marcado como desvinculado (`departed`), el sistema revoca y destruye todas sus sesiones activas en una única transacción atómica.

### 3. Modelo de autorización granular y aislamiento de buzones
- **Separación ortogonal de privilegios**: Las atribuciones de gestión (configuración de dominios, alta de personal) están estrictamente separadas del acceso a los correos. Ser administrador de la organización jamás concede acceso implícito a los buzones personales de otros miembros.
- **Límites de autorización estrictos**: Todo acceso a los mensajes se valida dinámicamente mediante `core.ResolveMailbox`, verificando la titularidad del buzón o el nivel concedido en buzones compartidos (`read` solo lectura, sin marcar ni enviar; `send` lectura y envío, sin eliminar ni mover; `full` administración completa).

### 4. Protección estricta contra ataques CSRF
- Todas las peticiones HTTP que alteran el estado (POST, PUT, PATCH, DELETE) requieren de forma obligatoria la cabecera personalizada `X-Requested-With: Mailhearth`. Las políticas de seguridad de los navegadores impiden que sitios externos puedan enviar esta cabecera sin aprobación previa de CORS.
- Cuando una petición incluye la cabecera `Origin`, el servidor valida minuciosamente que coincida de forma exacta con la dirección `MAILHEARTH_BASE_URL`.

### 5. Aislamiento estricto de contenido no confiable (Sandboxing)
Los correos electrónicos representan el canal más expuesto a contenidos no confiables. Mailhearth implementa una canalización de saneamiento exhaustiva:
- **Desinfección en el servidor mediante listas blancas**: El HTML pasa por una lista blanca rigurosa con `bluemonday` y un segundo filtrado del DOM que elimina estilos CSS peligrosos (`expression`, `url()`, `position: fixed`), reescribe recursos incrustados `cid:` a puntos seguros autenticados y fuerza `target="_blank" rel="noopener noreferrer"` en todos los enlaces.
- **Bloqueo predeterminado de imágenes remotas**: Las imágenes externas permanecen bloqueadas de forma predeterminada para neutralizar píxeles de rastreo y reconocimiento de direcciones IP, descargándose únicamente cuando el usuario lo autoriza.
- **Renderizado aislado en iframe sin scripts**: El HTML desinfectado se entrega desde una URL dedicada con cabeceras estrictas:
  ```http
  Content-Security-Policy: default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; script-src 'none'; form-action 'none'
  ```
  y se muestra dentro de un `<iframe sandbox="allow-same-origin allow-popups ...">` **sin incluir jamás `allow-scripts`**. En ausencia total de scripts y bajo una CSP tan restrictiva, el motor del navegador no puede ejecutar código bajo ninguna circunstancia.
- **Tokens de visualización HMAC sin cookies**: La carga del iframe se autentica mediante tokens HMAC efímeros vinculados al miembro, buzón, carpeta y UID del mensaje, sin requerir cookies ni exponerlas.
- **Disposición segura de archivos adjuntos**: Los adjuntos se entregan con `Content-Disposition: attachment` y `X-Content-Type-Options: nosniff`. Salvo imágenes rasterizadas, texto plano y PDF, se prohíbe de forma tajante la renderización en línea de archivos HTML, SVG o XML.
- **Desinfección simétrica en la redacción y firmas**: El contenido creado por el usuario en el editor y sus firmas pasan por el mismo proceso de desinfección antes de enviarse por SMTP, evitando que extensiones maliciosas del navegador inyecten código hacia los destinatarios.

### 6. Cabeceras de seguridad de nivel de aplicación
- Protección contra Clickjacking: `X-Frame-Options: DENY`;
- Privacidad de Referrer: `Referrer-Policy: no-referrer`;
- CSP de la aplicación web: Entorno restringido a `script-src 'self'`;
- Prevención de almacenamiento en caché en la API: Todos los endpoints `/api/` devuelven `Cache-Control: no-store`.

### 7. Seguridad de transporte y validación de certificados TLS
- Todas las conexiones habilitadas de IMAP, SMTP y ManageSieve validan estrictamente los certificados del servidor remoto y el nombre de host (SNI), utilizando el almacén de certificados raíz del sistema operativo o paquetes de CA privados.
- El sistema **no ofrece opciones** para ignorar errores de certificados ni omitir la validación de TLS.

### 8. Registro de auditoría inmutable (Audit Trail)
- Cualquier cambio administrativo (altas, modificaciones de roles, ajustes de conexiones, rotaciones de credenciales) queda registrado permanentemente en la tabla `audit_log`, guardando el ID del operador, IP de origen, fecha y hora y el desglose de los cambios.

## Recomendaciones de seguridad para producción

1. **Exigir HTTPS y configurar el proxy inverso**: Termina siempre TLS en tu proxy inverso (Caddy o Nginx) y define `MAILHEARTH_BASE_URL` con el prefijo `https://` para que la propiedad `Secure` de las cookies esté siempre activa. Habilita `MAILHEARTH_TRUST_PROXY=true` únicamente si el proxy inverso es de confianza y asigna correctamente las cabeceras.
2. **Aislar la clave maestra (`master.key`)**: No almacenes `/data/master.key` junto con las copias de seguridad de la base de datos en almacenes genéricos. Guarda `master.key` en un gestor de contraseñas corporativo (como 1Password o Bitwarden) o en un módulo de seguridad de hardware.
3. **Utilizar tokens de API dedicados para cada proveedor**: Genera credenciales de API exclusivas para Mailhearth en Purelymail o Migadu, evitando compartir credenciales para poder revocarlas de forma limpia e independiente.
4. **Activar autenticación de dos factores (2FA) en el proveedor**: Habilita siempre 2FA mediante hardware o TOTP en la cuenta principal de tu proveedor de correo para blindar la infraestructura base.
