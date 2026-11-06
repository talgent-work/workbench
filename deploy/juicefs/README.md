# JuiceFS 元数据

文件系统元数据存放在 Redis 的专用 DB 中，与业务 Redis 使用的 DB 分开；地址以 Secret `talgent-juicefs-secret` 的 `metaurl` 为准。该实例开启 AOF，`maxmemory-policy` 为 `noeviction`，JuiceFS 依赖这两项：淘汰键或丢失写入都会损坏文件系统。监控内存、磁盘与 AOF/RDB 错误；单机故障会中断服务，持久化不能代替异机备份。

## 初始化

挂载客户端与运维 CLI 都使用该 `metaurl`。Secret 另含 `name`、`storage`、`bucket`、`access-key`、`secret-key`、`format-options`，按部署说明的 Secret 一节初始化。Secret 不进入源码或备份日志。首次空卷可由 CSI 按 Secret 格式化；恢复卷必须先 load，不能重新格式化。业务代码不得写这个 DB。

## 一致的备份点

1. 通过产品 API 停止所有执行并等待匹配退出和环境回收。停止 controlplane 及部署中其他服务的写入（数据库与文件）；停止期间不再创建或启动 Session。保存业务数据库备份，使 Work、Work Subject 和文件引用与文件系统恢复点一致；需要恢复会话时，同一停写窗口还须备份 NAS 上的 HOME 和 workspace（见部署说明的存储布局）。
2. 在能连接元数据 Redis 和对象存储的运维环境使用固定 JuiceFS 1.3.1 客户端，执行下面的元数据备份。然后在保持停止写入的窗口内将**整个卷的对象前缀**复制到备份位置；不能用挂载目录中的可见文件副本替代 JuiceFS 元数据和对象备份。HOME 与 workspace 位于 NAS，不在这个 JuiceFS 恢复点内。
3. 将元数据二进制、校验文件、对象副本和业务数据库备份作为一个恢复点保存到另一台机器或独立存储。单独安全保管对象存储密钥。

```bash
export JUICEFS_META_URL=<Secret 中的 metaurl>
scripts/juicefs/metadata.sh backup "$backup_dir/juicefs.bin"
# source 是运维机上 rc（RustFS CLI）的 alias；bucket 与 Secret 一致。
rc mirror "source/$bucket" "$backup_dir/objects"
```

`juicefs dump` 不提供在线快照一致性。自动元数据备份可作为补充，不能替代上述停止写入的配套恢复点。元数据默认不包含对象存储认证信息。

## 恢复和重新挂载

1. 准备空的 Redis DB（或独立实例），采用同一持久配置（AOF、`noeviction`）。不要向运行中的卷执行恢复，不使用 `FLUSHDB`，不恢复到旧 MySQL 元数据。
2. 将对象副本恢复到目标 bucket，恢复对应业务数据库。源卷保持停止写入，防止两个元数据实例同时修改同一对象前缀。
3. 使用空目标 Redis URL 加载并校验元数据，然后通过 `juicefs config` 设置目标 bucket 及认证。从受保护环境中的 `ACCESS_KEY`、`SECRET_KEY` 变量传入 config 的认证选项；JuiceFS 1.3.1 的 config 不会像 format 一样自动读取这两个环境变量。不使用 `dump --keep-secret-key`。
4. 将 CSI Secret 指向恢复后的 Redis/bucket；所有旧客户端卸载后重新挂载。验证环境使用新 PV `volumeHandle` 和 PVC，避免复用旧 CSI 挂载。生产切换前停止引用原 PVC 的工作负载并确认旧挂载已释放；不能仅修改 Secret 后复用仍在运行的挂载。

```bash
export JUICEFS_META_URL=redis://restored-metadata:6379/1
scripts/juicefs/metadata.sh restore "$backup_dir/juicefs.bin"
juicefs config "$JUICEFS_META_URL" --storage s3 --bucket "$JUICEFS_BUCKET" \
  --access-key "$ACCESS_KEY" --secret-key "$SECRET_KEY"
juicefs fsck "$JUICEFS_META_URL"
juicefs mount -d --no-usage-report "$JUICEFS_META_URL" /mnt/talgent-recovered
```

`load` 对非空元数据数据库失败即停止；不要删除现有数据库来绕过失败。业务数据库恢复后，由 Agent 按 AgentDefinition 与 Work Subject 定位同一 Work。会话恢复还需要对应的 NAS HOME 和工作区；仅恢复 JuiceFS 不会恢复 Claude 原生会话文件。

## 恢复验收

通过产品的文件下载入口重新下载附件原件和已发布制品，比较备份前后完整字节；恢复 NAS 后通过 Work 入口读取工作文件。以 UID/GID 1000 的新 OpenSandbox/execd 工作负载实际 Resume 原会话，确认原生推理请求包含此前对话并能继续输入；另验证空会话/缺失历史的原生失败没有变为新上下文。

NAS 恢复覆盖完整 HOME 的历史、配置、Plugin、共享记忆及其他资产。HOME 按所有者划分，每个 AgentDefinition 的 Works 共享其 HOME；按部署中每个 HOME 的所有者逐一验证。用挂载后的内容验证，不能仅比较 Namespace 或容器内路径。停止和关闭 Work 保留 HOME，关闭后的回收只删除对应 Work 工作区。

官方依据：[Redis 运维](https://juicefs.com/docs/community/redis_best_practices/)、[元数据备份恢复](https://juicefs.com/docs/community/metadata_dump_load/)、[元数据引擎](https://juicefs.com/docs/community/databases_for_metadata/)。恢复后按部署验证检查产品入口；历史恢复执行结果只适用于其当时的代码、存储布局和环境。
