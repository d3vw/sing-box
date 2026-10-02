# Syncing custom with upstream/testing

`custom` 分支的结构约定：`custom` = `upstream/testing` 的线性历史 + 顶部恰好 1 个我们自己的 commit
（内容是 quota/speedtest/dashboard-ui 三个功能，commit message 以 "feat: add quota, speedtest, and dashboard-ui services" 开头）。

当 upstream 有新提交（可能经过 force-push，历史被重写过）时，需要把 custom 更新到最新 upstream/testing，
同时保持这个「N 个 upstream 提交 + 我们 1 个 commit」的结构不变。请按以下步骤操作：

1. 记录当前状态：

   ```
   git branch backup-custom-$(date +%Y%m%d%H%M%S) custom   # 备份，任何步骤出错可以回退
   OLD_BASE=$(git rev-parse custom~1)   # 当前我们这个 commit 的旧 parent（即旧的 upstream tip）
   OUR_COMMIT=$(git rev-parse custom)
   ```

2. 拉取最新 upstream（即使对方 force-push 过，直接 fetch 即可，不需要特殊处理，
   因为我们只依赖自己 commit 的 hash，不依赖 upstream 旧历史是否还存在）：

   ```
   git fetch upstream testing
   ```

3. 用 rebase --onto 把我们这 1 个 commit 摘下来，接到新的 upstream/testing 顶端：

   ```
   git rebase --onto upstream/testing $OLD_BASE custom
   ```

   - 如果没有冲突，直接完成，custom 现在是「新 upstream 线性历史 + 我们 1 个 commit」。
   - 如果有冲突，大概率出现在我们这个 commit touch 过的文件上，已知的重点文件有：
     `constant/proxy.go`, `release/DEFAULT_BUILD_TAGS_OTHERS`, `service/api/server.go`,
     `route/route.go`, `route/rule_conds.go`
     以及 quota/speedtest/dashboard_ui 相关的 `include/*`, `option/*`, `service/quota|speedtest|dashboard_ui/*`
     解决原则：upstream 新增的内容全部保留，我们自己加的 quota/speedtest/dashboard-ui 相关代码也全部保留，
     两者是不同的类型/字段/函数，正常是可以共存的，不要因为解决冲突而删掉任何一边的功能性代码。

4. 验证没有内容丢失（用这个方法而不是肉眼看 diff，更可靠）：

   ```
   git diff --stat $OUR_COMMIT custom   # 只应该看到 upstream 新增内容的变化，
                                         # 我们自己独有的文件（quota/speedtest/dashboard_ui）内容应该逐字不变
   ```

5. 编译验证：

   ```
   go build ./...
   ```

   如果编译失败，说明 rebase 冲突解决时可能改错了，回到 backup 分支重新来。

6. 确认无误后，强制推送（因为这个分支的历史每次都会被重写）：

   ```
   git push --force origin custom
   ```

7. 清理备份分支：

   ```
   git branch -D backup-custom-<timestamp>
   ```
