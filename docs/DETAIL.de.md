# Plugin für geplante Aufgaben（kore-plugin-cron）

## Überblick

Dieses Plugin in Produktionsqualität verwaltet die crontab des aktuellen Benutzers tatsächlich: alle Lese- und Schreibzugriffe laufen über `crontab -l` / `crontab -`, niemals direkt über `/var/spool/cron`; das Plugin enthält keinen Scheduler, keinen Ticker und kein Polling (G-19) — die eigentliche Auslösung erfolgt durch den System-cron. Geltungsbereich (ADR-0019): Es verwaltet nur die System-crontab (shell / http Aufgaben); Aufgaben des Typs „Plugin-Methode planmäßig aufrufen“ gehören zum Kernel-Scheduler, das Plugin erzeugt nur eine vorgeschlagene Beschreibung, ohne sie zu erstellen oder auszuführen.

## Protokollfläche

Protokoll `kore.plugin.v1`, `lifecycle=lazy`. Methoden: `kore.ping` / `shutdown` / `reload` sowie `cron.list` / `add` / `remove` / `replace` / `disable` / `enable` / `logs` / `runlog` / `run` / `templates` / `batch` / `migrate` / `config.get` / `config.set`. `cron.runlog` ist ein schreibgeschütztes Ausführungsprotokoll, `limit`的默认wert 50, Maximum 200; `cron.batch` verarbeitet höchstens 50 Einträge pro Aufruf ohne Rollback bei Teilfehlern; `cron.migrate` schreibt Wrapper-Zeilen, die auf Versionsverzeichnisse zeigen, auf den stabilen Pfad um und ist als `post_upgrade` deklariert.

## Kernfunktionen

Jeder Schreibvorgang wird gesichert und vollständig neu geschrieben; das Deaktivieren behält die ursprüngliche Definition und fügt nur eine Kommentarmarkierung hinzu, das Aktivieren stellt sie wortgetreu wieder her; `cron.logs` liest die System-cron-Startprotokolle (journalctl / cron.log / syslog); `cron.runlog` wird von `cron.run` und dem `__task-run`-Wrapper an `<state>/runlog/runlog.jsonl` angehängt, wobei nur das Dateiende gelesen wird, ohne Hintergrundsammler, mit den ersten 512 Bytes und gesetztem `truncated`. Vier Konfigurationsoptionen — `backup_dir` / `runlog_dir` / `log_file` / `disable_journal` — werden beim Speichern in `KORE_CRON_*`-Umgebungsvariablen zurückgeschrieben und wirken sofort.

## Sicherheitsgrenze

Das Plugin verwaltet nur die System-crontab und nur von ihm erzeugte Aufgabenzeilen; unerkannte Zeilen bleiben unverändert und werden mit einer Warnung gezählt; destruktive Operationen werden vor dem Schreiben gesichert. Aufgaben des Typs Plugin werden nie vom Plugin selbst ausgeführt, sondern an den Kernel-Scheduler übergeben, um Rechte außerhalb deklarierter Grenzen zu vermeiden; `cron.run` führt nur Befehle bereits registrierter Aufgaben aus. Die Fähigkeiten sind `system:read` (lesen) und `system:admin` (schreiben). Das Umgehen der crontab-Schnittstelle durch direktes Bearbeiten des Spool-Verzeichnisses liegt außerhalb des unterstützten Bereichs und ist eine Umgebungsbegrenzung.

## Build und Verifizierung

Die tägliche Prüfung nutzt lokales `make check` (gofmt → go vet → go test -timeout 200s); vor dem Release `make dist` zweimal mit identischem sha256. GitHub Actions nur manuell, Gitea Actions bei Versions-Tags. Tiefe `production`; Vertragstests für neue Methoden wie `cron.runlog` laufen mit dem regulären Gate.

## Verwandte Links

- [源码](https://github.com/kore-pl/kore-plugin-cron)
- [kore 内核与协议](https://github.com/kore-pl/kore)
- [插件索引](https://github.com/kore-pl/kore-plugins)
