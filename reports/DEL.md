{
  "lane": "DEL",
  "host": "Mac",
  "mainAdvanced": 0,
  "status": "salvage",
  "remoteBranches": 57,
  "checks": {
    "check": "NOTRUN rawFree 1247395840 < 6GiB",
    "build": "NOTRUN"
  },
  "originalTip": "e3aff53eaeae247011215cdaa37e4940fa20a02a",
  "rebasedTip": "305cc970aa61e9f367d64e8046b60333cedea126",
  "privateSourceTip": "160837b357bac7bdd9ec2aa0cf72eef9b5f01231",
  "privateSourceFiles": 168,
  "remoteHeadsSnapshot": "github-after-del.tsv",
  "writerTokenRevoked": true,
  "fullReportPending": false,
  "fullReport": "DEL 本轮迁移已结束，写入／推送令牌已释放。主线新增提交 0；57 个 salvage 分支已推送并核对 SHA，共保全相对主线的 241 个原始提交，以及 4 个变基提交和 1 个源码归档提交。 内容 远端分支（ salvage/mac/del/ 下） 完整 SHA 原始候选 del-tls-qualification-notes-e3aff53eaeae e3aff53eaeae247011215cdaa37e4940fa20a02a 独立旧历史 del-recovered-acceptance-9372c0dcf93a 9372c0dcf93a9198c048d160ffeeb4c02e915d16 变基待验候选 rebased-unverified-305cc970aa61 305cc970aa61e9f367d64e8046b60333cedea126 私人源码归档 private-source-archive-160837b357ba 160837b357bac7bdd9ec2aa0cf72eef9b5f01231 验证结果： 变基、差异空白检查均通过；候选文件树与原始候选合并当前主线的结果一致。 make check、make build 未运行：锁身份正确，但 raw Free 为 1,247,395,840 字节，低于 6 GiB 门槛。未尝试推进 main。 原有 22 个 DEL 引用、14 个现存工作树保持不变；缺失工作树的注册保留。 归档包含 168 份源码副本，其中 166 份逐字节一致。两份脚本内嵌 Git bundle，仅上传明确标注的源码投影；原件、哈希和省略说明均保留。 本轮新增勾选 0。现有 P1 勾选仍为 DEL-102/104/105/106/111/112/116/117、REL-110。21 条 P1 尚未全部完成：DEL-114 停在 TLS 实现及局部验证，仍缺最新候选的真实 Linux、实际镜像 CLI、安装和断网外连捕获验收；REL-106 的取消原型尚未运行，REL-102 仍是测量边界研究。 完整远端 SHA、历史覆盖和执行结果见 final-receipt.json。源码原件与投影映射见 source-manifest.json。 后续可从保留的 迁移工作树 继续验证；源码归档工作树 仅供查阅，旧门禁脚本不可执行。本轮未启动服务、未占用端口、未操作服务器或 lane-b-* 容器。",
  "originalUniqueCommitsBeyondMain": 241,
  "addedArchiveCommits": 1,
  "rebaseNewCommitObjects": 4,
  "tickedP1": [
    "DEL-102",
    "DEL-104",
    "DEL-105",
    "DEL-106",
    "DEL-111",
    "DEL-112",
    "DEL-116",
    "DEL-117",
    "REL-110"
  ],
  "remaining": "DEL-114 latest Linux/image CLI/install/offline egress acceptance; REL-106 cancellation prototype unrun; REL-102 measurement-boundary research",
  "finalReceipt": "/Users/passion./.local/state/ownsolid/coordinator/migration/20261113T223425Z-8f047db7/del/final-receipt.json"
}