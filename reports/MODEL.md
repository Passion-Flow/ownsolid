# Mac MODEL 迁移最终报告

1. main 推进 **0** 个提交；采用 **4 个 salvage 分支**，一次 atomic push 成功并已向 GitHub 核对原始 SHA。没有变基、解决冲突或改写原始提交。lane-m@dba2eaf6 已由 GitHub main 覆盖。

2. 关联路线图当前已打勾 **MOD-119、MOD-120、SRV-111**；本轮新增打勾 **0**。页面条目的既有证据归原页面 owner，本次只做保全。

3. 未完成停点：db843503 的 formal61 七个 SQL 根及十一维 cursor 仍未执行，等待 ARC 实际 schema61 目录指纹与真实受限 Control 角色资格；startup expiry 配置 e2f424bd 另行保留，未重放或验证。MOD-113 是签名索引交接研究，信任根、交付与 API/UI 消费者仍待完成。没有继续 role/SQL/UI 实现。

4. 迁移陷阱：2783a46f/dba2eaf6 的旧门禁证据不能用于新组合；历史无 ref 原始提交由协调器全局保全。现有 bundle 的 HEAD 与 db843503 相同，原 bundle 保留且未作为文件提交。旧 retired gates 禁止执行。归档 patch 的原始上下文空白使归档 diff --check 返回 2，保留原字节，没有将其报告为产品检查通过。

5. 原工作树：/Users/passion./Documents/Passion's WorkSpace/Development/lane-m，branch codex/lane-m-family61，HEAD db8435034a2a94a3285a059f6bd40237322ae1c2，干净。独立归档工作树：/Users/passion./Documents/Passion's WorkSpace/Development/lane-m-migration-iccdlyfb，branch salvage/mac/model/private-source-db8435034a2a，HEAD 93364a548ee9b7228b9bc7de34537be6d935d90a，干净。两处均保留，换机后需按远端 salvage refs 重建。

6. 验证与排除：新鲜 raw Free 为 176422912 bytes，低于 6442450944 bytes，构建准入未尝试；rebase、make check、make build、make test 均 NOTRUN，没有等待、重建锁或转服务器。归档 83 份源码/映射输入，连同 README 和 manifest 共 85 文件；排除 272 项，逐项路径和原因见 final-receipt.json。排除模型、数据集、DB、依赖、二进制、编译件、凭据、Git bundle 文件及临时输出，原物均保留。服务器七份非 Git source 快照留 CPU 阶段，本轮未触碰，也不声称已由 Mac 覆盖。

远端分支与准确 SHA：

| Remote ref | SHA |
| --- | --- |
| refs/heads/salvage/mac/model/lane-m-family61-db8435034a2a | db8435034a2a94a3285a059f6bd40237322ae1c2 |
| refs/heads/salvage/mac/model/lane-m-mod113-d839b4cc1d6a | d839b4cc1d6a2e5e3945eda3c9ca52ca45e52ca5 |
| refs/heads/salvage/mac/model/model-import-expiry-config-e2f424bdc7c5 | e2f424bdc7c5cfc8ddcaea36cf3f51fc027fd676 |
| refs/heads/salvage/mac/model/private-source-db8435034a2a | 93364a548ee9b7228b9bc7de34537be6d935d90a |

已存在 main：5790b8961955400ffe25f10ced170bf4f669683c

manifest：/Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-model/source-manifest.json
机器回执：/Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-model/final-receipt.json

本轮远端读取、fetch、源码归档 commit、atomic push 与最终 ls-remote 均退出 0；唯一静态非零为上述归档 patch whitespace review 2。完整命令结果见机器回执和同目录日志。

令牌已释放。迁移任务结束，不再开始测试、归档或开发。
