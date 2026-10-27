# 生产部署维护

记录 2026-10-27 的共享库切换及 CI 部署权限修复。以下路径是正式服务器配置，密码和密钥不进入仓库。

## 部署用户与生产变量

部署目录为 `/opt/backup/refactorBBS/jnsecBBS`，用户为 `gha_deploy`。现有 SSH 密钥使用
`restrict` 和固定命令，只能进入该目录并执行 `chmod +x run.sh && ./run.sh`，原密钥限制保持不变。

`run.sh` 每次以 `.env.example` 覆盖 `.env`。只修改 `.env` 会在下次部署时丢失设置。
先前的 `docker-compose.override.yml` 为 root 所有且权限为 `600`，普通部署用户读取时触发
`PermissionError`；该文件已移出项目目录并保留私有备份。

当前主机 SSH 使用 `UsePAM yes`，`/etc/pam.d/sshd` 启用了 `pam_env.so user_readenv=1`。
生产变量放在 `/home/gha_deploy/.pam_environment`，由 PAM 在执行 SSH 固定命令前加载。
Docker Compose 优先使用继承的环境变量，因此 `.env` 被重新生成也不会改变生产连接或只读状态。
此方式依赖该主机已验证的 PAM 配置；其他服务器应先检查其 SSH/PAM 是否读取此文件。

文件所有者为 `gha_deploy`，权限 `600`。格式如下，示例中的占位值必须替换为服务器现有值：

```text
APP_DB_DSN DEFAULT="bbs_go_shared:CHANGE_ME@tcp(mysql:3306)/flarum?charset=utf8mb4&parseTime=true&loc=Local" OVERRIDE="bbs_go_shared:CHANGE_ME@tcp(mysql:3306)/flarum?charset=utf8mb4&parseTime=true&loc=Local"
READ_ONLY DEFAULT="false" OVERRIDE="false"
TRUSTED_ORIGINS DEFAULT="bbs.jnsec.org,new.bbs.jnsec.org" OVERRIDE="bbs.jnsec.org,new.bbs.jnsec.org"
SESSION_SECRET DEFAULT="KEEP_EXISTING_VALUE" OVERRIDE="KEEP_EXISTING_VALUE"
```

同一文件还保留现有 `SITE_NAME`、`COOKIE_SECURE`、`REGISTRATION`、`ASSETS_DIR`、
`APP_PORT` 和 `TZ`。可信来源填写主机名；`APP_PORT` 为 `127.0.0.1:8080`。
迁移变量时应保留当前会话密钥和业务配置。PAM 文件不是 shell 脚本，不能用 `source` 加载；
手工运维也应从该部署用户的新 SSH 会话执行，不能只凭重新生成的 `.env` 判断有效配置。

应用账号 `bbs_go_shared` 只有 `flarum.*` 的 `SELECT`、`INSERT`、`UPDATE`、`DELETE` 权限。
Go 辅助表由管理员备份后显式执行 `migrate-db` 建立；应用启动不会自动升级表。

## Nginx 与有限 sudo

正式配置为 `/etc/nginx/jnsec-bbs-production.conf`，活动链接为：

```text
/etc/nginx/sites-enabled/bbs.jnsec.org -> /etc/nginx/jnsec-bbs-production.conf
```

正式域名代理到 `127.0.0.1:8080`，保留 Host、客户端地址与 Cloudflare 转发协议头。
`run.sh` 仍会把旧模板复制到 `/etc/nginx/sites-available/bbs.jnsec.org`；该路径已不是活动配置，
不会把正式域名切回旧 PHP 入口。维护正式路由时应修改活动配置并执行 Nginx 检查及重载。

`/etc/sudoers.d/jnsec-bbs-deploy` 只允许脚本已有的三个命令免密执行：

```sudoers
gha_deploy ALL=(root) NOPASSWD: /usr/bin/cp deploy/nginx.conf /etc/nginx/sites-available/bbs.jnsec.org, /usr/sbin/nginx -t, /usr/sbin/nginx -s reload
```

该文件由 root 管理，权限 `440`，已用 `visudo -cf` 校验；未授予通用 sudo 权限。
复制命令的源路径与脚本当前工作目录有关，应继续使用仓库原有的固定部署命令。

## 服务与数据状态

- `bbs-app` 对外提供 Go 论坛，使用可写的共享运行库 `flarum`。
- `bbs-php` 与 `bbs-mysql` 保持运行；旧站仅保留本机维护入口。`jnsec-legacy-private.service`
  在 Docker `DOCKER-USER` 链阻断外部访问原 8081 端口，并随开机加载。
- `bbs`、`bbs_v2` 保留为停用副本；原生 `bbs_v2` 结构尚不能直接供在线 API 使用。
- `jnsec-bbs-sync.timer` 与 `jnsec-bbs-native-sync.timer` 已禁用，对应服务已停止。
- 共享 `wwwdata` 继续向 Go 容器提供只读旧站资源，`appdata` 保留 Go 上传文件。

切换或回退时先备份当前 `flarum` 与 `appdata`。已有新站写入后，不能直接用切换前的 SQL
备份覆盖运行库；重新开放旧 PHP 界面前也应验证其对新写入内容的兼容性。

## 验收与记录

生产验收使用仅允许本机来源、且具有与 CI 相同固定命令限制的临时 SSH 密钥，执行现有
`run.sh`，退出码为 `0`。镜像成功构建并替换为健康的 app 容器，部署源码为 `ee66f4c`
（包含 PR #9）。原有部署密钥未改动，临时验收密钥已删除。

随后通过正式 HTTPS 验证登录/退出、共享库资料写入、历史帖子、头像、Markdown 和非法来源
CSRF 拒绝。临时主题模拟“评论计数 143、已有第 144 楼”，两次回帖成功分配第 145、146 楼，
返回正确页面。临时主题、帖子、账号、资料和会话全部删除。

当前 GitHub 身份无权重跑旧失败任务，上述结果来自受限 SSH 实际部署验收，并非 Actions
重跑记录。后续 CI 仍通过原 SSH 密钥执行原 `run.sh`。

服务器私有备份与记录：

- `/opt/backup/shared-db-cutover-20261027-055323/`：切换前数据库与配置备份。
- `/opt/backup/cicd-env-repair-20261027-064239/`：覆盖文件备份、部署验收日志及权限修复记录。
- `/home/gha_deploy/JNSEC-DEPLOY.md`：部署用户可读的运维说明。

受保护的 `run.sh`、`Dockerfile`、`docker-compose.yaml`、`config.example.yaml`、`deploy/`、
`.github/` 未因本次修复修改。维护生产变量时使用服务器私有配置，不把凭据加入 PR。
