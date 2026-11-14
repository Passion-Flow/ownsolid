# ARC Mac 迁移回执

1. 提交：仅提交指定三份文档，提交为 `0cc4a6cfc0d175f9f515a09a0184221bd72e00c7`。原始 `aaf1f65a94736b441323c3e18819e5cbc69d34ac` 独立保全，NOTRUN 内容保持真实，没有新增 roadmap 勾选。

2. 变基与验证：fetch GitHub main 成功，准确远端 SHA 为 `5790b8961955400ffe25f10ced170bf4f669683c`。新鲜 raw Free 为 1,651,965,952 字节，低于 6,442,450,944 字节，完整 gate 真实 NOTRUN。O_NOFOLLOW/fstat 身份核对通过，FD 已关闭，NB 零次，没有构建或测试子进程。按本轮准入拒绝直接 salvage；rebase、make check、make build、make test 均未运行，退出码为 null，不能算通过。

3. 冲突与历史：未启动变基，没有冲突；未改写历史，未 force/reset/clean，未删分支、工作树或原件。12 个原 ARC 工作树路径保持原样，仅指定树增加文档提交，其余 HEAD 不变且干净。main/shared checkout 和旧 ownsolid-lane-a-push 未改动。

4. 推送：main 推进 0 个提交，走 salvage。30 个 refs 一次原子推送，退出 0；回读退出 0，30/30 SHA 精确一致。包括24个原始 refs、新文档 tip、4个旧 bundle-only tip 和独立源码归档 tip。完整准确映射见下表和 final-receipt.json。

5. Roadmap 与半成品：本线已勾选 P1 为 ARC-101、ARC-102、ARC-103、ARC-106、ARC-107；P0 属既有基线。FLT-113、FLT-114 是既有 Web 线成果，不冒认为本轮完成。ARC-111 仍未勾选：真实 PG18.6/vector0.8.7 已完成 fresh61 和 restore55..61 共8份目录的元数据测量，但 schema61 候选 `549c3ad699debc2acd0c7b9754fd59d97788d74c` 未编译、未完成原始运行时资格验证。接手后的下一步是满足原完整资源门槛后，重建匹配候选的三个 Linux 包，恢复真实 host-TCP fixture，再运行原 Migrate/grant/Control-role/MODEL/cursor、安装及完整 gates。ARC-108、ARC-110 有保留候选但未完成全量验收；其他开放项未因本次迁移改勾。

6. 源码、坑与路径：862份 Git 外源码实例去重为474个可读源码文件，加3份说明与映射，共477个归档文件；独立提交 `38e9ffdb8e6af29dd1591cdc4d1c02e7a19a6276`。原型及旧私有包装器只作 salvage 资料，没有执行或计作验收。未发现归档源码内嵌二进制 bundle；外部86份载荷保留原件哈希及重建映射，Git bundle 未作为文件提交。归档原始源码的既有空白使 diff --check 返回2，源码字节未为消除提示而改写；文档 diff --check 为0。

已知坑：Docker 请求的 host-loopback PortBindings 曾出现实际 NetworkSettings.Ports=null，不能用 docker-exec 元数据路径冒充原 host-TCP 运行时 fixture；source-A 旧二进制和局部结果不能转为 source-B 的资格证明；旧 retired 包装器不能再执行；raw Free 不能用 available/cache 替代。

工作树主路径：`/Users/passion./Documents/Passion's WorkSpace/Development/lane-arc111-qualified-readers`。新增归档树：`/Users/passion./Documents/Passion's WorkSpace/Development/lane-arc-source-archive-20261114`。12个原 ARC 工作树的完整路径、HEAD 与分支映射见 final-receipt.json。换机后按对应远端 salvage ref/准确 SHA 重建工作树，不运行旧包装器。服务器仓库、服务和 ARC 资源均未触碰。

排除：第三方依赖、缓存、模型/数据库、编译件、Git bundle/外部传输载荷、凭据、运行输出、重复 Git 源码，以及非本线 stash/tags；全部133243条排除记录保存在 final-receipt.json，原物未删。118284项重复源码由已保全原始 tip 的可达 Git blob 覆盖；4个额外 bundle tip 已独立推送。全局4171个原始提交及其他 reflog-only DAG 由协调器统一保全，本轮没有重复导出。

Manifest：`/Users/passion./Documents/Passion's WorkSpace/Development/lane-arc-source-archive-20261114/tools/dev/arc-migration-salvage/source-manifest.json`。外部载荷重建映射：`/Users/passion./Documents/Passion's WorkSpace/Development/lane-arc-source-archive-20261114/tools/dev/arc-migration-salvage/external-payload-reconstruction.json`。完整机器回执：`/Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-arc/final-receipt.json`。推送及回读原始输出：push-atomic.log、remote-final.log。最初报告模板编码解析退出1发生在任何脚本语句执行前，没有远端写入；修正模板后执行本次原子推送。所有实际命令退出码及 NOTRUN 原因均保留。

| GitHub ref | Accurate SHA |
| --- | --- |
| `refs/heads/salvage/mac/arc/codex-arc107-before-schema60-rebase-1e687ed99bc0` | `1e687ed99bc0727016c15c616d95124e9e4a212c` |
| `refs/heads/salvage/mac/arc/codex-arc107-full-qualified-129573ae-129573ae8977` | `129573ae8977cce83c1e81d9d386e45c1a176c3b` |
| `refs/heads/salvage/mac/arc/codex-arc107-full-qualified-59c9c86a-59c9c86a432c` | `59c9c86a432c8623ec6b68595bf575920ff89056` |
| `refs/heads/salvage/mac/arc/codex-arc107-installed-busy-149a9501-149a95013db6` | `149a95013db6c6d1ca9dbadd09b05fff9b4c98de` |
| `refs/heads/salvage/mac/arc/codex-arc107-qualified-eea738bf-eea738bfc11d` | `eea738bfc11d74f8505eee41c3a3ce0d9fa5cf7b` |
| `refs/heads/salvage/mac/arc/codex-arc108-observation-qualified-30cc67d6-30cc67d6e6c5` | `30cc67d6e6c546e05cf268de1aba7d96d8c3823b` |
| `refs/heads/salvage/mac/arc/codex-arc108-qualified-a91a817b-a91a817b2315` | `a91a817b23157fd610f714533b04cf6e7d8ec012` |
| `refs/heads/salvage/mac/arc/codex-gw-published-f7842492-f78424921d99` | `f78424921d99c284daa42da5f461ecffc9fb8ead` |
| `refs/heads/salvage/mac/arc/heads-codex-arc104-signed-registry-consumer-81701dba16e2` | `81701dba16e2e61e4c032fec3b47d11161ecdfe7` |
| `refs/heads/salvage/mac/arc/heads-codex-arc107-handoff-6f785818156f` | `6f785818156ff5e827ccc86923b7cfa0b4c0185d` |
| `refs/heads/salvage/mac/arc/heads-codex-arc107-production-lifecycle-e2fde0f5fc8f` | `e2fde0f5fc8faa04f63f37ba18441b163ad58f53` |
| `refs/heads/salvage/mac/arc/heads-codex-arc107-resume-22a39595-22a395952291` | `22a3959522913bdc7c189fb78f8d88910b0b4236` |
| `refs/heads/salvage/mac/arc/heads-codex-arc108-inference-heartbeat-cost-d3528d5f6b66` | `d3528d5f6b660eb1ddd23324a96cdbb164e09a17` |
| `refs/heads/salvage/mac/arc/heads-codex-arc108-operation-cost-1d3ac8384fcb` | `1d3ac8384fcbe6d87974b983d601818cce8f205f` |
| `refs/heads/salvage/mac/arc/heads-codex-arc110-entrance-listeners-bc2d12cf9572` | `bc2d12cf95726982f2feb872581be34236da37ee` |
| `refs/heads/salvage/mac/arc/heads-codex-arc111-database-role-baa584c39ff0` | `baa584c39ff0d3039f0b463e4415bb71bef63eaa` |
| `refs/heads/salvage/mac/arc/heads-codex-arc111-joint-catalog-9c8fa041f74c` | `9c8fa041f74c3030540f3f7a97f1958f04c879af` |
| `refs/heads/salvage/mac/arc/heads-codex-arc111-measured61-pins-1a7ad692-549c3ad699de` | `549c3ad699debc2acd0c7b9754fd59d97788d74c` |
| `refs/heads/salvage/mac/arc/heads-codex-arc111-qualified-readers-aaf1f65a9473` | `aaf1f65a94736b441323c3e18819e5cbc69d34ac` |
| `refs/heads/salvage/mac/arc/heads-codex-arc111-reader-lifecycle-8e9a6c4ab6fa` | `8e9a6c4ab6fa144edfd935f2b796e232ef7bc8e7` |
| `refs/heads/salvage/mac/arc/heads-codex-dat101-schema60-integration-f78424921d99` | `f78424921d99c284daa42da5f461ecffc9fb8ead` |
| `refs/heads/salvage/mac/arc/heads-codex-gw120-schema59-integration-4f76102ca993` | `4f76102ca99328e0b9063eb05b0352e62501e154` |
| `refs/heads/salvage/mac/arc/heads-codex-net102-startup-qualification-2025a1c877b9` | `2025a1c877b9d1165739c25da84ff534a9b83c41` |
| `refs/heads/salvage/mac/arc/heads-lane-arc-0f55a770aa24` | `0f55a770aa242a36d53fafde15f2cc8172380b3c` |
| `refs/heads/salvage/mac/arc/qualified-readers-docs-0cc4a6cfc0d1` | `0cc4a6cfc0d175f9f515a09a0184221bd72e00c7` |
| `refs/heads/salvage/mac/arc/bundle-net102-625dbdd8-625dbdd87d53` | `625dbdd87d536f8519f23b00d647c5438bc6837a` |
| `refs/heads/salvage/mac/arc/bundle-dat101-fa82645c-fa82645ca498` | `fa82645ca49811521872df90450dad858816b71f` |
| `refs/heads/salvage/mac/arc/bundle-dat101-schema60-f1107579-minimal-f110757995e0` | `f110757995e081544b836ec240474a700176571e` |
| `refs/heads/salvage/mac/arc/bundle-gw120-12520508-12520508998c` | `12520508998c0cf8d1e7eb3e606d642bdbb37794` |
| `refs/heads/salvage/mac/arc/private-source-archive-38e9ffdb8e6a` | `38e9ffdb8e6af29dd1591cdc4d1c02e7a19a6276` |

迁移令牌已释放。本线停止，不接力其他车道，不再追加测试、归档或研发。
