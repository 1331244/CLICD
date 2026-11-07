# 1331244/CLICD 部署与更新

此仓库是当前 WHMCS 集成使用的 CLICD 节点源码，保留上游 Apache 2.0 许可证及原有功能。首个维护版本为 `v1.1.38`，基于上游 `70d33fed695cd2bfd097bd8b5113fda3248f2059`，包含已验证的 WHMCS 兼容扩展。

## 已合入

- WHMCS 的独立计费限制、CPU%/Swap 持久化和离线公钥写入。
- 稳定 UUID、节点进程代次及端口映射条件写入校验。
- 付费流量重置的 SQLite 原子回执、重复请求恢复及采样基线处理。
- WHMCS 预留名称的 LXC 月流量耗尽后，上下行各限速 2 Mbps，实例保持运行；付费或自然月重置后恢复套餐带宽。命名内核 quota 在包路径触发，支持实例及节点进程重启后保持已消耗额度。
- 独立兼容配置表保存计费限制、资源字段和流量重置代次，避免 SQLite 保存/重启丢失扩展字段。

限速依赖 `nft` 的 bridge、命名 quota 和字节 rate limit。安装器包含 nftables 依赖；不要用含 `flush ruleset` 的全局配置覆盖节点规则。普通非 WHMCS 实例继续使用上游逻辑。

## 安装与升级来源

安装器和程序内升级默认从 **1331244/CLICD** 下载，`/api/version` 返回 repository 和构建 revision 以便核对来源。使用：

```sh
curl -fsSL https://raw.githubusercontent.com/1331244/CLICD/main/install.sh | sudo sh
```

已有 WHMCS 测试节点只更新二进制并保留现有网络、配置、数据库和实例；不重新执行完整安装流程。标准程序为 `/usr/local/bin/clicd`，服务 ExecStart 必须指向该路径，才能与原生 CLI 升级目标一致。升级过程不创建代码、配置或数据库备份，临时下载在结束后清理。

## 合并上游

```sh
git fetch upstream
git merge upstream/main
```

保留本仓库的维护提交，逐项解决冲突，再运行测试和构建。不要对维护分支执行强制同步或用供应商二进制覆盖。上游地址为 https://github.com/MengMengCode/CLICD 。

```sh
cd backend
go test ./...
cd ../frontend
npm ci
npm run build
```

在独立实例验证开通、暂停/恢复、重装、实际流量耗尽限速及付费重置；保留用户查看的服务。确认后推送维护提交，再针对已验证提交创建版本 tag。`Build` workflow 构建 amd64/arm64 安装包并发布到本仓库；上游按代码行数自动发版的 workflow 在此 fork 中跳过。

下一次改动继续从本仓库进行，无需在已集成源码上再次应用外部补丁脚本。WHMCS 模块源码单独维护，不包含在此节点仓库内。
