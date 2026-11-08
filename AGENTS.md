# AGENTS.md

御坂捷网 MProxy：把 CDN 反向代理当作受控的 HTTP 正向代理入口。事实标准以代码与各目录内文档为准，本文件不重复实现细节与历史决策。

## 唯一约定

**禁止未经明确授权修改 AGENTS.md。** 未获得用户明确授权，任何智能体不得增删改本文件。

## 仓库与分支

- 远程仓库：`https://github.com/Sakana-1314/MProxy`（public），默认分支 `main`。
- 工作分支固定为 `dev`；不要在 `main` 上开发。
- `main` 只保留一行 README：「开发中，敬请期待」，与 `dev` 历史独立，不要把占位内容合并进 `dev`。
- 提交身份固定为 `Sakana <admin@yangrucheng.top>`，使用 SSH 签名，保持 GitHub 显示 Verified。
- 提交信息中文，格式 `type(scope): 描述`；分支前缀 `feat/`、`fix/`、`refactor/`、`docs/`、`chore/`。

## 目录

- `server/` - 后端（Go + Gin + SQLite），含 `Dockerfile`
- `web/` - 管理前端（Vue 3 + Vite + Naive UI），含 `Dockerfile` 与 nginx 模板
- `.github/workflows/` - 持续集成与镜像发布
- `build.ps1` - 构建前端并生成 `cdn-proxy.exe`

根目录只放 `AGENTS.md`、`README.md`、`build.ps1`、`.gitignore`；其余代码与文档放进所属目录内部。

## README

- `dev` 的 `README.md` 必须保持为空，不写说明、示例或链接；项目约定只写在本文件。
- `main` 的 `README.md` 只有一行「开发中，敬请期待」。

## 协作要求

- 改动前先读相关代码与测试；代码与文档不一致时以代码为准，并回头改文档。
- 已明确删除的功能不得恢复：请求日志、匿名代理、对外代理地址设置、站点名称设置、CA 下载、规则测试、版本号 UI。
- UI 只保留必要信息，不写解释性小字；设置页只有管理员密码与只读的代理接入。
- 行为变更必须补测试。提交前必须通过：`gofmt -w`、`go vet ./...`、`go test ./...`、`npm run build`。
- 不新增依赖，除非确有必要并在回复中说明原因。
- 代理热路径不写数据库；统计走内存聚合、定期批量落库。
- 流式响应不得退化为攒批：SSE 与 chunked 每次写入立即 flush，不加写超时，不替客户端协商压缩。

## 容器与发布

- 镜像固定 tag：`ghcr.io/sakana-1314/mproxy:server`、`ghcr.io/sakana-1314/mproxy:web`，不追加版本号。
- `web` 镜像由 nginx 提供管理界面，`/api/` 反代到 `SERVER_ADDR`（默认 `server:8088`）；`server` 镜像只提供 API 与代理。
- 推送 `dev` 触发「持续集成」与「构建镜像」；`main` 不触发发布。
