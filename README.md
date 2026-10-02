# Bilidown（个人修改版）

哔哩哔哩视频解析下载工具，支持 8K 视频、Hi-Res 音频、杜比视界下载、批量解析，可扫码登录，常驻托盘。

> 本项目是 [iuroc/bilidown](https://github.com/iuroc/bilidown) 的个人分支，基于 `v2.1.1`。
> 原项目采用 Apache-2.0 许可，本分支沿用同一许可，著作权归原作者所有。
> 改动过的文件在文件头部都标注了修改说明。

## 下载

已提供 Windows 预编译包，可直接到
[Releases](https://github.com/yagumi0411/bilidown/releases) 下载：

-   `bilidown_Windows_x86_64.zip`：Windows 64 位版本，推荐大多数用户使用。
-   `bilidown_Windows_x86.zip`：Windows 32 位版本，适合仍在使用 32 位 Windows 的环境。
-   `SHA256SUMS.txt`：发布包 SHA256 校验值。

压缩包内已包含 `bilidown.exe`、`static/` 前端资源和 `bin/ffmpeg.exe`。
解压后双击 `bilidown.exe` 即可运行。

## 本分支的改动

### 1. 修复大文件下载到约 92% 中断（主要改动）

原版用 Go 默认启用的 HTTP/2 下载媒体流。B 站 CDN 会在传输中途发送
`RST_STREAM(INTERNAL_ERROR)` 重置连接，Go 客户端抛出：

```
stream error: stream ID 1; INTERNAL_ERROR; received from peer
```

实测同一个视频连续两次分别停在 **91.9%** 和 **92.6%**（约 74MB / 80MB 处），
而且没有任何重试机制——已下载的部分被整个丢弃，任务直接判失败。
一个 2 小时视频的音频部分，下到 74MB 时一条连接被重置，整个任务就废了。

**改动**（`server/bilibili/client.go`）：显式关闭 HTTP/2，媒体下载改走 HTTP/1.1，
同时复用全局 Transport，保留连接复用与 DNS 缓存。

改动后同一个用例重下成功：产物 2,012,929,389 字节，fMP4 的 box 结构校验完整无截断。

### 2. 失败原因写入日志文件

发布版没有控制台，任务标红之后无从查起。现在标准库 `log` 的输出会同步追加写入
运行目录下的 `bilidown.log`，每行带时间戳：

```
2026/09/14 21:01:51 Task-6-Error: DownloadMedia: Get "https://...": dial tcp ...: connection refused
2026/09/14 21:01:24 Bilidown v2.1.1 启动，工作目录: D:\Tools\bilidown_Windows_x86_64
```

覆盖下载失败、合并失败、元数据写入失败、启动信息等。任务失败的原因同时保留在
数据库的 `log` 表里。

### 3. 下载 goroutine 的 panic 兜底

下载跑在独立 goroutine 里，未捕获的 panic 会**静默杀掉整个进程**。
现在会连同调用栈一起写进日志，并把任务标记为失败，进程继续存活。

### 4. 发布形态

构建时加 `-ldflags "-H windowsgui"`，双击运行不再弹出黑色控制台窗口。

## 支持解析的链接类型

-   【单个视频】https://www.bilibili.com/video/BV1LLDCYJEU3/
-   【番剧和影视剧】https://www.bilibili.com/bangumi/play/ss48831
-   【视频合集】https://space.bilibili.com/282565107/channel/collectiondetail?sid=1427135
-   【收藏夹】https://space.bilibili.com/1176277996/favlist?fid=1234122612
-   【收藏夹】https://www.bilibili.com/medialist/detail/ml1234122612
-   【UP 主空间地址】等待 3.x 版本支持

说明：视频合集链接会先解析合集中的第一个视频，再按普通视频继续解析。

## 构建与运行（Windows）

推荐直接下载 Release 里的预编译压缩包。下面的步骤适合需要自行构建或二次开发的用户。

### 1. 直接运行发布包

下载对应架构的压缩包并解压，保持目录结构不变：

```
bilidown.exe
static/
bin/ffmpeg.exe
```

双击 `bilidown.exe`。托盘出现图标，浏览器自动打开 <http://127.0.0.1:8098>。

首次运行会在运行目录创建 `data.db`、`download/` 和 `bilidown.log`。

**注意**：用快捷方式启动时，"起始位置"必须指向 exe 所在目录；
从命令行启动要先 `cd` 过去。否则程序会在错误的位置新建一个空的 `data.db`，
任务列表和登录状态全部丢失。

### 2. 构建后端

需要 [Go](https://go.dev/dl/) 1.23 或更高版本。

```shell
cd server
set CGO_ENABLED=0
go build -ldflags "-H windowsgui" -o bilidown.exe .
```

Windows 上三个依赖（`getlantern/systray`、`modernc.org/sqlite`、`skip2/go-qrcode`）
都是纯 Go，`CGO_ENABLED=0` 实测可行。
Linux 下 systray 需要 CGO，另需 `pkg-config`、`gcc`、`libayatana-appindicator3-dev`。

如需构建 32 位 Windows 版本：

```shell
cd server
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=386
go build -ldflags "-H windowsgui" -o bilidown.exe .
```

### 3. 构建前端

仓库里不含构建好的前端产物（`static/` 已被 `.gitignore` 排除），需要自己构建一次：

```shell
cd client
pnpm install
pnpm build
```

产物会输出到 `server/static`（见 `client/vite.config.ts`）。不改前端的话，这一步只需做一次。

### 4. 准备运行目录

程序里 `static/`、`bin/ffmpeg`、`data.db` 都是**相对当前工作目录**查找的，
所以下面这些文件必须和 exe 放在同一个目录：

```
bilidown.exe      构建产物
static/           前端产物（第 3 步的输出）
bin/ffmpeg.exe    合流工具；装在 PATH 里也可以
data.db           首次运行自动创建
download/         下载输出目录，可在设置里改
bilidown.log      首次运行自动创建，用于排查失败原因
```

## 软件特色

1. 前端采用 [Bootstrap](https://github.com/twbs/bootstrap) 和 [VanJS](https://github.com/vanjs-org/van) 构建，轻量美观
2. 后端使用 Go 语言开发，数据库采用 SQLite，简化构建和部署过程
3. 前端通过 [p-queue](https://github.com/sindresorhus/p-queue) 控制并发请求，加快批量解析速度

## 其他说明

-   本程序不支持也不建议 HTTP 代理，直接使用国内网络访问能提升批量解析的成功率和稳定性。
-   直链带 2 小时有效期，批量下载超过 2 小时需要重新解析。

## 特别感谢

本项目基于 [iuroc/bilidown](https://github.com/iuroc/bilidown)，感谢原作者及以下开源项目：

-   [twbs/bootstrap](https://github.com/twbs/bootstrap) - 前端开发必备的响应式框架，简化页面布局
-   [vanjs-org/van](https://github.com/vanjs-org/van) - 轻量级的前端框架，专注于构建高效应用
-   [vitejs/vite](https://github.com/vitejs/vite) - 快速的前端构建工具，基于 ES 模块开发
-   [SocialSisterYi/bilibili-API-collect](https://github.com/SocialSisterYi/bilibili-API-collect) - B 站 API 集合，支持多种操作接口
-   [sindresorhus/p-queue](https://github.com/sindresorhus/p-queue) - 支持并发限制的 JavaScript 队列处理库
-   [iuroc/vanjs-router](https://github.com/iuroc/vanjs-router) - 轻量级前端路由工具，适用于 Van.js 框架
-   [uuidjs/uuid](https://www.npmjs.com/package/uuid) - 用于生成唯一标识符（UUID）的 JavaScript 库
-   [getlantern/systray](https://github.com/getlantern/systray) - 简单的跨平台系统托盘图标库，支持图标管理
-   [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) - Go 语言的 SQLite3 数据库驱动，轻量高效
-   [skip2/go-qrcode](https://github.com/skip2/go-qrcode) - 生成 QR 码的 Go 语言库，简单易用

## 软件界面

![](./docs/2024-11-05_090604.png)

## 许可

[Apache License 2.0](LICENSE)，同上游项目。
