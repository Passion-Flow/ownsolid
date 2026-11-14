# ownsolid 换机交接清单

本机与 ownsolid-cpu 的现存原始 Git 工作已保全到 GitHub。独立 GitHub 克隆核验 5,008 个原始提交无遗漏，git fsck --full --strict 通过。未删除原有分支、工作树或原始文件，未强推。代码保全与合入、端到端验收、路线图勾选分别记录。

本轮 main 推进 0 个提交。13 条车道的未合入成果均保存于 salvage；原始提交和中间历史都保留。各车道候选没有获得当前 main 上的完整验收，后续按依赖继续整合。

## 1. Roadmap

89 / 589; P0 28/28, P1 61/326, P2 0/171, P3 0/18, P4 0/46. New ticks: 0.

## 2. main

`5790b8961955400ffe25f10ced170bf4f669683c` on ownsolid-cpu.

| Command | Exit |
| --- | --- |
| `make check` | 0 |
| `make build` | 0 |
| `make test` | 2 |

main 的 make check、make build 通过；make test 未通过，在浏览器测试阶段停止，不能宣称主线验收全绿。相关失败日志和测试环境已保留。迁移未修改产品实现来处理这些失败。

`make test` stopped at `test-web-browser`; later Go/Cargo/web-unit stages were not reached. Full logs and tool/environment details are in [verification](verification) and [health.json](health.json).

## 3. Lanes

| Lane | Route | Next work |
| --- | --- | --- |
| [ARC](reports/ARC.md) | salvage; main +0 | 先核对真实 control startup/config、数据库角色与 actual migrate；旧 PG 历史资格不能代替当前启动验收。 |
| [DEL](reports/DEL.md) | salvage; main +0 | 在最新 main 完成安装、CLI、TLS、离线出口及取消流程的整体复验；原始 e3aff53e 与未验收重放 305cc970 均保留。 |
| [DEV](reports/DEV.md) | salvage; main +0 | 从 b57e975b 恢复严格 JSON/API 兼容工作；旧独立仓库 origin 曾指向本机路径，新机须使用 GitHub origin；GPU 环境另行准备。 |
| [WEB](reports/WEB.md) | salvage; main +0 | 继续 UX-127 / POR-112，补齐 installed authority、SQL、离线及 SSO 联验；旧短路径和退役门禁不能直接复用。 |
| [OPS](reports/OPS.md) | salvage; main +0 | OPS-104 尚缺持久游标和实时流；CAT 尚缺持久安装状态、授权 UI 与应用自身的离线 Helm 验收。 |
| [IAM](reports/IAM.md) | salvage; main +0 | 恢复各候选分支与 3 个 stash；SAML、CSRF、SSO、启动角色与资格需按最新依赖复验；3 个审查树已单独保全。 |
| [APP](reports/APP.md) | salvage; main +0 | AGT-101 的正式入口、角色、生产迁移接入与离线 UI 验收仍开放；AGT-111 仅组件资格；模型安全案例未定级。 |
| [SCI](reports/SCI.md) | salvage; main +0 | EVL 的持久化、正式路由及科研计算/审计的当前受权链仍需接入；服务器配对源码与 GPU 项保持各自证据范围。 |
| [MODEL](reports/MODEL.md) | salvage; main +0 | formal61 的 7 个 SQL 根、11 维 cursor、startup 配置尚待验证；MOD-113 停在签名索引研究。服务器 7 份非 Git 快照已收录。 |
| [OBS](reports/OBS.md) | salvage; main +0 | 继续 26 个 Go 根与当前 startup / installed 链、API / Console 及摄入限额验收；旧 schema59 证据不能迁移为新资格。 |
| [GW](reports/GW.md) | salvage; main +0 | 复验当前 whole / clippy / main replay 与 installed 路径；旧 219af27e 等结果只适用于原源码范围。 |
| [TRN](reports/TRN.md) | salvage; main +0 | startup-role、只读挂载与 drain 仍需验证；旧 e639db90 native 结果不覆盖这些项目。独立 source-only tree 已保全。 |
| [SCH](reports/SCH.md) | salvage; main +0 | dispatcher、role、UI/config 与调度/计量闭环仍开放；Mac 缺失旧工作树登记保留，按存续 HEAD 与服务器来源重建。 |

## 4. Branches and preserved states

- [All source heads and tags](github-final-refs.tsv): 1,213 source branches and 9 tags. This delivery adds the branch `salvage/migration-handoff-20261114` (1,214 total branches).
- [Original references/worktree paths](original-refs-and-worktrees.json), [lane receipts](lanes.json), [preservation proof](final-preservation-proof.json).
- CPU private source archive: [`63a32096f9e714e72354504aef2f31fee9dbef6d`](https://github.com/Passion-Flow/ownsolid/tree/63a32096f9e714e72354504aef2f31fee9dbef6d): 208,885 source/configuration instances, 23,987 unique blobs, 209 manifest parts.
- Mac source drafts outside commit history: [`c0aeea6401cd72bd1bc742a3b79a33364562d65e`](https://github.com/Passion-Flow/ownsolid/tree/c0aeea6401cd72bd1bc742a3b79a33364562d65e): 156 original draft blobs.
- 3 IAM stash commits, 9 evidence tags and 4 source-only tree snapshots are preserved in the Mac lane references.

## 5. Recovery

```sh
git clone https://github.com/Passion-Flow/ownsolid.git
cd ownsolid
git worktree add ../ownsolid-handoff --detach origin/salvage/migration-handoff-20261114
```

Read `AGENTS.md`, `docs/product/roadmap/index.md` and `docs/development/environment.md` on main. Install the pinned toolchains; run `make web-install` before checks that import web dependencies. Configure GitHub and SSH/Tailscale privately. Use a short, real TMPDIR for Chromium/Unix sockets and install the complete Playwright Chromium distribution with its libraries.

Rebuild isolated worktrees at the original HEADs listed in the reference mapping. Then reconcile each candidate with current main and verify it. The next integration step includes reproducing and resolving the browser-test failures recorded here. Retired gate wrappers remain archival.

List private source roots or preview a selection from the product clone:

```sh
python3 ../ownsolid-handoff/restore_sources.py
python3 ../ownsolid-handoff/restore_sources.py --prefix /root/ownsolid-lane-m-family4.UVeXu9hB/source/
```

Add `--destination ../recovered-source` to restore into a new directory. The helper checks SHA-256 and refuses to overwrite an existing destination. A two-file recovery smoke check passed; see [evidence](source-restoration-smoke.json).

## 6. Limits

- 部分旧工作树目录在本次盘点前已经缺失。它们存续的 HEAD/index 状态已保留，无法证明已不存在目录里是否曾有未暂存文件。
- 编译产物、第三方依赖缓存、模型、数据集、数据库与凭据不作为源码归档；新机需重新配置 GitHub、SSH/Tailscale 与私密环境文件。
- CPU 归档扫描中的两个解析异常已核实为零字节旧归档和 163 字节 AppleDouble 旁注，不含源码载荷。
- 旧门禁绑定旧路径、inode、端口及源码版本，应视为归档，禁止照搬执行。
- 相对 origin/main 仍领先代表尚未合入；独立克隆的可达性核验确认其已在 GitHub 保全。
- 本次没有新增路线图勾选，也没有补做硬件、客户环境或完整安装的独立验收。

## 7. Scheduled tasks

```json
{
  "status": "verified PAUSED",
  "time": "2026-11-14T09:12:43.085477+00:00",
  "automations": [
    {
      "id": "ownsolid",
      "name": "ownsolid 双机资源巡检",
      "status": "PAUSED",
      "target_thread_id": "01a1bc59-c46b-7723-b081-2f1bb324ce59",
      "updated_at": 1794647563000
    },
    {
      "id": "ownsolid-2",
      "name": "ownsolid 开发巡检",
      "status": "PAUSED",
      "target_thread_id": "01a1bc5d-f337-7073-93cd-58c7f96bf328",
      "updated_at": 1794647563035
    }
  ],
  "scope": "Only these two ownsolid app automations. No Claude Guard or other project automation was operated on."
}
```

Only `ownsolid` and `ownsolid-2` are in scope. No Claude Guard or unrelated project automation was operated on.
