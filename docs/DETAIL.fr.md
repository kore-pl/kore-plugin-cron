# Plugin de tâches planifiées（kore-plugin-cron）

## Aperçu

Ce plugin de niveau production gère réellement le crontab de l’utilisateur courant : toutes les lectures/écritures passent par `crontab -l` / `crontab -`, sans toucher directement à `/var/spool/cron` ; il n’intègre aucun ordonnanceur, aucun ticker et aucun sondage (G-19) — le déclenchement est assuré par le cron système. Périmètre (ADR-0019) : il ne gère que le crontab système (tâches shell / http) ; les tâches de type plugin « appeler une méthode à une heure donnée » relèvent de l’ordonnanceur du noyau, le plugin ne produisant qu’une description suggérée sans les créer ni les exécuter.

## Surface du protocole

Protocole `kore.plugin.v1`, `lifecycle=lazy`. Méthodes : `kore.ping` / `shutdown` / `reload` et `cron.list` / `add` / `remove` / `replace` / `disable` / `enable` / `logs` / `runlog` / `run` / `templates` / `batch` / `migrate` / `config.get` / `config.set`. `cron.runlog` est un journal d’exécution en lecture seule, avec `limit` par défaut 50 et plafonné à 200 ; `cron.batch` traite au plus 50 entrées par appel sans rollback en cas d’échec partiel ; `cron.migrate` réécrit les lignes wrapper pointant vers des répertoires de version vers le chemin stable et est déclaré comme `post_upgrade`.

## Fonctionnalités clés

Chaque écriture est sauvegardée puis réécrite intégralement ; désactiver conserve la définition d’origine et n’ajoute qu’un marqueur de commentaire, tandis que réactiver la restaure à l’identique ; `cron.logs` lit les journaux de démarrage du cron système (journalctl / cron.log / syslog) ; `cron.runlog` est alimenté par `cron.run` et le wrapper `__task-run` dans `<state>/runlog/runlog.jsonl`, avec une lecture de la fin du fichier uniquement, sans collecteur en arrière-plan, en gardant les 512 premiers octets et en positionnant `truncated`. Quatre options — `backup_dir` / `runlog_dir` / `log_file` / `disable_journal` — sont réécrites dans les variables `KORE_CRON_*` à l’enregistrement pour un effet immédiat.

## Périmètre de sécurité

Le plugin ne gère que le crontab système et uniquement les lignes de tâches qu’il a générées ; les lignes non reconnues sont conservées telles quelles et comptées avec un avertissement ; les opérations destructives sont sauvegardées avant écriture pour traçabilité. Les tâches de type plugin ne sont jamais exécutées par le plugin lui-même : elles sont confiées à l’ordonnanceur du noyau, évitant des droits d’exécution hors périmètre déclaré ; `cron.run` n’exécute que les commandes de tâches déjà enregistrées. Les capacités sont `system:read` (lecture) et `system:admin` (écriture). Contourner l’interface crontab pour modifier directement le répertoire spool est hors périmètre pris en charge et relève d’une limitation de l’environnement.

## Compilation et vérification

La vérification quotidienne utilise `make check` en local (gofmt → go vet → go test -timeout 200s) ; avant publication, `make dist` deux fois avec sha256 identique. GitHub Actions est manuel uniquement, Gitea Actions se déclenche sur les tags de version. Niveau de profondeur `production` ; les tests de contrat des nouvelles méthodes comme `cron.runlog` passent avec la porte habituelle.

## Liens connexes

- [源码](https://github.com/kore-pl/kore-plugin-cron)
- [kore 内核与协议](https://github.com/kore-pl/kore)
- [插件索引](https://github.com/kore-pl/kore-plugins)
