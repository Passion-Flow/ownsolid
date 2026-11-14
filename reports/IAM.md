# IAM Mac 迁移保全报告

令牌已释放；本线已停止，不再追加验证、归档或开发。

## 1. 结果与远端

本轮 main 新增 0 个提交。一次 atomic push 成功保全 17 个 salvage 分支与 9 个原始证据标签，共 26 个远端引用；包括 10 个原始分支、3 份原 SHA stash、3 份明确未验收的原 tree 快照和 1 份独立源码归档。原始候选共有 88 个不在当前 main 中的独有提交，已随原始 DAG 保全。所有远端引用已逐项回读核对。

- Archive: `refs/heads/salvage/mac/iam/private-source-1813fdb24816`
- Archive SHA: `1813fdb2481676a74aabc2965a4428ecefb19beb`
- Main SHA: `5790b8961955400ffe25f10ced170bf4f669683c`

## 2. 已完成条目

路线图维持 P1 5/19、P2 0/6：已勾选 IAM-101、112、115、116、117。本次不新增验收结论，不改变勾选。

## 3. 未完成停点与下一步

b3c2a700 的既有证据限于 3 个无数据库测试根和 3 个清理子测试通过；不包含真实 SQL、启动角色、只读挂载和网关 drain。c2b955ed 的 HTTP SAML 完成候选尚未编译或测试；既有门禁因原始空闲内存不足退出 78，不能沿用旧 v4 或 stage 测试结论。UI、安装配置、生产入口接入和完整验收仍开放。模块顺序下一条为 IAM-102；迁移轮次不继续研发。

## 4. 必须继承的约束

旧 workstation lock epoch 已退役，归档脚本只供阅读，不得直接运行或重新创建旧锁。全量构建必须遵守新的持久载体身份与资源准入。共享/仅 reflog 历史交给协调器统一兜底；REL 的三个含 IAM 名称引用不属于本线，保持原样。服务器留待 CPU 阶段，本轮没有访问或修改服务器。没有删除分支、工作树、stash、原始脚本或 bundle，也没有强推或改写原始历史。

## 5. 另一台电脑重建

在独立 checkout 获取下列 salvage 引用。产品候选按准确 SHA 建立新工作树；私人原型从源码归档读取 archive-files.json，按原路径和 SHA-256 还原。exclusions.json 中标明 Git-backed 的文件按给出的 commit/path 取回；依赖按锁定清单重新准备，凭据和临时 fixture 重新生成。保留源码归档分支独立，不能合入 main。

```bash
git fetch origin 'refs/heads/salvage/mac/iam/*:refs/remotes/origin/salvage/mac/iam/*'
git worktree add --detach ../iam-mac-source-archive 1813fdb2481676a74aabc2965a4428ecefb19beb
```

## 6. 验证、排除与交接

源码文件逐项核对 SHA-256，Git 原始分支、stash 和 HEAD 保持不变；bundle list-heads/verify、归档对象建立、atomic push 与远端回读均退出 0。make check、make build、make test 均为 NOTRUN：采样 raw Free 为 5,597,200,384 字节，低于 full profile 的 6 GiB；锁仅 lstat，未打开或重建。迁移保全不代表功能验收。

保全 993 个源码/必要配置/交接文件，最终源码归档共 1002 个文件；排除清单共 13,525 项，含原始文件、tar 成员、目录及符号链接。排除依赖、编译件、数据库、运行时 fixture、凭据、日志和临时输出；已在 Git 中逐字节相同的快照用准确提交映射重建。2 个独立 bundle 与 3 个 tar 中副本全部指向已保全的 b3c2a700/c2b955ed，无 bundle-only head；bundle 未作为文件提交。源码脚本中未发现二进制 bundle/payload，不需要源码投影。

## Remote ref -> SHA

| Ref | SHA |
| --- | --- |
| `refs/heads/salvage/mac/iam/head-codex-iam-agent-readers-20261113-b3c2a700fe55` | `b3c2a700fe55e50be61c35155f9d43de665ff5ae` |
| `refs/heads/salvage/mac/iam/head-codex-iam-audit-http-9a6d1b6cd3b7` | `9a6d1b6cd3b713e71eb25fdcd5618159f42eb088` |
| `refs/heads/salvage/mac/iam/head-codex-iam-audit58-input-0f02cf93-0f02cf937479` | `0f02cf937479aeae9dcbdea8da4ee8ef27f551a5` |
| `refs/heads/salvage/mac/iam/head-codex-iam-saml-completion-http-20261113-c2b955ed7aca` | `c2b955ed7aca6cdaae98ef22d2a0759d92bac5ba` |
| `refs/heads/salvage/mac/iam/head-codex-iam-service-principals-0fd74f924d80` | `0fd74f924d8079ed16987e8556b47acac2522237` |
| `refs/heads/salvage/mac/iam/head-codex-iam-session-devices-4e8a99ea80aa` | `4e8a99ea80aa1c0806bdd1a76d07566db77b1764` |
| `refs/heads/salvage/mac/iam/head-codex-iam-verified-36313f07-36313f07a309` | `36313f07a309b413ec7a09574439c4cae57ae19f` |
| `refs/heads/salvage/mac/iam/head-codex-iam-verified-544c77a9-544c77a92c04` | `544c77a92c0495be55c47bbb6015a176005277fa` |
| `refs/heads/salvage/mac/iam/head-codex-iam119-recoverable-sso-5790b8961955` | `5790b8961955400ffe25f10ced170bf4f669683c` |
| `refs/heads/salvage/mac/iam/head-lane-iam-bad52f760e1f` | `bad52f760e1f772e160608a52eb9f88a0156035b` |
| `refs/tags/iam-evidence/20261112-6100a2fe` | `6100a2fe78f81db3b289cc335b5a3ce2efd57023` |
| `refs/tags/iam-evidence/joint-handoff-d575e86e` | `d575e86ed93d649a2018af909ebc75b6e45dabe1` |
| `refs/tags/iam-evidence/migration-88404b04` | `88404b0410787a9af14354f3ed5bdcfdb10ca27a` |
| `refs/tags/iam-evidence/offline-ab02c3b1` | `ab02c3b15f249028373df2c5f1f925d17a97ee55` |
| `refs/tags/iam-evidence/original-candidates` | `e1e6363e1e67e82bf70e9a26630a118f8934fcf8` |
| `refs/tags/iam-evidence/pre-merge-126523d8` | `126523d883efd153c1afbfd3cc98bb35a01401e2` |
| `refs/tags/iam-evidence/resumed-0abd4f3b` | `0abd4f3b5dbe6e3a8aa342b8da9fdd0dddf068f2` |
| `refs/tags/iam-evidence/shared-device-91d77b14` | `91d77b143d17044c7dbad82ee9f71f466a5c5bd2` |
| `refs/tags/iam-evidence/sso-keyring-7e77b814` | `7e77b814cb0ff61aa1b1d25b00d22a4ac5ba64c2` |
| `refs/heads/salvage/mac/iam/stash-0-c1b11aab5179` | `c1b11aab517905bcd793989822b28a6d71fa9f18` |
| `refs/heads/salvage/mac/iam/stash-1-a35691d4c7b3` | `a35691d4c7b321666691f71adc1fb215cc732b06` |
| `refs/heads/salvage/mac/iam/stash-2-7a396121171d` | `7a396121171df1b553ba2f51aef7183b4cf5c4d4` |
| `refs/heads/salvage/mac/iam/review-tree-88af72c92b00-96c0f074ed56` | `96c0f074ed56a52ef96ae58ff267503cf1805fee` |
| `refs/heads/salvage/mac/iam/review-tree-0abbf5f0db21-4227bca732b7` | `4227bca732b7bf7afabd8d2b0609c131e0e9dc3d` |
| `refs/heads/salvage/mac/iam/review-tree-1af0ca5497fb-a8bea3d0e32a` | `a8bea3d0e32a8ca325a756eaa0014155b1addc2a` |
| `refs/heads/salvage/mac/iam/private-source-1813fdb24816` | `1813fdb2481676a74aabc2965a4428ecefb19beb` |

## Local handoff files

- Receipt: `/Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-iam/final-receipt.json`
- Manifest: `/Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-iam/archive-manifest.json`
- Exclusions: `/Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-iam/archive-content/exclusions.json`
- Bundle coverage: `/Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-iam/bundle-coverage.json`

