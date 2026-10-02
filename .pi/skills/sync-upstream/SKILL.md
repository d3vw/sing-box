---
name: sync-upstream
description: 同步 sing-box 上游变更：读取 dev-next 的 upstream tracking 映射并 fetch，让 dev-next 对齐上游，然后在 custom 分支 rebase，解决冲突，将全部定制变更保留为 dev-next 顶层的一个 commit。用于拉取上游、同步上游、更新 dev-next、rebase custom、压缩定制提交。
---

# 同步上游，保留单个定制提交

目标历史：`上游最新提交 (= dev-next) → 一个定制 commit (= custom)`。
只在本 sing-box 仓库执行。不默认 push，不创建 merge commit，不将上游提交 squash 成定制变更。
以下为分阶段操作指南，必须检查每步结果，不要一次盲跑全部命令。

## 1. 检查与保护现场

- 进入 `git rev-parse --show-toplevel` 指向的仓库根目录。
- 检查 `git status --short`、`git branch -vv`、`git remote -v`、`git worktree list --porcelain`。
- 如有正在进行的 rebase/merge/cherry-pick，先询问，不覆盖现场。
- 工作区或 index 不干净时，列出改动并询问：纳入定制提交，还是暂存为独立工作。未经确认不 commit、不 stash、不丢弃。未跟踪文件也必须保护；禁止 `git clean`。
- 用户要求纳入时，在 custom 上审核并按文件暂存、提交，稍后一起 squash。用户要求独立保留时，使用带唯一说明的 `git stash push -u`，记录 stash OID，最后 apply（成功核对前不 drop）。有忽略文件、子模块改动时另外检查保护，stash 并不覆盖所有情况。
- 若 custom 或 dev-next 被其他 worktree 使用，不强行移动该分支；协调后在对应 worktree 操作或暂停。
- 在改写前记录 custom、dev-next 及 tracking ref 的完整 SHA，并为 custom、dev-next 创建唯一的 `backup/custom-<timestamp>`、`backup/dev-next-<timestamp>` 分支。备份不自动删除。

## 2. 读取映射，先 fetch

```bash
git config --get branch.dev-next.remote
git config --get branch.dev-next.merge
git rev-parse --symbolic-full-name 'dev-next@{upstream}'
```

本仓库创建 skill 时映射为 `dev-next → upstream/testing`，远端为 `https://github.com/SagerNet/sing-box.git`。每次必须重新读取，禁止假定远端分支也叫 dev-next。配置缺失、remote 为 `.`、URL 或映射可疑时先询问。

按读取的 remote 和 merge ref 显式 fetch，并保存本次抓取的目标：

```bash
# remote、merge_ref 为上面验证过的配置值
# 显式抓取 tracking 对应分支，兼容上游 force-push。
git fetch "$remote" "$merge_ref"
new_upstream=$(git rev-parse 'FETCH_HEAD^{commit}')
```

fetch 失败就停止，禁止用旧的远端缓存冒充最新上游。不要 `git pull` 引入 merge。

## 3. 识别真正的定制边界（关键）

在移动 dev-next 前确定 `custom_base`：custom 当前所基于的最后一个上游提交。记录定制提交列表和净 diff。

- 若旧 dev-next 确实是 custom 的祖先，且其后的提交全部是定制提交，可使用旧 dev-next。
- 否则查看 custom 日志、备份 ref、远端旧历史、reflog、`git merge-base --fork-point`、`git log --left-right --cherry-pick` 及必要的 `git range-diff`，区分定制提交和上游重写历史。
- **禁止把 `git merge-base custom dev-next` 或 `dev-next..custom` 自动当作定制范围。** 上游可能 force-push，旧 dev-next 也可能落后，范围中可能混入数百个上游提交。
- 若 custom 顶层只有一个经核实的定制提交，则其父提交通常就是 custom_base；需审阅提交内容，而非只相信提交标题。
- 不能可靠确认边界时，展示候选及原因并询问，不能猜测后 reset。

检查 `custom_base` 是 custom 的祖先，审核 `git diff "$custom_base" custom` 和范围内全部提交。涉及 merge 时尤其检查净变更是否仅含定制功能。

## 4. 对齐 dev-next，压缩并 rebase custom

确保工作区干净且备份已建立。先切到 custom，再移动没有被其他 worktree 占用的 dev-next：

```bash
git switch custom
git branch -f dev-next "$new_upstream"
```

dev-next 是纯上游镜像，即使上游重写历史也应与本次 fetch 的提交完全一致；若发现其上有真正的本地定制提交，先询问归属，不静默丢弃。

若 custom_base 之上有多个定制提交，先压缩成一个（只有一个时无需重建）：

```bash
git reset --soft "$custom_base"
git commit -m 'feat: maintain custom sing-box services and integrations'
```

提交消息应按实际定制内容调整。压缩后验证 custom 的 tree 与压缩前备份一致。
然后显式指定旧边界，避免将旧版上游历史一起重放：

```bash
git rebase --onto dev-next "$custom_base" custom
```

这一步把单个定制补丁放到最新 dev-next 顶层。不要改用无明确边界的普通 rebase 来重放已重写的旧上游提交。

## 5. 解决冲突

- 使用 `git status` 和 `git diff --name-only --diff-filter=U` 定位冲突，阅读冲突双方及调用方。
- 保留上游修复与新 API，适配本地定制功能，不为过编译而删功能。重点检查 quota、speedtest、dashboard_ui 的实现、注册、选项、构建标签、发布工作流、安装脚本及依赖。
- rebase 中 `ours` 是新上游及已重放结果，`theirs` 是正重放的定制提交；禁止全局 `--ours` / `--theirs` 或盲目选择一侧。
- Go 依赖按实际引用协调 go.mod/go.sum/vendor，使用仓库既有工具链和生成流程，禁止随意升级所有依赖。
- dashboard 静态产物必须与源代码及 index/资源引用一致；检查 Makefile 中生成步骤及外部源码是否可用，不手改压缩 bundle、不使用不明版本重建。缺少源码或工具时说明阻塞。
- 对明确可推导的冲突直接解决；涉及产品语义无法确定的选择时询问。
- 逐文件 `git add <paths>`，确认无未合并文件后 `GIT_EDITOR=true git rebase --continue`，重复直至完成。不要无理由 `--skip`。
- 如果全部定制已被上游吸收，核实后说明情况；目标要求一个 commit，可用有明确说明的空提交保留标记，禁止伪造代码改动。
- 无法完成时报告现场、备份及原因。rebase 进行中可 `git rebase --abort` 返回 rebase 前状态；它不会撤销之前的 squash 或 dev-next 移动，完整恢复需使用记录的备份并先保护后续改动。

## 6. 验证与收尾

审阅新的 `git diff dev-next custom`，与同步前定制补丁对照，确认本地功能没有遗漏、旧上游实现没有被带回。

先读取 Makefile、go.mod、相关 CI 和构建标签文件，再运行适当的格式检查、相关测试和构建；至少覆盖修改的服务和带实际定制标签的 CLI 构建。使用临时构建输出路径，避免覆盖仓库现有二进制。工具链、依赖、权限或网络限制导致未运行/失败时如实列出，不能宣称验证通过。

验证中产生的必要修复用 `git commit --amend --no-edit` 纳入顶层提交，不能留下第二个定制提交。

最终必须验证：

```bash
test "$(git branch --show-current)" = custom
test "$(git rev-parse dev-next)" = "$new_upstream"
test "$(git rev-parse custom^)" = "$(git rev-parse dev-next)"
test "$(git rev-list --count dev-next..custom)" = 1
git diff --check dev-next custom
git status --short
```

如曾 stash 独立工作，用记录的 OID 执行 `git stash apply`，解决恢复冲突，核对文件后再考虑删除对应 stash；这些独立改动保留未提交，不擅自 amend。明确区分“已提交定制只有一个 commit”和“另有按要求保留的工作区修改”。

汇报映射、上游旧/新 SHA、custom 新 SHA、备份分支、冲突解决摘要、测试结果及剩余工作区改动。
默认不推送。用户明确要求推送时，先检查远端 custom 的最新状态及是否有他人新增工作，记录预期远端 SHA，使用带显式 lease 的 `--force-with-lease=refs/heads/custom:<expected-sha>`，禁止裸 `--force`，不推送到 upstream。
