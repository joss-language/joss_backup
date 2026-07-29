# joss_backup 2.0

`joss_backup` crea, verifica, restaura y programa respaldos locales. El JP v2 contiene bytecode Joss, un indice publico para el editor y sidecars para Windows, Linux y macOS (amd64 y arm64). No requiere `use`, Go ni herramientas externas en la aplicacion que lo consume.

## Instalacion

```bash
joss pub add joss_backup 2.0.1
```

## Configuracion

| Variable | Uso |
| --- | --- |
| `BACKUP_PATH` | Directorio donde se guardan los respaldos locales. |
| `APP_KEY` | Clave usada como password cuando no se proporciona una con `password()`. |

El proveedor incluido es `local`. Solicitar un proveedor remoto no instalado devuelve un error explicito; los proveedores remotos se implementan como adaptadores separados.

## Uso

```joss
$id = Backup::create()
    ->files()
    ->encrypt(true)
    ->password("clave-de-respaldo")
    ->keep(7)
    ->run()

$valid = Backup::verify($id, "local", "clave-de-respaldo")
Backup::restore($id)->password("clave-de-respaldo")->run()
```

`create()`, `restore($id)` y `schedule()` devuelven un constructor fluente. Este ofrece `full`, `files`, `database`, `differential`, `incremental`, `provider`, `password`, `encrypt`, `at`, `daily`, `keep`, `run` y `save`. Tambien estan disponibles `list`, `delete`, `verify`, `testProvider` y `migrate` en `Backup`.

El archivo usa ZIP seguro, validacion al restaurar, proteccion contra path traversal, retencion y cifrado autenticado AES-256-GCM cuando se habilita el cifrado.

## Distribucion y desarrollo

`joss_backup.jp` empaqueta el codigo y los binarios para seis targets. Incluye `META-INF/joss-symbols.json`, que permite a la extension mostrar autocompletado y parametros sin cargar el fuente del plugin.

Para reconstruirlo se requiere Go y Joss 3.6.0 o posterior. El flujo de distribucion central compila y verifica los sidecars; un proyecto que instala el paquete no necesita ese entorno.
