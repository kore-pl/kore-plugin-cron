# Plugin de tarefas agendadas（kore-plugin-cron）

## Visão geral

Este plugin de nível de produção gerencia de verdade o crontab do usuário atual: todas as leituras e escritas passam por `crontab -l` / `crontab -`, sem tocar diretamente em `/var/spool/cron`; o plugin não embeda agendador, nem ticker, nem polling (G-19) — o disparo real é feito pelo cron do sistema. Escopo (ADR-0019): ele gerencia apenas o crontab do sistema (tarefas shell / http); tarefas do tipo plugin «chamar um método em determinada hora» pertencem ao agendador do kernel, e o plugin apenas gera uma descrição sugerida, sem criá-las nem executá-las.

## Superfície do protocolo

Protocolo `kore.plugin.v1`, `lifecycle=lazy`. Métodos: `kore.ping` / `shutdown` / `reload` e `cron.list` / `add` / `remove` / `replace` / `disable` / `enable` / `logs` / `runlog` / `run` / `templates` / `batch` / `migrate` / `config.get` / `config.set`. `cron.runlog` é um registro de execução somente leitura, com `limit` padrão 50 e máximo 200; `cron.batch` processa no máximo 50 entradas por chamada, sem rollback em falha parcial; `cron.migrate` reescreve linhas de wrapper que apontam para diretórios de versão no caminho estável e é declarado como `post_upgrade`.

## Funcionalidades principais

Cada escrita é salva e reescrita por completo; desabilitar mantém a definição original e apenas adiciona um marcador de comentário, enquanto habilitar a restaura literalmente; `cron.logs` lê os logs de inicialização do cron do sistema (journalctl / cron.log / syslog); `cron.runlog` é alimentado por `cron.run` e pelo wrapper `__task-run` em `<state>/runlog/runlog.jsonl`, consultando apenas o fim do arquivo, sem coletores em segundo plano, mantendo os primeiros 512 bytes e marcando `truncated`. Quatro opções de configuração — `backup_dir` / `runlog_dir` / `log_file` / `disable_journal` — são gravadas de volta nas variáveis `KORE_CRON_*` ao salvar, com efeito imediato.

## Limite de segurança

O plugin gerencia apenas o crontab do sistema e apenas linhas de tarefas que ele gerou; linhas não reconhecidas são preservadas e contadas com aviso; operações destrutivas são salvas antes da escrita para rastreabilidade. Tarefas do tipo plugin nunca são executadas pelo próprio plugin — são entregues ao agendador do kernel, evitando direitos além do escopo declarado; `cron.run` executa apenas comandos de tarefas já registradas. As capacidades são `system:read` (leitura) e `system:admin` (escrita). Burlar a interface do crontab editando diretamente o diretório spool está fora do escopo suportado e é uma limitação do ambiente.

## Build e verificação

A verificação diária usa `make check` local (gofmt → go vet → go test -timeout 200s); antes do lançamento, `make dist` duas vezes com sha256 idêntico. GitHub Actions apenas manual, Gitea Actions por tag de versão. Nível de profundidade `production`; os testes de contrato de novos métodos como `cron.runlog` rodam junto com a verificação usual.

## Links relacionados

- [源码](https://github.com/kore-pl/kore-plugin-cron)
- [kore 内核与协议](https://github.com/kore-pl/kore)
- [插件索引](https://github.com/kore-pl/kore-plugins)
