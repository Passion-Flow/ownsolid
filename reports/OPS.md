# OPS Mac migration report

已完成 OPS 本机工作保全；8 个 salvage refs 一次原子推送成功，逐一回读 GitHub SHA 全部一致。迁移令牌已释放，已停止；未操作服务器。

## Main and salvage

本轮推进 main 0 个提交，走 salvage。main 保持 5790b8961955400ffe25f10ced170bf4f669683c。原有两个工作区干净，没有待提交源码。新建私人源码归档提交为 16df6ae114fbdda928161b7971212945e65d8d89。原始分支、提交、工作树、脚本和日志全部保留，未 reset、clean、删除或 force push。

## Roadmap

OPS-101–114 与 CAT-101–108 没有已打勾条目，本轮新增勾选 0。此前实现的是 OPS-104 有界快照，以及 CAT-101/102/105/107 的签名元数据、精确 pin、依赖绑定、版本图与 Compose 镜像计划等组成部分，不能等同完整用户路径完成。

## Unfinished work and next machine

OPS-104 停在快照读取，尚缺任务生产者提交后持久游标、经授权可恢复实时流和 Console 接入。CAT-101/102/105/107 停在校验与执行适配边界，尚缺持久安装状态、历史/幂等/恢复/保留数据决策及服务端授权 UI 路由。CAT-103/104/108 还需安装级网关密钥、OIDC 客户端生命周期、可见性与原子配额预留。CAT-106 需可靠安装/工作负载身份再接健康、日志、终端；其余 OPS 主机管理用户路径也未完成。新机器先按精确映射恢复原始分支，比较当前 main，完成完整构建，然后按交接研究接入这些拥有者。应用 Helm 验收需生产者批准的离线应用包、独立公钥信任及 chart/image 许可，并由 DEL 消费应用自身的 pin/chart/OCI 清单、离线依赖与 schema、目标节点镜像映射。

## Known traps

旧门禁已封闭；.recorded 文件包含过期目录锁和 O_CREAT 协议，绝不能执行或恢复。当前持久锁不得重建。本轮原始 raw Free 只有约 1.39 GiB，低于 6 GiB，所以 rebase、make check/build/test 均为 NOTRUN，没有冲突尝试或 main 推送。历史根 Makefile 漏了 catalog，需另外 build/race/vet；HFS+ 曾导致还原内容的变更时间检查失败，过长 APFS 挂载路径曾令 Unix socket 绑定失败，应使用短且规范的 APFS 路径。平台 Helm 测试不能替代应用 Helm 验收。原生 Linux 旧临时日志在重启后丢失，不能声称本轮复验。仅 reflog 的共有历史由协调器统一保全，重建来源映射已提供。

## Source preservation

归档 31 份逐字节保留的源码/配置/说明，35,303 字节，另加 3 份交接文件，共 34 个新增文件。排除但保留原地 106 份临时输出/元数据；两个工作区另有 58,268 个忽略的依赖、缓存和构建文件，按 31 个路径组列入排除清单。9,699 个非依赖/非构建文件的扫描未发现自有 Git bundle，31 份私人源码未发现内嵌二进制 bundle，无需导入 bundle-only head。未将二进制、依赖、模型、数据库或凭据纳入归档。

## Verification

迁移实际命令退出码：GitHub ls-remote 0、fetch 0；源码哈希检查 0；明确路径暂存 0；git diff --cached --check 0；归档提交 0；唯一引用创建 0；推送前原始状态检查 0；原子 push 0；远端回读 0。历史完整 make check/build/test 及 catalog build/race/vet、选定断网测试通过的精确提交是 67293e12e3fd47cbb5b07c32fcc4ce3a596b02aa，仅引用既有实测，不作本轮复验。

## Recreate worktrees

- `/Users/passion./Documents/Passion's WorkSpace/Development/lane-ops-cat105`
  - `refs/heads/codex/ops-compose-image-plan` at `68fc5e02f6110e2d9681a3dd3c306f7cd3881bdc`

- `/Users/passion./Documents/Passion's WorkSpace/Development/lane-ops`
  - `refs/heads/lane-ops` at `828eada8bc3b5f2ff45d70c85fe7bd60186dfed2`

- `/Users/passion./Documents/Passion's WorkSpace/Development/lane-ops-source-archive-20261114`
  - `refs/heads/codex/ops-migration-source-archive-20261114` at `16df6ae114fbdda928161b7971212945e65d8d89`

## Exact GitHub refs

| Remote ref | SHA |

| --- | --- |

| `refs/heads/main` | `5790b8961955400ffe25f10ced170bf4f669683c` |

| `refs/heads/salvage/mac/ops/ops-bindings-retained-1c1d05bf078d` | `1c1d05bf078d4db8c615fdc9fb07a78eee3fe30d` |

| `refs/heads/salvage/mac/ops/ops-compose-handoff-retained-b390fe5e4022` | `b390fe5e4022fcf90d4390c13a6228860fb5100a` |

| `refs/heads/salvage/mac/ops/ops-compose-input-retained-731ab7c95996` | `731ab7c9599696527b3e327ab5b3ba27cde80e07` |

| `refs/heads/salvage/mac/ops/ops-compose-joint-retained-35bee4d506af` | `35bee4d506af660ee51c94f107c835b3b02c0e02` |

| `refs/heads/salvage/mac/ops/ops-compose-retained-399d800716c2` | `399d800716c2c8d318c84c785d70af242e61e48f` |

| `refs/heads/salvage/mac/ops/ops-preserved-20261112-53198de800a9` | `53198de800a9c7fa1e69158c4da7c7b7def6ccac` |

| `refs/heads/salvage/mac/ops/lane-ops-828eada8bc3b` | `828eada8bc3b5f2ff45d70c85fe7bd60186dfed2` |

| `refs/heads/salvage/mac/ops/private-source-archive-16df6ae114fb` | `16df6ae114fbdda928161b7971212945e65d8d89` |

## Original name coverage

| Original ref | Original SHA | Remote coverage |

| --- | --- | --- |

| `refs/heads/codex/ops-bindings-retained` | `1c1d05bf078d4db8c615fdc9fb07a78eee3fe30d` | `refs/heads/salvage/mac/ops/ops-bindings-retained-1c1d05bf078d` (exact-original-sha) |

| `refs/heads/codex/ops-compose-handoff-retained` | `b390fe5e4022fcf90d4390c13a6228860fb5100a` | `refs/heads/salvage/mac/ops/ops-compose-handoff-retained-b390fe5e4022` (exact-original-sha) |

| `refs/heads/codex/ops-compose-image-plan` | `68fc5e02f6110e2d9681a3dd3c306f7cd3881bdc` | `refs/heads/main` (ancestor) |

| `refs/heads/codex/ops-compose-input-retained` | `731ab7c9599696527b3e327ab5b3ba27cde80e07` | `refs/heads/salvage/mac/ops/ops-compose-input-retained-731ab7c95996` (exact-original-sha) |

| `refs/heads/codex/ops-compose-joint-retained` | `35bee4d506af660ee51c94f107c835b3b02c0e02` | `refs/heads/salvage/mac/ops/ops-compose-joint-retained-35bee4d506af` (exact-original-sha) |

| `refs/heads/codex/ops-compose-retained` | `399d800716c2c8d318c84c785d70af242e61e48f` | `refs/heads/salvage/mac/ops/ops-compose-retained-399d800716c2` (exact-original-sha) |

| `refs/heads/codex/ops-preserved-20261112` | `53198de800a9c7fa1e69158c4da7c7b7def6ccac` | `refs/heads/salvage/mac/ops/ops-preserved-20261112-53198de800a9` (exact-original-sha) |

| `refs/heads/lane-ops` | `828eada8bc3b5f2ff45d70c85fe7bd60186dfed2` | `refs/heads/salvage/mac/ops/lane-ops-828eada8bc3b` (exact-original-sha) |

## Local receipts

- `/Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-ops/final-receipt.json`

- `/Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-ops/migration-manifest.json`

- `/Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-ops/historical-tip-source-mapping.json`

- `/Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-ops/existing-evidence-mapping.json`

- `/Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-ops/private-source-exclusions.json`

- `/Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-ops/ignored-worktree-exclusions.json`

## Stop

远端核对后仅生成最终回执与本报告，迁移令牌已释放。原物全部保留，现已停止，不再开发或启动测试。
