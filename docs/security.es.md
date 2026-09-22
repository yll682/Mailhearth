# Seguridad

[English](security.md) · [简体中文](security.zh-CN.md) · [繁體中文](security.zh-TW.md) · [日本語](security.ja.md) · **Español**

## Modelo de amenazas

Mailhearth se sitúa entre los navegadores del personal y Purelymail con un token
de API que puede crear, eliminar y restablecer todos los buzones de la cuenta.
Los activos, en orden de sensibilidad:

1. El token de API de Purelymail.
2. Las contraseñas de aplicación de los buzones que se guardan para el acceso
   IMAP/SMTP/Sieve.
3. El contenido del correo y el directorio de la organización.
4. Las sesiones de los miembros.

Los adversarios considerados son: un atacante en internet, un correo malicioso o
de phishing, un empleado que se marcha y un miembro que intenta leer un buzón
que no se le concedió. Un host comprometido queda fuera del alcance más allá de
limitar el radio de impacto (los secretos se cifran en reposo, así que una copia
de la base de datos por sí sola no sirve de nada).

## Controles

**Secretos en reposo.** `secrets.Box` sella los valores con AES-256-GCM usando
una clave derivada (HKDF-SHA256) de la clave maestra de la instalación. La clave
maestra proviene de `MAILHEARTH_MASTER_KEY` o de `<data>/master.key` (modo 0600,
generado en el primer arranque). Cada propósito usa una clave derivada distinta.
El token de API solo se muestra como una pista enmascarada después de guardarlo;
las contraseñas de aplicación nunca se muestran.

**Autenticación.** Las contraseñas de los miembros usan argon2id (19 MiB, t=2).
Las sesiones son tokens aleatorios de 256 bits que se guardan con hash (SHA-256);
las cookies son `HttpOnly`, `SameSite=Lax`, `Secure` cuando la URL base es HTTPS,
con caducidad deslizante de 30 días. El inicio de sesión y la aceptación de
invitaciones tienen un límite de peticiones por IP. Deshabilitar o dar de baja a
un miembro revoca todas sus sesiones de inmediato.

**Autorización.** Cada endpoint de administración comprueba un permiso del rol
del miembro; las acciones exclusivas del propietario (transferencia) requieren
`org.owner`. Los endpoints de correo resuelven el buzón mediante
`core.ResolveMailbox`, que exige la propiedad o una concesión de acceso explícita
y aplica el nivel de la concesión (`read` no puede marcar ni enviar; `send` no
puede eliminar ni mover). Los derechos administrativos nunca implican acceso al
correo.

**CSRF.** Las peticiones `/api` que cambian el estado deben llevar
`X-Requested-With: Mailhearth` (que los navegadores no pueden añadir entre
orígenes sin CORS) y, cuando hay una cabecera `Origin`, esta debe coincidir con
el host.

**Contenido de correo no confiable.**
- El HTML se sanea en el servidor con una lista de permitidos (bluemonday) más
  una pasada del DOM que elimina el CSS peligroso (`expression`, `url()`,
  `position:fixed`), reescribe las imágenes `cid:` a URL de partes autenticadas,
  bloquea las imágenes remotas salvo que se soliciten y fuerza
  `target=_blank rel=noopener noreferrer` en los enlaces.
- El documento saneado se sirve desde su propia URL con
  `Content-Security-Policy: default-src 'none'; img-src 'self' data:; style-src
  'unsafe-inline'; script-src 'none'; form-action 'none'` y se muestra en un
  `<iframe sandbox="allow-same-origin allow-popups …">` sin `allow-scripts`.
  `allow-same-origin` se mantiene solo para que el elemento padre pueda medir la
  altura; la CSP garantiza que no se ejecute ningún script dentro en cualquier
  caso.
- El iframe se autentica con un token de vista HMAC de corta duración vinculado
  al miembro, el buzón, la carpeta y el UID, por lo que no necesita cookies.
- Los archivos adjuntos se sirven con `Content-Disposition: attachment` a menos
  que el tipo sea un tipo seguro para mostrar en línea (imágenes de mapa de bits,
  PDF, audio/vídeo, text/plain); el HTML, el SVG y el XML nunca se representan en
  línea. `X-Content-Type-Options: nosniff` en todas partes.
- El HTML redactado y las firmas pasan por el mismo saneador antes de enviarse,
  de modo que una sesión de navegador comprometida no puede inyectar scripts en
  el correo.
- Los tamaños de los mensajes y los archivos adjuntos tienen un tope
  (`MAILHEARTH_MAX_UPLOAD_MB`, `MAILHEARTH_MAX_MESSAGE_MB`); las partes de texto
  de más de 2 MB se truncan para mostrarlas.

**Cabeceras de la aplicación.** `X-Frame-Options: DENY`, `Referrer-Policy:
no-referrer`, una CSP para la SPA (`script-src 'self'`), caché inmutable solo
para los recursos con hash, `no-store` para las respuestas de la API.

**Credenciales de Purelymail.** Una contraseña de aplicación por buzón. El
traspaso, la conversión a compartido, la suspensión y la baja rotan esa
contraseña y además restablecen la contraseña de Purelymail, de modo que los
teléfonos y clientes de escritorio configurados por la persona que se marcha
dejan de funcionar. La contraseña de aplicación antigua se revoca en origen.

**Sieve.** Las reglas se compilan a partir de un modelo estructurado con
validación de nombres de cabecera, tamaños, direcciones y marcas; los usuarios no
pueden enviar Sieve sin procesar.

**Auditoría.** Cada cambio administrativo se registra con el actor, el objetivo y
el detalle en `audit_log`.

## Recomendaciones de despliegue

- Termina el TLS en un proxy inverso y configura `MAILHEARTH_BASE_URL` con el
  origen HTTPS para que las cookies sean `Secure`. Configura
  `MAILHEARTH_TRUST_PROXY=true` solo cuando el proxy establezca
  `X-Forwarded-For`.
- Haz una copia de seguridad de `master.key` aparte de la base de datos;
  guárdala en tu gestor de contraseñas. Sin ella, las credenciales guardadas
  deben volver a crearse (el producto puede hacerlo: rota todos los buzones).
- Usa un token de API de Purelymail dedicado a Mailhearth para poder revocarlo de
  forma independiente.
- Activa la autenticación en dos pasos en la propia cuenta de Purelymail; el
  token de API la omite, y por eso es el activo más importante de la lista
  anterior.
