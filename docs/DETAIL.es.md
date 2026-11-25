# Complemento de tareas programadas（kore-plugin-cron）

## Resumen

Este complemento de nivel producción gestiona de verdad el crontab del usuario actual: todas las lecturas y escrituras pasan por `crontab -l` / `crontab -`, sin tocar `/var/spool/cron` directamente; no incorpora planificador, ni ticker, ni sondeo (G-19): el disparo real lo hace el cron del sistema. Alcance (ADR-0019): solo gestiona el crontab del sistema (tareas shell / http); las tareas de tipo plugin «llamar a un método a una hora» corresponden al planificador del núcleo, y el complemento solo genera una descripción sugerida sin crearlas ni ejecutarlas.

## Superficie del protocolo

Protocolo `kore.plugin.v1`, `lifecycle=lazy`. Métodos: `kore.ping` / `shutdown` / `reload` y `cron.list` / `add` / `remove` / `replace` / `disable` / `enable` / `logs` / `runlog` / `run` / `templates` / `batch` / `migrate` / `config.get` / `config.set`. `cron.runlog` es un registro de ejecución de solo lectura con `limit` por defecto 50 y máximo 200; `cron.batch` acepta hasta 50 entradas por llamada sin revertir fallos parciales; `cron.migrate` reescribe las líneas wrapper que apuntan a directorios de versión a la ruta estable y se declara como `post_upgrade`.

## Funcionalidades principales

Cada escritura se respalda y se reescribe por completo; deshabilitar conserva la definición original y solo añade un comentario, mientras habilitar la restaura literalmente; `cron.logs` lee los registros de arranque del cron del sistema (journalctl / cron.log / syslog); `cron.runlog` es añadido por `cron.run` y el envoltorio `__task-run` en `<state>/runlog/runlog.jsonl`, consultando solo el final del archivo sin colectores en segundo plano, conservando los primeros 512 bytes y marcando `truncated`. Cuatro opciones de configuración — `backup_dir` / `runlog_dir` / `log_file` / `disable_journal` — se escriben de vuelta en variables `KORE_CRON_*` al guardar para surtir efecto inmediato.

## Límite de seguridad

El complemento solo gestiona el crontab del sistema y únicamente las líneas que él genera; las líneas no reconocidas se conservan tal cual y se cuentan con advertencia; las operaciones destructivas se respaldan antes de escribir para poder rastrearlas. Las tareas de tipo plugin nunca las ejecuta el propio complemento: se entregan al planificador del núcleo, evitando permisos de ejecución fuera del alcance declarado; `cron.run` solo ejecuta comandos de tareas ya registradas. Las capacidades son `system:read` (lectura) y `system:admin` (escritura). Eludir la interfaz de crontab para editar el directorio spool directamente queda fuera del alcance soportado y es una limitación del entorno.

## Compilación y verificación

La verificación diaria usa `make check` local (gofmt → go vet → go test -timeout 200s); antes de publicar, `make dist` dos veces con sha256 idéntico. GitHub Actions solo manual, Gitea Actions por tag de versión. Nivel de profundidad `production`; las pruebas de contrato de métodos nuevos como `cron.runlog` se ejecutan con la puerta habitual.

## Enlaces relacionados

- [源码](https://github.com/kore-pl/kore-plugin-cron)
- [kore 内核与协议](https://github.com/kore-pl/kore)
- [插件索引](https://github.com/kore-pl/kore-plugins)
