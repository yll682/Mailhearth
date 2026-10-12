# Registro de cambios

[English](CHANGELOG.md) · [简体中文](CHANGELOG.zh-CN.md) · [繁體中文](CHANGELOG.zh-TW.md) · [日本語](CHANGELOG.ja.md) · **Español**

Todos los cambios notables introducidos en Mailhearth quedan documentados en este archivo.

El formato sigue las pautas de [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) y el proyecto se rige por [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).

## [No publicado]

### Novedades y mejoras

- **Ampliación de localización multilingüe**: Traducciones completas de la interfaz web y del panel de administración para chino tradicional (Taiwán), japonés y español, seleccionables en la pantalla de inicio de sesión y en Ajustes. La cobertura de diccionarios y variables se valida al 100% mediante análisis de AST en TypeScript.
- **Arquitectura multiproveedor**: Soporte ampliado para Migadu y servidores genéricos manuales IMAP/SMTP, permitiendo credenciales de gestión y plantillas de endpoints independientes por conexión.
- **Flujo de conciliación y limpieza de credenciales huérfanas**: Implementación de informes de limpieza fuera de línea y flujos de conciliación administrativa ante interrupciones de red o fallos en la provisión remota.
- **Reescritura integral de la documentación multilingüe**: Modernización y unificación estructural de las guías de arquitectura, operaciones, seguridad, pruebas de integración y presentación general en los cinco idiomas.

## [0.1.0] - 2026-09-20

Primera versión pública: plataforma de correo empresarial autoalojada y ligera que convierte cuentas de correo de proveedores ascendentes en un servicio completo y colaborativo para equipos.

### Funcionalidades añadidas

- **Asistente de configuración inicial**: Creación del administrador de la organización, validación de tokens de API y descubrimiento e importación no destructiva de dominios, buzones y reglas de reenvío existentes.
- **Modelo de organización centrado en las personas**: Gestión de colaboradores con cargos, departamentos, roles y estados de ciclo de vida (invitado, activo, deshabilitado, desvinculado); roles predefinidos (propietario, admin, miembro) y roles personalizados granulares; registro de auditoría inmutable y traspaso de propiedad.
- **Gestión integral de buzones**: Soporte para buzones personales y compartidos con niveles de acceso (total, lectura/envío, solo lectura); generación y rotación segura de contraseñas de aplicación; flujo guiado de baja (Offboarding) que reasigna buzones, archiva, redirige correo, cierra sesiones activas y desvincula grupos.
- **Topología flexible de direcciones**: Soporte nativo para direcciones principales, alias, reenvíos, captura global (catch-all), enrutamiento por prefijo y grupos de distribución dinámicos vinculados a la plantilla del equipo.
- **Gestión de dominios y salud de DNS**: Vinculación de dominios con monitoreo visual de registros MX, SPF, DKIM y DMARC, con valores listos para copiar y pegar.
- **Webmail moderno y adaptable**: Carpetas de sistema y personalizadas, paginación, búsqueda instantánea en el servidor, mensajes destacados, acciones masivas, visor y descarga de adjuntos, autoguardado de borradores, editor enriquecido, firmas, múltiples identidades, notificaciones de escritorio mediante IMAP IDLE, atajos de teclado y temas claro y oscuro.
- **Colaboración en buzones compartidos**: Contexto de respuesta en tiempo real: comprobación de quién respondió, asignación de responsables, marcas de resolución y notas privadas internas; el estado se vincula al `Message-ID`, manteniéndose intacto tras mover mensajes entre carpetas.
- **Reglas automáticas y respuestas de vacaciones**: Editor visual de condiciones y acciones compiladas a scripts Sieve compatibles con RFC desplegados por ManageSieve, activos incluso cuando no hay usuarios conectados; confirmación expresa antes de sustituir scripts activos.
- **Seguridad en profundidad**: Cifrado completo de credenciales y contraseñas con AES-256-GCM derivado de la clave maestra; hash de contraseñas con argon2id, protección CSRF y limitación de tasa en el inicio de sesión; saneamiento riguroso de HTML en el servidor dentro de un iframe aislado sin scripts bajo estricta CSP; bloqueo predeterminado de imágenes remotas y adjuntos protegidos con `nosniff`.
- **Despliegue monolítico simplificado**: Binario ejecutable Go único con los recursos web estáticos incrustados, almacenamiento SQLite nativo con `modernc.org/sqlite`, `Dockerfile`, `docker-compose.yml` y flujos de integración continua en GitHub Actions.
- **Herramientas de desarrollo y verificación**: Entorno mock en proceso con datos de demostración preconfigurados (`make dev`), scripts para captura automatizada de pantallas de interfaz y suite de pruebas de integración frente a proveedores reales.

[No publicado]: https://github.com/yll682/Mailhearth/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/yll682/Mailhearth/releases/tag/v0.1.0
