本轮推进 main 0 个提交，走 salvage。一次原子推送保存 18 个原始 DAG tip 和 1 个独立源码归档分支，远端 SHA 已逐一回读核对；没有新增功能、截图或 roadmap 勾选。

既有账本已勾选：UX-001–005、UX-101–105、UX-107–116、UX-118–121、UX-123–126、UX-128–129；POR-106、POR-113。这是历史完成记录，本次迁移没有重新验收这些条目。

UX-127 和 POR-112 保持未完成。UX127 当前源码停在 fc8d26d3ec64793c29ed7121594400344613dbfa，已有定向测试和截图阅读记录；Portal 联调候选为 bfeea90cd5adb082b77047b0c97fcea7c7654c51，串行 lint 候选为 98cf81c8f3c2f82ec2967c017e266f9c345560c0。新机器下一步是按映射恢复源码，保留最新 main 的内容做整合，在完整准入满足后运行 make check/build，再完成 joint/full 与 installed authority、SQL、offline、SSO 验收。

不要把定向通过当成完整验收。56a19799、4ca31d94、f556daff 的实际源码已被保全 tip 覆盖；七个旧工作树、孤立 2b983dbf、六个 refs/archive/web-* 和两个 handoff refs 都有映射。退休门禁保持退休；归档脚本未执行。历史副本保留原始字节，diff --check 的 213 个空白问题已记录为退出码 2；源码归档不应直接合入产品 main。服务器未操作，共有 reflog-only 历史由协调器统一保全。

Worktrees retained:

- /Users/passion./Documents/Passion's WorkSpace/Development/lane-web | refs/heads/lane-web | 124520b94be27f673bdb32a3c5d825d1d337f75d
- /Users/passion./Documents/Passion's WorkSpace/Development/lane-web-acceptance-4463bbe6 | DETACHED | 4463bbe628f60486bbdb92c15f72311fb53d076a
- /Users/passion./Documents/Passion's WorkSpace/Development/lane-web-acceptance-a4f1120d | DETACHED | a4f1120d5d90a892cca21c991f7d68f0b13c500b
- /Users/passion./Documents/Passion's WorkSpace/Development/lane-web-acceptance-be62f58e | DETACHED | be62f58e846dcc654dafee65cba6cc7480890570
- /Users/passion./Documents/Passion's WorkSpace/Development/lane-web-acceptance-help-2b983dbf | DETACHED | 2b983dbf75feaf36f6ac3423f4d1b0a1ecf0fc52
- /Users/passion./Documents/Passion's WorkSpace/Development/lane-web-acceptance-help-44ab5229 | DETACHED | 44ab52293e516128ba2efc0edc2cb2053acc3462
- /Users/passion./Documents/Passion's WorkSpace/Development/lane-web-history | refs/heads/codex/web-resource-history-ui | fc8d26d3ec64793c29ed7121594400344613dbfa
- /Users/passion./Documents/Passion's WorkSpace/Development/lane-web-source-archive-20261114 | codex/web-migration-source-archive-20261114 | e143f2cd172005254c8db4525d81b98f1e0e0ad9

新鲜 raw Free 为 1,062,944,768 字节，低于 6 GiB；锁文件身份及磁盘检查通过。make check/build、rebase 均为 NOTRUN，没有启动构建或浏览器。1,008 份私人源码/配置/验收记录逐字节保全，其中源码 386 份，851 个不同内容；归档提交共 1,020 个文件。排除 3,210 个文件及 13 个依赖/缓存目录，清单保留准确路径和原因；截图、bundle、构建产物、临时输出原件不删。30 个 bundle 的 heads 均有可达证明，压缩包 70 个文本成员已有同哈希归档，源码未发现内嵌二进制 bundle。无二进制文件或高精度凭据特征命中。

Verified GitHub refs:

```text
refs/heads/salvage/mac/web/refs-archive-web-aud108-before-main-refresh-8afef0a56a65 8afef0a56a6503ffccd97d082dddb695aed19843
refs/heads/salvage/mac/web/refs-archive-web-aud108-producer-783567ea-783567ea4a2e 783567ea4a2e19f3b2f7ca065d47dfe59886633e
refs/heads/salvage/mac/web/refs-archive-web-aud108-recovery-before-rebase-7aa8b068a24b 7aa8b068a24bf18db3a6fe8fb57e8a0aecd22857
refs/heads/salvage/mac/web/refs-archive-web-consumers-7401-qualified-7401ff675b32 7401ff675b3259e018e3ba148ed63ae2331b11bd
refs/heads/salvage/mac/web/refs-archive-web-consumers-final-qualified-6a77bcdbc372 6a77bcdbc372a0aa7fac24b3f21a83105c246c86
refs/heads/salvage/mac/web/refs-archive-web-consumers-original-6a80e122daf6 6a80e122daf69c6b5f058f7e94317ce8e6ba82cf
refs/heads/salvage/mac/web/refs-heads-archive-web-description-20261113-96975ee50cbd 96975ee50cbd1a0e97c60f2da61cf598f3c39f9e
refs/heads/salvage/mac/web/refs-heads-archive-web-description-pre-integration-bfaaaec3b3cf bfaaaec3b3cfdec0b20accc8c6ca30e7865fc485
refs/heads/salvage/mac/web/refs-heads-archive-web-gate-60b85e03-60b85e037aa2 60b85e037aa24fc0d30ae0195e6dffe8f59a6946
refs/heads/salvage/mac/web/refs-heads-codex-portal-account-admission-record-8acdf1bb1d3c 8acdf1bb1d3c5aed944dcc0c67445067583fd490
refs/heads/salvage/mac/web/refs-heads-codex-portal-account-sessions-joint-bfeea90cd5ad bfeea90cd5adb082b77047b0c97fcea7c7654c51
refs/heads/salvage/mac/web/refs-heads-codex-web-audit-metadata-c7ba62718fa4 c7ba62718fa4cded20914c179473cb2a02bf2ea9
refs/heads/salvage/mac/web/refs-heads-codex-web-gw104-help-8b987d4a3738 8b987d4a3738702c9ab357810116411a13b6ffdd
refs/heads/salvage/mac/web/refs-heads-codex-web-resource-history-ui-fc8d26d3ec64 fc8d26d3ec64793c29ed7121594400344613dbfa
refs/heads/salvage/mac/web/refs-heads-codex-web-serial-lint-98cf81c8f3c2 98cf81c8f3c2f82ec2967c017e266f9c345560c0
refs/heads/salvage/mac/web/worktree-lane-web-acceptance-4463bbe6-4463bbe628f6 4463bbe628f60486bbdb92c15f72311fb53d076a
refs/heads/salvage/mac/web/worktree-lane-web-acceptance-help-2b983dbf-2b983dbf75fe 2b983dbf75feaf36f6ac3423f4d1b0a1ecf0fc52
refs/heads/salvage/mac/web/bundle-monitoring-locales-4056925f-4056925fea03 4056925fea033b1fb22f32b3597679fdf6d8ebb3
refs/heads/salvage/mac/web/private-source-archive-e143f2cd1720 e143f2cd172005254c8db4525d81b98f1e0e0ad9
```

GitHub main: 5790b8961955400ffe25f10ced170bf4f669683c

Receipt: /Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-web/final-receipt.json

Migration manifest: /Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/mac-web/migration-manifest.json

Source manifest: /Users/passion./Documents/Passion's WorkSpace/Development/lane-web-source-archive-20261114/web/migration-salvage/source-manifest.json

迁移写入令牌已释放。本轮工作结束，停止所有开发、测试、归档和推送。
