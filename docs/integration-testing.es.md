# Pruebas de integración contra una cuenta real de Purelymail

[English](integration-testing.md) · [简体中文](integration-testing.zh-CN.md) · [繁體中文](integration-testing.zh-TW.md) · [日本語](integration-testing.ja.md) · **Español**

Las pruebas unitarias se ejecutan contra una implementación simulada en memoria
de la API de Purelymail. Esa implementación simulada demuestra que Mailhearth es
coherente consigo mismo, pero no puede demostrar que Mailhearth coincida con el
servicio real: la implementación simulada se escribe a partir de la misma lectura
de la API que hace el cliente. Solo una cuenta real puede detectar un nombre de
campo incorrecto, una regla que Purelymail interpreta de forma distinta a la que
supusimos, una contraseña de aplicación que en realidad no se ha revocado, o
correo que nunca llega.

El conjunto de pruebas de `internal/integration` cierra esa brecha. Se omite de
forma predeterminada, así que `make test` y CI permanecen sin conexión y rápidos.

## Qué cubre

Cada prueba impulsa la propia capa de servicio de Mailhearth y después comprueba
el resultado contra la cuenta real en lugar de contra la base de datos de
Mailhearth.

| Prueba | Qué demuestra |
| --- | --- |
| `TestImportExistingAccount` | Conectarse a una cuenta que ya contiene usuarios y reglas los importa fielmente, los marca como importados, no emite ninguna contraseña de aplicación y no cambia nada en el proveedor. Una segunda sincronización no hace nada. |
| `TestMailboxLifecycle` | Crear un buzón crea un usuario real con una contraseña de aplicación que funciona; el inicio de sesión IMAP, el envío por SMTP, la entrega, el renderizado y la copia en Enviados funcionan; la rotación invalida la contraseña anterior en el proveedor; la eliminación borra el usuario. |
| `TestRoutingRules` | Un alias se convierte en una regla de enrutamiento que el proveedor respeta, el correo dirigido a él llega y eliminar la dirección retira la regla. |
| `TestGroupDistribution` | Una dirección de grupo llega a todos los miembros, y quitar a un miembro reescribe la regla en el proveedor. |
| `TestSharedMailboxAccess` | Los permisos concedidos deciden quién puede abrir un buzón compartido. Los derechos de administrador por sí solos nunca conceden acceso al correo. Revocar cierra la puerta. |
| `TestSieveRules` | El script de Sieve compilado por Mailhearth es aceptado por el proveedor, se convierte en el script activo y de hecho archiva el correo en la carpeta de destino en lugar de en la bandeja de entrada. |
| `TestOffboarding` | Una persona que se va pierde todas las vías de acceso, incluida la credencial que guardaba su cliente de correo de escritorio, mientras que su buzón y su historial se transfieren a la persona sucesora. |
| `TestSuspendAndReactivate` | La suspensión deja fuera a todo el mundo pero sigue aceptando correo; la reactivación restaura el acceso y el correo que llegó mientras tanto está ahí. |
| `TestPasswordResetForExternalClients` | La contraseña entregada para Thunderbird autentica de verdad, y la propia contraseña de aplicación de Mailhearth sobrevive al restablecimiento y sigue siendo distinta de ella. |
| `TestExternalForwarding` | Un reenvío a una dirección fuera de la cuenta se convierte en la regla que esperamos. |
| `TestTokenRejection` | Un token de API incorrecto se rechaza y no daña la conexión funcional almacenada. |

## Seguridad

El conjunto de pruebas crea y elimina buzones reales y reglas de enrutamiento
reales, y eliminar un usuario de Purelymail elimina su correo. Dos mecanismos
mantienen eso contenido.

Todos los objetos que crea el conjunto de pruebas se llaman
`<prefix>-<runid>-<role><n>`, donde el prefijo es `mh-it` de forma
predeterminada. Antes de que cualquier función auxiliar destructiva toque una
dirección, llama a `guardOwned`, que aborta la ejecución a menos que la dirección
esté en el dominio de prueba configurado **y** lleve el prefijo. Una prueba no
puede eliminar un buzón que no creó, aunque un cambio en el código se lo pida.

Cada prueba también registra sus objetos para la limpieza a medida que los crea,
de modo que una ejecución interrumpida o fallida igualmente desmonta lo que creó.

Aun así: **dirige el conjunto de pruebas a un dominio que no contenga nada que te
importe.** Un dominio de prueba dedicado en la cuenta es la configuración
correcta. Las protecciones protegen contra un mal comportamiento del conjunto de
pruebas; una errata en `MAILHEARTH_IT_DOMAIN` que casualmente nombre tu dominio y
tu prefijo de producción queda fuera de su alcance.

Cada prueba construye su propia base de datos vacía con su propia clave maestra
en un directorio temporal, así que nada toca tu instalación real.

## Cómo ejecutarlo

El dominio ya debe existir en la cuenta de Purelymail con registros MX que
funcionen, ya que la entrega es lo que miden la mayoría de estas pruebas. El
token de API necesita acceso completo.

```bash
export MAILHEARTH_IT_TOKEN=your-api-token
export MAILHEARTH_IT_DOMAIN=test.example.com

go test ./internal/integration -v -timeout 40m
```

Espera que el conjunto completo tarde varios minutos: la mayor parte del tiempo
se va en esperar a que el correo llegue de verdad. Para ejecutar una sola prueba
mientras iteras:

```bash
go test ./internal/integration -run TestMailboxLifecycle -v -timeout 15m
```

Las pruebas crean y eliminan usuarios en la cuenta. Purelymail factura por
usuario, así que una ejecución completa cuesta unas pocas fracciones de céntimo.

## Configuración

| Variable | Valor predeterminado | Propósito |
| --- | --- | --- |
| `MAILHEARTH_IT_TOKEN` | — | Token de API. Obligatorio; el conjunto de pruebas se omite sin él. |
| `MAILHEARTH_IT_DOMAIN` | — | Dominio de prueba. Obligatorio; el conjunto de pruebas se omite sin él. |
| `MAILHEARTH_IT_PREFIX` | `mh-it` | Prefijo de la parte local que marca los objetos propiedad del conjunto de pruebas. Debe empezar por `mh`. |
| `MAILHEARTH_IT_EXTERNAL` | — | Una dirección fuera de la cuenta. Habilita `TestExternalForwarding`. |
| `MAILHEARTH_IT_DELIVER_SECONDS` | `180` | Cuánto esperar a que llegue un mensaje antes de fallar. |
| `MAILHEARTH_IT_KEEP` | sin definir | Deja los objetos creados para inspeccionarlos. Debes limpiarlos tú mismo. |
| `MAILHEARTH_IT_API_URL` | `https://purelymail.com/api/v0` | Punto de conexión de la API. |
| `MAILHEARTH_IT_IMAP_ADDR` | `imap.purelymail.com:993` | Dirección IMAP. |
| `MAILHEARTH_IT_SMTP_ADDR` | `smtp.purelymail.com:465` | Dirección de envío. |
| `MAILHEARTH_IT_SIEVE_ADDR` | `mailserver.purelymail.com:4190` | Dirección de ManageSieve. Si está vacía, se omite la prueba de Sieve. |
| `MAILHEARTH_IT_IMAP_TLS` | `tls` | `tls`, `starttls` o `none`. |
| `MAILHEARTH_IT_SMTP_TLS` | `tls` | `tls`, `starttls` o `none`. |
| `MAILHEARTH_IT_SIEVE_TLS` | `starttls` | `tls`, `starttls` o `none`. |

## Cuando falla una prueba

Los tiempos de espera de entrega son el fallo habitual y normalmente significan
que el DNS del dominio está incompleto, y no que Mailhearth esté roto. Comprueba
primero los registros MX y SPF, y después sube `MAILHEARTH_IT_DELIVER_SECONDS`.
El filtrado de spam de Purelymail también puede archivar un mensaje de prueba en
Correo no deseado; el mensaje de fallo indica la carpeta que buscó y cuántos
mensajes vio allí.

Vuelve a ejecutar con `MAILHEARTH_IT_KEEP=1` para dejar los objetos en su sitio e
inspeccionarlos en la interfaz web de Purelymail. Recuerda eliminarlos después,
porque siguen costando dinero y la siguiente ejecución no los adoptará.

Si una ejecución se termina de forma tan brusca que se salta la limpieza, los
restos son fáciles de encontrar: todos ellos llevan el prefijo.

## Mantenerlo fuera de CI

No conectes este conjunto de pruebas a la canalización de solicitudes de
extracción. Necesita un token real, cuesta dinero y sus esperas de entrega lo
hacen demasiado lento para una barrera previa a la fusión. Ejecútalo antes de una
versión y después de cualquier cambio en el cliente de Purelymail, en las rutas
IMAP o SMTP, o en el compilador de Sieve.
