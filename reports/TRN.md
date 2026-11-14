# Mac TRN/DAT 迁移保全报告

1. 源码提交：保留全部 7 个原始分支和 92 个自定义引用。独立迁移工作树为 `/Users/passion./Documents/Passion's WorkSpace/Development/ownsolid-mac-trn-migration-20261114t072303z`。原始源码树 `b8d76d93dcbed76ad7f54be23da35b4b90d8e272` 的 5,088 个文件按原树生成未验收快照 `9ce875aed719110710470a4f51a62a7a82b290aa`；没有改写原始树。本次未发现仍存的本线 Git 外源码或 bundle，原临时目录已经缺失，不能恢复其未提交内容。

2. 同步和验收：显式从 GitHub fetch main，退出码 0，观察到 `5790b8961955400ffe25f10ced170bf4f669683c`。新鲜 raw Free 为 3.923 GiB，低于 6 GiB 门槛；变基、make check、make build、模块测试均为 NOTRUN。没有运行旧入口，没有获取或重建完整构建锁。

3. 冲突和原物：未进行变基，因此没有解决或丢弃冲突。未 force、reset、clean、prune 或删除分支、工作树、源码。现存 lane-f 工作树检查干净；两处缺失临时工作树的注册仍保留，其当前未提交状态未知。共享 main 中预存的外线编译文件未动。

4. 推送：一次 --atomic 推送退出码 0；100 个 salvage 引用逐一通过 GitHub SHA 回读，其中 99 个保留原始引用、1 个保存源码快照。main 新增提交 0；本次新建未验收快照提交 1。准确远端 ref→SHA 及原始 ref/HEAD 对应关系见 final-receipt.json。

5. 保全和重建：所有候选仅在 salvage/mac/trn 下保留。本轮新增 roadmap 勾选 0；快照中两个所属模块的完成勾选数也为 0。换机后从 GitHub fetch 对应 salvage 引用，再用 git worktree add 创建独立工作树。原始 published-reader 和 native-test-review 的保全引用见下方；完整验收通过后才可推进 main。实际网关启动、身份角色、只读挂载、排空生命周期以及需要 GPU/Agent 的边界仍未因此验收。

```bash
git fetch https://github.com/Passion-Flow/ownsolid.git 'refs/heads/salvage/mac/trn/heads-codex-trn-published-reader-review-7043cbbff0c0:refs/heads/salvage/mac/trn/heads-codex-trn-published-reader-review-7043cbbff0c0' 'refs/heads/salvage/mac/trn/heads-codex-trn-reader-native-test-review-3603eaa77d9d:refs/heads/salvage/mac/trn/heads-codex-trn-reader-native-test-review-3603eaa77d9d' 'refs/heads/salvage/mac/trn/source-only-unqualified-9ce875aed719:refs/heads/salvage/mac/trn/source-only-unqualified-9ce875aed719'
git worktree add ../trn-reader-review 'refs/heads/salvage/mac/trn/heads-codex-trn-published-reader-review-7043cbbff0c0'
git worktree add ../trn-native-review 'refs/heads/salvage/mac/trn/heads-codex-trn-reader-native-test-review-3603eaa77d9d'
git worktree add ../trn-unqualified-source 'refs/heads/salvage/mac/trn/source-only-unqualified-9ce875aed719'
```

6. 清单和陷阱：source-snapshot-manifest.json 列出 5,088 个路径、模式及 blob SHA；original-refs.json 和 original-ref-coverage.json 保存原始引用映射；original-worktrees.json 记录缺失工作树。Git 外源码归档文件数为 0。排除预存外线编译文件 1 个；依赖、node_modules、模型、数据、数据库、构建输出、临时输出和凭据均不归档，未广泛枚举他线缓存，未删除原物。共有及仅 reflog 历史由协调器统一保全，本线未重复导出。未把本机 origin 当作 GitHub 备份，传输显式使用已验证 GitHub HTTPS 及代理。不存在独立全量 clone 验证；由协调器统一执行。

远端 SHA 回读后已释放本机串行令牌并停止。没有服务器操作或后续测试。
