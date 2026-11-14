Mac GW 换机保全结果

1. main 推进与保全
本次推进 main 0 个提交，采用 salvage。已将 2 个指定原始分支 tip 和 19 个自有 custom refs，以原始 SHA 保存到 21 个独立远端分支；完整映射如下。
| Original ref | GitHub ref | Exact SHA |
|---|---|---|
| refs/codex/gateway-docs-2a327420 | refs/heads/salvage/mac/gw/gateway-docs-2a327420-2a327420a3fc | 2a327420a3fc03c1ad6bbe4bb32f312abf818fca |
| refs/codex/gw-docs-b2068c3e | refs/heads/salvage/mac/gw/gw-docs-b2068c3e-b2068c3edb2c | b2068c3edb2c6962563c698312680b1adade6563 |
| refs/codex/gw-main-4c8a8289 | refs/heads/salvage/mac/gw/gw-main-4c8a8289-4c8a8289cf1b | 4c8a8289cf1b8ae8bc72b9f5b6faa4a422cf8931 |
| refs/codex/gw-published-8123466c | refs/heads/salvage/mac/gw/gw-published-8123466c-8123466c5a64 | 8123466c5a64b6424047f126815ae63100383bc6 |
| refs/codex/gw-published-be62f58e | refs/heads/salvage/mac/gw/gw-published-be62f58e-be62f58e846d | be62f58e846dcc654dafee65cba6cc7480890570 |
| refs/codex/gw-published-f7842492 | refs/heads/salvage/mac/gw/gw-published-f7842492-f78424921d99 | f78424921d99c284daa42da5f461ecffc9fb8ead |
| refs/codex/gw104-main4c8-prepared | refs/heads/salvage/mac/gw/gw104-main4c8-prepared-1e4035b1fe60 | 1e4035b1fe60de6a377d5cba8251349a5c16c504 |
| refs/codex/gw104-native-preflight-214ab9cc | refs/heads/salvage/mac/gw/gw104-native-preflight-214ab9cc-214ab9ccd2c4 | 214ab9ccd2c47570f161227b2557a2db18e2e9e9 |
| refs/codex/gw104-native-qualification-c5f797d5 | refs/heads/salvage/mac/gw/gw104-native-qualification-c5f797d5-c5f797d57d65 | c5f797d57d6567a16235ccc3a0993bb987805576 |
| refs/codex/gw104-qualification-c4fd65c9 | refs/heads/salvage/mac/gw/gw104-qualification-c4fd65c9-c4fd65c96b35 | c4fd65c96b35d94e52166598346b78c7d730815d |
| refs/codex/gw104-read-219af27e | refs/heads/salvage/mac/gw/gw104-read-219af27e-219af27e3d9f | 219af27e3d9f73c78acc1b8ab8324e086a6a2f62 |
| refs/codex/gw104-read-54d4ac26 | refs/heads/salvage/mac/gw/gw104-read-54d4ac26-54d4ac266ed8 | 54d4ac266ed8ce7fca9b4ac8776f3bc7e1565fad |
| refs/codex/gw104-scram-plan-454da1c7 | refs/heads/salvage/mac/gw/gw104-scram-plan-454da1c7-454da1c79954 | 454da1c799541e53d3602a18cd8cd709adaba4aa |
| refs/codex/gw104-wire-plan-819fc7a8 | refs/heads/salvage/mac/gw/gw104-wire-plan-819fc7a8-819fc7a899cc | 819fc7a899ccb98498348be0f4415d379e6c2a8a |
| refs/codex/gw120-evidence-0ea888ec | refs/heads/salvage/mac/gw/gw120-evidence-0ea888ec-0ea888ec619e | 0ea888ec619eda15b182216f1ef9b5fd34a999a6 |
| refs/codex/gw120-evidence-168646b8 | refs/heads/salvage/mac/gw/gw120-evidence-168646b8-168646b81c57 | 168646b81c57bcc563373c2f74e6dfd6f6f400f4 |
| refs/codex/gw120-evidence-36f66734 | refs/heads/salvage/mac/gw/gw120-evidence-36f66734-36f66734fd46 | 36f66734fd465fa542c4f2099fd810ddb15a308e |
| refs/codex/gw120-evidence-4526bca6 | refs/heads/salvage/mac/gw/gw120-evidence-4526bca6-4526bca68a8d | 4526bca68a8d40dcba32300b0071c7a63b58f6f9 |
| refs/codex/gw120-main4c8-prepared | refs/heads/salvage/mac/gw/gw120-main4c8-prepared-d7a2d7aad5e8 | d7a2d7aad5e8d2df6e03f24c0689bd31e66074bd |
| refs/heads/codex/gw120-before-final-rebase-12520508 | refs/heads/salvage/mac/gw/codex-gw120-before-final-rebase-12520508-12520508998c | 12520508998c0cf8d1e7eb3e606d642bdbb37794 |
| refs/heads/codex/gw120-qualified-a446ec58 | refs/heads/salvage/mac/gw/codex-gw120-qualified-a446ec58-a446ec580cf6 | a446ec580cf6dffc63df5d8269518f1ef8175d40 |

2. 已勾选 roadmap
冻结候选中的已勾选条目为 GW-113、GW-114、GW-117，属于既有完成记录。本次迁移新增完成 0 条，不把未验收候选写成完成。

3. 未完成工作
GW-104 停在只读能力候选及原始范围的 PostgreSQL 验证；生产发布仍关闭，完整候选、clippy、当前主线、typed startup/公开契约/客户端/Help 和安装链仍需后续验收。GW-120 保留已有证据，安装态及完整完成条件仍未满足；GW-123 非 inference 自动交付仍未完成。本轮停止功能开发。

4. 必须保留的范围说明
219af27e 的四项真实 PG 根仅保留原始 darwin/arm64/operator fixture 范围，不证明当前候选通过。旧 Mac 临时目录已不存在，1e4035b1 的 Git 提交由 custom ref 保留并已远端保全。旧 retired gates 不得重放。Linux 三个 GW 工作树、15 custom refs、42 份历史 bundles 由 CPU 阶段处理，本轮未连接服务器。共有/reflog-only 历史由协调器统一保全。

5. 工作树与源码归档
迁移工作树：/Users/passion./Documents/Passion's WorkSpace/Development/lane-gw-migration-20261114t071350z
分支：salvage/mac/gw/gw104-main4c8-prepared-1e4035b1fe60
HEAD：1e4035b1fe60de6a377d5cba8251349a5c16c504
原始 tree：0d7d3afde4fb0748f0a834b27924b57d51672af4
工作树保留，未重写原始提交。明确自管的现存 Mac Git 外源码、必要配置、bundles 及内嵌 bundle 均为 0，故源码归档文件数为 0；已在 Git 中的源码没有重复拷贝。排除范围、数量和缺失路径见 receipt，不删除原物。

6. 命令、准入及停止
远端查询、fetch、21 个原始保全 ref 创建、worktree add、atomic push 与远端 SHA 回读均记录真实退出码。
atomic push exit=0; exact remote readback exit=0; matching refs=21/21.
raw Free 为 4,764,811,264 字节，门槛为 6,442,450,944 字节。make check/build、clippy、rebase 均未运行；不打开门禁、不等待、不转服务器、不推进 main。未提交源码为 0，因此没有创建源码提交。没有发现或解决变基冲突。
Receipt: /Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-gw/final-receipt.json
Manifest: /Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-gw/preservation-manifest.json
Report: /Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-gw/report.md
迁移串行令牌已归还，本线停止。
