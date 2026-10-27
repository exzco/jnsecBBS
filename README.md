# JNSEC BBS 

原有 PHP Flarum 论坛系统进行重构。项目基于 **Go 1.25** 与 **React 19** 构建，采用前后端单二进制嵌入交付，在完全兼容 Flarum 历史数据（9,000+ 讨论与帖子、s9e 富文本、bcrypt 密码、旧版 URL）的前提下，实现轻量化与自动化运维。

### 后端服务 (Backend)
- **核心语言与框架**：Go 1.25+、Gin 
- **数据持久化**：GORM ，生产环境运行于 MySQL 5.7
- **内容解析与安全**：
  - `goldmark`：CommonMark 标准 Markdown 解析器
  - `bluemonday`：HTML 白名单净化过滤（抵御 XSS 注入）
  - `internal/flarum`：实现 Flarum s9e XML 逆向还原引擎（还原历史发帖原文且保持 SHA-256 强一致）提取数据
- **打包集成**：标准库 `embed.FS`，前端构建产物直接内嵌于单一 Go 二进制可执行文件。

### 前端系统 (Frontend)
- **开发与构建**：React 19、TypeScript、Vite 8；本地与 CI 支持 Bun，生产镜像使用 Node/npm
- **样式与组件**：Tailwind CSS v4、Radix UI Primitives、Lucide React 图标库、Geist Variable 字体
- **状态与缓存**：TanStack React Query v5（服务端状态与缓存）、NanoStores（轻量原子前端状态）

### 运维与基础设施 (Infra & DevOps)
- **容器与网关**：Docker、Docker Compose、Nginx（宿主机反代与子域名分流）
- **CDN 与证书**：Cloudflare CDN（边缘 HTTPS 卸载与安全防护）
- **迁移期同步工具**：Python 3 同步脚本 (`scripts/sync-flarum.py`) + Linux Systemd Timer；当前生产共享数据库，定时任务已停用

---

## 2. 项目架构 (Project Architecture)

```
jnsecBBS/
├── cmd/bbs/               # 命令行总入口 (serve, init-db, seed-admin, audit-content)
├── internal/
│   ├── api/               # RESTful API 路由分发、业务控制器与只读拦截器
│   ├── auth/              # 鉴权引擎 (RBAC 角色组、Session 会话、密码校验)
│   ├── config/            # 配置映射 (支持 config.yaml 与 JNSEC_* 环境变量覆盖)
│   ├── db/                # 数据库连接、GORM 迁移与只读检查
│   ├── flarum/            # Flarum s9e XML 格式还原引擎 (与旧版 PHP 行为完全等价)
│   ├── frontend/          # embed.FS 静态产物内嵌入口 (dist/*)
│   ├── markdown/          # Markdown 渲染与 XSS 净化管道
│   ├── models/            # 数据模型定义 (兼容原 Flarum 表结构及扩展表)
│   ├── web/               # Web 服务装配 (Gin 引擎、CSRF 中间件、SPA Fallback、头像生成)
├── frontend/              # React 19 SPA 前端工程 (源码、组件、状态与页面)
├── deploy/                # 部署配置 (Nginx 配置模板、Systemd 单元、SQL 迁移脚本)
├── scripts/               # 核心同步工具 (sync-flarum.py 及其实际环境自动化测试)
├── Dockerfile             # 多阶段构建文件 (Node/npm 构建前端 -> Go 编译静态二进制 -> Alpine 运行)
└── docker-compose.yaml    # 生产容器编排文件
```

### 核心处理链路
```mermaid
flowchart TD
    Client[客户端浏览器] -->|HTTP / HTTPS| WebServer[internal/web/server.go]
    WebServer --> CSRF[CSRF 中间件 gorilla/csrf]
    CSRF --> Session[Session 会话装配]
    Session --> Router{路由分发}
    
    Router -->|/api/*| API[internal/api 业务控制层]
    Router -->|/assets/* /avatars/*| Static[静态资源 / 字环头像生成]
    Router -->|其他路由| SPA[internal/frontend 内嵌 React SPA]

    API --> ReadOnlyCheck{只读模式检查?}
    ReadOnlyCheck -->|写入操作且处于只读期| Reject[拦截返回 403 / code=read_only]
    ReadOnlyCheck -->|读操作或允许写入| Service[数据访问层 & 内容处理]
    
    Service --> FlarumXML[internal/flarum s9e 还原引擎]
    FlarumXML --> Markdown[internal/markdown goldmark + bluemonday]
    Service --> DB[(GORM -> MySQL / SQLite)]
```

---

## 3. CI/CD 架构 (CI/CD Architecture)

代码仓库使用 **GitHub Actions** 自动化驱动持续集成与持续部署（CI/CD）：
![alt text](image-1.png)

```mermaid
flowchart LR
    Push([Push to main]) --> CI_Go[Job: Go 检验]
    Push --> CI_Front[Job: 前端构建检验]
    
    CI_Go -->|gofmt / vet / race test| Gate{全部通过?}
    CI_Front -->|bun run build| Gate
    
    Gate -->|通过| Deploy[Job: SSH 部署]
    Gate -->|失败| Block([阻断流程并报警])
    
    Deploy -->|appleboy/ssh-action| SSH[连接目标 VPS]
    SSH --> RunScript[执行 run.sh]
    RunScript --> Compose[Docker Compose 本地增量构建 & 容器热更]
```

### 部署关键细节
1. **测试门禁**：
   - 后端执行 `gofmt` 格式断言、`go vet` 静态排查以及 `go test -race` 竞态安全测试。
   - 前端通过 `bun --frozen-lockfile` 安装并执行 `tsc -b && vite build`，确保无类型及语法缺陷。
2. **安全连接**：
   - 依赖 GitHub Secrets：`VPS_HOST`、`VPS_USER`、`VPS_SSH_KEY`、`VPS_FINGERPRINT`。
3. **CD 触发策略**：
   - 为避免非生产变更频繁触发构建，CD（`deploy` Job）仅在以下两种场景执行：
     - **PR 合并到 main**：合并提交（包含 `Merge pull request`、`Merge branch` 或 PR 编号）。
     - **以 `cicd` 开头的提交**：例如 `cicd: 重新部署`、`cicd: force rebuild`。
   - 其他常规提交（如 `docs:`, `fix:`, `refactor:` 等）仅执行 CI 检查，自动跳过 CD 部署。
4. **VPS 落地触发**：
   - 经 SSH 连接后进入 `/opt/backup/refactorBBS/jnsecBBS`，触发 `run.sh` 执行 `git pull --ff-only`、配置自动同步、镜像构建与 `docker-compose up -d --no-deps app` 容器平滑切换。
   - 正式部署用户 `gha_deploy` 的 SSH 密钥仅允许执行现有 `run.sh`。生产变量由服务器 PAM 在 SSH 登录时加载，优先于脚本重新生成的 `.env`；项目目录不再使用 `docker-compose.override.yml`。
   - 正式 Nginx 配置独立保存，部署用户仅获得脚本现有 Nginx 操作所需的有限免密 sudo。配置位置、原因和验收方式见 [生产部署维护](docs/production-deployment.md)。

---

## 4. 部署后架构与运行时拓扑 (Deployed Architecture)

2026-10-27 已切换为**新站对外读写、Go 与旧站共享 `flarum` 数据库**。旧站和 MySQL 保持运行，旧站公网入口关闭；`bbs`、`bbs_v2` 保留为停用副本，两个同步任务均已停止。

```mermaid
flowchart TD
    User([终端用户]) -->|HTTPS 请求| Cloudflare[Cloudflare CDN / SSL 卸载]
    
    Cloudflare -->|HTTP:80 透传| Nginx[VPS 宿主机 Nginx 网关]
    
    subgraph DomainRouting [生产入口]
        Nginx -->|bbs.jnsec.org| GoApp[新站容器 bbs-app<br/>127.0.0.1:8080 / 可读写]
        Operator[服务器本机维护] -->|127.0.0.1:8081| PHPApp[旧站容器 bbs-php<br/>保持运行 / 公网端口已阻断]
    end
    
    subgraph DataStorage [MySQL 5.7 / bbs-mysql]
        PHPApp -->|保留连接| DB_Flarum[(共享运行库: flarum)]
        GoApp -->|读写| DB_Flarum
        Inactive[(停用副本: bbs / bbs_v2)]
    end

    subgraph Volumes [共享持久化卷]
        Vol_WWW[外部卷 wwwdata] -.->|挂载 /var/www/bbs| PHPApp
        Vol_WWW -.->|只读挂载 /flarum:ro| GoApp
        Vol_App[卷 appdata] -.->|挂载 /app/data| GoApp
    end
```

### 运行特征说明
1. **正式流量**：
   - `bbs.jnsec.org` 指向 Go 论坛，`JNSEC_READ_ONLY=false`，使用专用账号对 `flarum` 执行查询及业务写入。
   - 旧站保留本机维护入口；`jnsec-legacy-private.service` 在 Docker 转发链阻断外部访问 8081，规则随开机加载。
   - Nginx 同时接受 `new.bbs.jnsec.org`，但切换验收时该域名未能解析，正式入口以 `bbs.jnsec.org` 为准。
2. **数据与同步状态**：
   - Go 使用原 Flarum 表及显式迁移建立的 Go 辅助表；`bbs_v2` 的原生结构尚未接入在线 API。
   - `jnsec-bbs-sync.timer`、`jnsec-bbs-native-sync.timer` 均已禁用，对应服务已停止。共享模式无需同步，也不删除停用副本。
3. **资源无缝共享**：
   - 共享 Docker 卷 `wwwdata`：PHP 容器挂载写入，Go 容器只读挂载至 `/flarum:ro`，旧头像与附件直接由 Go 容器的 `/assets` 路由透出，免除额外磁盘同步开销。

---

## 5. 快速开始与常用操作

### 本地开发
```bash
cd frontend
bun install --frozen-lockfile
bun run build
cd ..

go build -o bbs ./cmd/bbs
export JNSEC_SESSION_SECRET="$(openssl rand -hex 32)"
./bbs init-db -c config-test.yaml  # 仅适用于空数据库
./bbs serve -c config-test.yaml
```

应用启动仅校验数据库，不自动建表。已有兼容库由管理员显式执行
`./bbs migrate-db -c config-test.yaml`，升级 Go 辅助表与迁移版本记录。仅对选定的 Go
运行库执行迁移；共享库先备份并由管理员迁移，应用账号无需 DDL 权限。只读镜像同步会保留 `bbs_user_profiles`、`bbs_schema_migrations` 等已批准
本地表，继续拒绝未知表及结构漂移。

`bbs native-import` 可将旧站快照转换到隔离的 `bbs_v2` 影子库，保留原始数据归档。
在线 API 尚未适配该结构，不可直接修改应用 DSN。详见
[独立数据库迁移](docs/native-migration.md) 和 [当前进度](PLAN.md)。

已修复回复原位编辑、分类返回列表及分页跳转（#5–#7），保持现有三栏样式。
PR #9 修复历史楼层缺口及并发回帖导致的重复楼层错误，使用 MySQL 5.7 完成并发回归，
并随生产部署验收通过。当前服务器配置及验收记录见 [生产部署维护](docs/production-deployment.md)。

### 生产同步任务维护
```bash
# 共享库模式下两个定时器应为 disabled，对应服务应为 inactive
sudo systemctl is-enabled jnsec-bbs-sync.timer jnsec-bbs-native-sync.timer
sudo systemctl is-active jnsec-bbs-sync.service jnsec-bbs-native-sync.service
# 查看最近 20 条同步执行日志
sudo journalctl -u jnsec-bbs-sync.service -n 20 --no-pager
```

`is-enabled` / `is-active` 对禁用或停止状态返回非零退出码，这是上述检查的预期结果。
重新引入镜像同步或切换运行库前，需单独制定数据切换方案。

## 6、测试 issue 

浏览新站测试得到问题的同学在 issue 发一下具体问题以及简单的复现步骤，也许是这样说，别在 QQ 群说了，人多了问题多了不太好记。
