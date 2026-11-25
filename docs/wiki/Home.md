# 计划任务插件（kore-plugin-cron）

kore-plugin-cron 是 kore 的 `cron` 插件，独立仓库、独立版本线、独立 CI。当前版本 `0.9.0`，协议 `kore.plugin.v1`。

## 这是什么

本插件是生产级计划任务插件，真实管理当前用户的 crontab：所有读写都通过 `crontab -l` / `crontab -` 完成，不直接改 `/var/spool/cron`；插件不内置调度器、不起 ticker、不轮询（G-19），实际触发由系统 cron 执行。能力边界（ADR-0019）：只管系统 crontab（shell / http 任务）；"到点调用插件方法"的 plugin 类型任务归内核 scheduler，插件只生成建议描述，不创建、不执行。

## 快速入口

- [安装](Installation)：插件包获取与安装步骤
- [配置](Configuration)：配置项、能力声明与环境变量
- [使用方法](Usage)：协议方法与典型调用
- [常见问题](FAQ)：边界、权限与降级行为
- [变更记录](Changelog)：语义化版本历史

## 相关链接

- 源码：https://github.com/kore-pl/kore-plugin-cron
- 内核与协议：https://github.com/kore-pl/kore
- 索引：https://github.com/kore-pl/kore-plugins
