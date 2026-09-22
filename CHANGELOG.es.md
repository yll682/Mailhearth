# Registro de cambios

[English](CHANGELOG.md) · [简体中文](CHANGELOG.zh-CN.md) · [繁體中文](CHANGELOG.zh-TW.md) · [日本語](CHANGELOG.ja.md) · **Español**

Todos los cambios importantes de Mailhearth se documentan en este archivo.

El formato sigue [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
y el proyecto usa [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Sin publicar]

### Añadido

- Traducciones de la interfaz al chino tradicional (Taiwán), japonés y
  español, seleccionables desde la pantalla de inicio de sesión y desde
  Ajustes.

## [0.1.0] - 2026-09-20

Primera versión pública: una plataforma de correo empresarial autoalojada
que convierte una cuenta de Purelymail en un producto de correo para
equipos.

### Añadido

- **Configuración inicial**: crea la organización, valida el token de la API
  de Purelymail e importa los dominios, buzones y reglas de enrutamiento
  existentes sin cambiar nada en la cuenta. La importación es de solo
  lectura y repetible.
- **Modelo de organización**: miembros con puesto, departamento, rol y
  estado (invitado, activo, deshabilitado, baja); roles integrados de
  propietario, administrador y miembro, además de roles personalizados
  creados a partir de permisos detallados; un registro de auditoría de las
  acciones administrativas y de la transferencia de propiedad.
- **Ciclo de vida del buzón**: buzones personales y compartidos, concesiones
  de acceso en los niveles `full`, `send` y `read`, una contraseña de
  aplicación por buzón creada y rotada por el servidor, y una baja en un
  solo paso que traspasa un buzón, lo convierte en compartido, lo conserva o
  lo bloquea, reenvía el correo nuevo, rota todas las credenciales y quita
  al miembro de los grupos.
- **Direccionamiento**: direcciones principales, alias, reenvíos, reglas de
  catch-all y prefijo, y direcciones de distribución de grupo que siguen la
  pertenencia al grupo.
- **Dominios**: gestión de dominios con el estado del DNS para MX, SPF, DKIM
  y DMARC, y registros DNS listos para copiar y pegar.
- **Correo web**: carpetas, paginación, búsqueda en el servidor, marcas,
  acciones masivas, archivos adjuntos, borradores con guardado automático,
  firmas y varias identidades de envío, notificaciones de escritorio
  mediante IMAP IDLE, atajos de teclado, temas claro y oscuro, inglés y
  chino simplificado.
- **Trabajo en equipo en buzones compartidos**: ver quién respondió, asignar
  un mensaje, marcarlo como resuelto y dejar notas internas. El estado de
  colaboración se indexa por `Message-ID`, por lo que sobrevive a los
  movimientos entre carpetas.
- **Reglas de correo**: condiciones y acciones estructuradas, además de la
  respuesta automática de vacaciones, compiladas a Sieve e instaladas
  mediante ManageSieve para que sigan funcionando mientras nadie ha iniciado
  sesión.
- **Seguridad**: el token de la API de Purelymail y todas las contraseñas de
  aplicación de los buzones se cifran en reposo con AES-256-GCM bajo una
  clave derivada de la clave maestra y nunca salen del servidor; hash de
  contraseñas con argon2id, protección CSRF, inicio de sesión con límite de
  intentos; el HTML de los mensajes se sanitiza en el servidor y se
  representa en un iframe en sandbox, sin scripts, bajo una CSP estricta con
  las imágenes remotas bloqueadas de forma predeterminada; los archivos
  adjuntos se sirven con `nosniff` y una disposición de descarga.
- **Despliegue**: un único binario estático de Go con el cliente web
  integrado, almacenamiento SQLite mediante el controlador
  `modernc.org/sqlite` escrito en Go puro, un `Dockerfile` y
  `docker-compose.yml`, y un flujo de trabajo de GitHub Actions que ejecuta
  la suite de pruebas, compila el binario y publica imágenes
  multiarquitectura en GHCR.
- **Desarrollo y verificación**: una implementación simulada en proceso de
  la API de Purelymail, IMAP y SMTP con datos de demostración (`make dev`),
  un script de capturas de pantalla que maneja la interfaz real y una suite
  de integración que se ejecuta contra una cuenta real de Purelymail.

[Unreleased]: https://github.com/yll682/Mailhearth/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/yll682/Mailhearth/releases/tag/v0.1.0
