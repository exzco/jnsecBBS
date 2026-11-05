# 独立数据库结构与影子转换

`bbs native-import` 将 Flarum 快照转换到独立数据库（例如 `bbs_v2`）。
当前 Go API 仍使用兼容旧结构的 `bbs`，不能直接将应用 DSN 改成 `bbs_v2`。

```text
旧站 flarum ──镜像同步──> bbs ──> 当前 Go 站
          └──转换导入──> bbs_v2（隔离影子库）
```

核心表覆盖用户、用户组、权限、标签、主题、帖子、点赞、阅读位置和订阅。
导入保留旧 ID 与密码哈希，将正文转换为 Markdown，同时保存旧正文原始字节与
SHA-256。`legacy_tables` / `legacy_rows` 归档全部源表定义、列顺序及原始数据，
包括尚未映射的扩展字段。归档含私人数据，只能按数据库备份的权限管理。
附件文件不由该命令复制；保留引用不代表附件、通知和所有插件已经完成业务适配。

## 显式导入

源账号只授予源库 SELECT，目标账号只授予隔离目标库所需权限。
连接参数仅从环境变量读取，不提交实际密码。

```sh
export JNSEC_IMPORT_SOURCE_DSN='源库专用只读 DSN'
export JNSEC_IMPORT_TARGET_DSN='隔离影子库 DSN'
./bbs native-import -initialize -read-only-target
# 后续刷新，不加 -initialize
./bbs native-import -read-only-target
```

初始化要求目标为空。后续刷新校验源库身份、源/目标结构指纹和目标表集合。
源端使用一致性快照，目标数据在单个事务中替换；发现无效关系、计数或哈希差异时
回滚。目标 `sessions`、`user_profiles` 保留，`migration_state` 记录成功版本和计数。

命令只允许对仍然只读的影子目标执行覆盖刷新。完成最终迁移并开放业务写入后，
必须停止快照覆盖，不能将新站写入当成可以丢弃的影子数据。
该命令不修改 `deploy/`，也不自动安装或更改任何同步计划。

## 后续切换

需要完成 native 数据访问、登录会话、权限和扩展语义适配，经过 API 行为验收，
再安排最终导入和数据库切换。当前转换成功不代表在线服务已经使用新表结构。

本机 MySQL 集成测试可使用专门的临时实例：

```sh
BBS_NATIVE_TEST_DSN='root:测试密码@tcp(127.0.0.1:23306)/?parseTime=true' \
  go test ./internal/migration -count=1 -v
```

测试创建并清理独立临时数据库；不要指向生产实例。
