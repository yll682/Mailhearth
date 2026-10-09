# Pruebas de integración con varios proveedores

[English](integration-testing.md) · [简体中文](integration-testing.zh-CN.md) · [繁體中文](integration-testing.zh-TW.md) · [日本語](integration-testing.ja.md) · **Español**

## Comprobaciones locales

Ejecuta desde la raíz del repositorio. Conserva caché y archivos intermedios en `data/`, ignorado por Git.

```powershell
$env:GOCACHE=Join-Path (Get-Location) 'data/go-build-cache'
$env:GOTMPDIR=Join-Path (Get-Location) 'data/integration/go-work'
New-Item -ItemType Directory -Force $env:GOCACHE,$env:GOTMPDIR | Out-Null
go test ./... -run '^$'
go test -count=1 ./... -run '^TestMultiProvider' -timeout 120s
go vet ./...
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
git diff --check
```

La compilación de todos los paquetes usa `-run '^$'`. Las pruebas seleccionadas usan
SQLite real, el HTTP server de la aplicación y algoritmos puros para pertenencia,
autorización, operaciones, rutas, importación/observación de reenvíos, estado de envío
y Sieve. Las pruebas web usan TypeScript parser para estabilidad de requestId, cobertura
de traducciones e interpolación. No comprueban entrega externa ni interacción del navegador.
La concurrencia y reenvíos pueden repetirse con `-count=20`; race requiere compilador C y cgo.

## Entornos reales y seguridad

`internal/integration/multiprovider` utiliza API y protocolos reales. Deshabilita
`MAILHEARTH_DEV_STACK`; la configuración ausente provoca un error explícito. Usa cuentas,
dominios y buzones de prueba sin mensajes importantes. Las pruebas de protocolos envían
correo real y modifican endpoint en una instalación local aislada. Lee las pruebas antes
de ejecutarlas; las comprobaciones actuales de requisitos y protocolos no restablecen
contraseñas externas existentes. La futura aceptación del ciclo de vida puede crear/eliminar
recursos, revocar credenciales y cambiar reglas; necesita recursos dedicados.

Guarda la configuración únicamente en `data/integration/multi-provider/`; el lector resuelve
y valida la pertenencia de la ruta. No guardes credenciales en Git, chat ni registros.
Cada ejecución conserva ahí su base de datos, clave maestra, datos y resultados aislados.
Protege los archivos y realiza su limpieza según corresponda.

## Configuración

El lector JSON rechaza campos desconocidos y valores JSON adicionales. Configura todos los entornos:

| Campo | Contenido necesario |
|---|---|
| `purelymail` | `apiKey`, `domain`, `imap`, `smtp`, `managesieve` |
| `migadu` | Lo anterior más `apiUsername`; ManageSieve deshabilitado explícitamente si no existe |
| Cada plantilla de protocolo | `enabled`; si está habilitada: `host`, `port`, `tlsMode` (`tls`/`starttls`), `caBundleId` opcional |
| `manual` | `primaryMailbox`, `secondaryMailbox`, `independentSmtp`, `privateCaMailbox`, `noSieveMailbox` |
| Cada buzón manual | `address`, `credentials` (`clientKey`, `secret`) y `endpoints` con los tres protocolos |
| Endpoint manual habilitado | `networkMode: override`, plantilla `network`, `authMode: password`, `username`, `credential: {clientKey}` |
| Endpoint manual deshabilitado | `networkMode: disabled` |
| `deliveryTimeoutSeconds` | Entero positivo; predeterminado 180 |

IMAP y SMTP de `independentSmtp` deben usar username y secret distintos. La CA privada debe
estar disponible en la instalación de prueba y tener su `caBundleId`. `MAILHEARTH_CA_BUNDLES_FILE`
apunta al JSON que asocia ID de CA positivos a rutas PEM. `noSieveMailbox`
deshabilita ManageSieve y conserva IMAP/SMTP utilizables. La autenticación de administración
del proveedor no proporciona contraseñas de protocolos del buzón.

```powershell
$env:MAILHEARTH_MULTIPROVIDER_TEST_CONFIG=Join-Path (Get-Location) 'data/integration/multi-provider/config.json'
go test -count=1 -v ./internal/integration/multiprovider -timeout 40m
```

## Cobertura y resultados

- `TestRealMultiProviderPrerequisites`: autenticación real de administración Purelymail/Migadu
  y credenciales manuales. Escribe `prerequisites.json` al completarse.
- `TestRealMultiProviderTransactions`: aislamiento, registro de la misma dirección, candidatos
  rechazados, conflictos revision, requestId/contenido y campos públicos seguros. Corresponde a
  partes de T01, T03, T08, T17–T19 y T40; escribe `transactions.json`.
- `TestRealMultiProviderProtocols`: credenciales independientes y entrega real (T04), lectura
  sin SMTP (T05), lectura/entrega sin ManageSieve (T06), revisión de endpoint y cierre de conexiones
  antiguas (T37). Escribe `protocols.json`.

Los informes conservan `acceptanceComplete=false`. La matriz T01–T40 y V01–V07 completa necesita
pruebas adicionales y ejecución real. La suite Purelymail de `internal/integration` todavía necesita
migración a las interfaces actuales. Una prueba local o importación satisfactoria no completa
aceptación real. Los modos de reenvío Migadu permanecen `unverified` hasta superar V03.

Si falla entrega, comprueba MX/SPF, credenciales, protocolos, confirmación de destinos, carpetas
incluido Junk y Message-ID. Un error de red no demuestra revocación ni eliminación. Conserva
operationId/submissionId y comprueba resultados desconocidos sin reenviar automáticamente.
Mantén las credenciales reales fuera del CI habitual; realiza aceptación dedicada tras cambios
de proveedor, SMTP/IMAP, migration o Sieve y antes de publicar una versión.
