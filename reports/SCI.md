# Mac SCI migration handoff

1. 本轮推进 main 0 个提交；按授权走 salvage，原 SHA 保全 4 个独立 tip，覆盖 5 个原始 ref 名称。未提交源码为空，没有新增产品提交。

2. 本轮没有新增 roadmap 勾选。既有审计模块 AUD-102、AUD-105、AUD-110 已打勾；AUD-102 包含前任成果，AUD-110 属于 Web 线，不冒领。评测和科研计算 P1 仍未全部验收。

3. 8531226d 组件实现已由 GitHub main 覆盖，实际安装范围及各入口验收仍开放。EVL 持久化、报告授权和路由，以及 SCI 统一作业、模型许可准入和对象存储依赖未完成。下一阶段由对应所有者继续。本轮不补实现；服务器 /root/lane-sci 及 4aa7bfbe 留待 CPU 迁移。

4. 历史 audit58 分支是原型和旧 schema 输入，不可当作当前 main 的可直接重放迁移或完整验收。旧审计记录不在哈希链覆盖内，检测数据库所有者整体重写仍须外部独立保管检查点。评测证据仅覆盖已测试的固定 CPU 配置；受限科研模型须客户持有精确制品和用途的有效许可。Mac 旧私有脚本目录已不存在，未伪造重建。

5. 已新建并保留独立迁移工作树，原始分支及共享 main 工作树均未修改。换机后从报告中的准确 salvage ref 重建工作树；全部四个独立原始 tip 均须保留。

6. GitHub 远端 ref 与完整 SHA 已逐条回读核对。原物、分支和工作树保留；没有 force、reset、clean、删除或服务器访问。未启动新功能或 retired gate。令牌已释放，停止。

Worktree: /Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-sci/worktree
Branch: salvage/mac/sci/audit58-integration-d7dfcfa3a158
HEAD: d7dfcfa3a158003959b58ef72b73a2556b9298ce

## Verified GitHub references

| Remote ref | Full SHA |
| --- | --- |
| refs/heads/main | 5790b8961955400ffe25f10ced170bf4f669683c |
| refs/heads/salvage/mac/sci/audit58-admission-151df7a5e05f | 151df7a5e05f82da01aeb0c3a2221154cccd6062 |
| refs/heads/salvage/mac/sci/audit58-catalog-2043f9e02410 | 2043f9e02410a1a208268b7cf188344511797738 |
| refs/heads/salvage/mac/sci/audit58-integration-d7dfcfa3a158 | d7dfcfa3a158003959b58ef72b73a2556b9298ce |
| refs/heads/salvage/mac/sci/audit58-preserved-213b6432-213b64327ea3 | 213b64327ea35cf57e4cdc201119a9a69058c9cc |

## Source and validation record

Source manifest: 48 original file versions across 12 paths and four independent tips. New non-Git source files: 0.
Complete make check/build: NOTRUN. Raw free 1303035904 bytes; required 6442450944 bytes.
Historical qualification is retained only at its recorded scope. No new product acceptance is claimed.
Authoritative remote verification: 2026-11-14T13:51:02+08:00
Archive manifest: /Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-sci/archive-manifest.json
Admission: /Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-sci/admission.json
Commands: /Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-sci/commands-publication.json
Receipt: /Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-sci/final-receipt.json

## Recreate after migration

Fetch the exact selected remote ref in a new clone and create a new worktree. Preserve all four independent tips.

```sh
git fetch https://github.com/Passion-Flow/ownsolid.git refs/heads/salvage/mac/sci/audit58-integration-d7dfcfa3a158
git worktree add --detach <new-worktree-path> FETCH_HEAD
```

Every observed exclusion and the original-to-remote mapping is recorded in archive-manifest.json and final-receipt.json.
