# tcp2rdp-client-web

tcp2rdp 桌面工作台的 **Web 版**：在浏览器里完成 tcp2rdp（rdp2tcp）客户端的全部操作 ——
节点管理、启动/停止隧道、实时状态与日志、服务器探测、分流规则、TUN 全局接管，
界面与桌面版 `tcp2rdp-client` 一致。

技术栈：**Go + Svelte + SQLite**（纯 Go、无 CGO，Windows / Linux / macOS 单文件运行）。

```
浏览器 ──HTTP/SSE──► tcp2rdp-web (Go 内嵌 Svelte 前端)
                        │  驱动子进程
                        ▼
                   rdp2tcp -type client   ──RDP 伪装隧道──►  rdp2tcp -type server
                        │
                 127.0.0.1:1080 SOCKS5 / HTTP 代理 / -L -U 转发 / TUN 虚拟网卡
```

## 下载

到 [Releases](https://github.com/jiqing112/tcp2rdp-client-web/releases/latest)
下载对应平台的压缩包，解压即用：

| 包名 | 平台 |
|---|---|
| `tcp2rdp-web-<版本>-windows-amd64.zip` | Windows x64 |
| `tcp2rdp-web-<版本>-linux-amd64.tar.gz` | Linux x64 |
| `tcp2rdp-web-<版本>-linux-arm64.tar.gz` | Linux ARM64 |
| `tcp2rdp-web-<版本>-darwin-amd64.tar.gz` | macOS Intel |
| `tcp2rdp-web-<版本>-darwin-arm64.tar.gz` | macOS Apple Silicon |

**打包规则**：每个压缩包里都内置两个二进制 —— 本仓库编译的工作台 `tcp2rdp-web`，
以及在打包时自动从 [jiqing112/tcp2rdp](https://github.com/jiqing112/tcp2rdp)
最新 release 下载的**对应平台**隧道客户端 `rdp2tcp`（Windows 为 `rdp2tcp.exe`），
两者放在一起，解压后无需再单独下载隧道本体。另附 `README.md`、`LICENSE`，
发布页还有 `checksums.txt`（SHA-256）可校验完整性。

内置的 rdp2tcp 不会随工作台自动升级：想换新版，下载新发布的包即可；或手动替换
程序目录（或 `bin/`）里的 rdp2tcp，也可以在页面「日志与高级」里直接填它的完整路径。

## 功能

- **节点管理**：多节点增删改、点击切换（运行中切换自动重连）、每节点独立分流模式
  （全局 / 规则分流·清单 / 规则分流·仅 IP / 直连）、自定义 IP/域名清单与规则 DNS
- **隧道控制**：一键启动/停止（Ctrl+Enter），状态机 未连接→连接中→运行中→停止中/出错
- **实时仪表**：隧道延迟（隧道 / TCP / Ping 三种检测方式）、延迟曲线、上行/下行速率、
  连接数、运行时长（rdp2tcp `-stat` 输出驱动）
- **会话日志**：实时推送（SSE）、级别过滤、按 `rdp2tcp` 的输出智能分级，重启后保留尾部
- **服务器探测**：按 mstsc 的字节序做 RDP 协商 + TLS 握手，报告证书指纹，一键填入 `-pin`
- **本地 HTTP 代理**：SOCKS5 模式自动在本地起 HTTP 代理桥（Windows 可一键接管系统代理，
  断开自动还原）
- **TUN 模式**：预设后自动等待隧道就绪，创建虚拟网卡接管整机 IPv4 流量（Windows 用
  wintun，Linux 用内核 TUN），服务器/DNS 自动旁路，支持多点自检，退出自动撤销路由
- **通用设置**：监听地址/端口、代理模式、`-L`/`-U` 端口转发、TLS 指纹、填充、证书
  校验（TOFU / pin / insecure）等，与桌面版同一套参数

## 构建

依赖：Go 1.22+、Node 18+。

```bash
make build      # 构建本机二进制（先构建前端再编译 Go，前端嵌入二进制）
make package    # 打本机平台的发布包（捆绑上游最新 rdp2tcp），输出 dist/
make dist       # 打全部五个平台的发布包，输出 dist/ 并生成 checksums.txt
```

手动构建：

```bash
cd frontend && npm install && npm run build && cd ..
go build -ldflags "-s -w" -o tcp2rdp-web .
```

打包规则实现在 [scripts/package.sh](scripts/package.sh)：交叉编译工作台 → 从
`https://github.com/jiqing112/tcp2rdp/releases/latest/download/` 拉取对应平台的
rdp2tcp → 连同 README/LICENSE 压成 zip（Windows）或 tar.gz（其余平台，保留可执行位）。

### 发布（自动）

推送 `v*` 标签（或在 Actions 页手动触发 workflow）即运行
[.github/workflows/release.yml](.github/workflows/release.yml)：
构建前端 → `go test` → 按 `scripts/package.sh` 的规则打五个平台的包 →
自动上传到 Releases，生成更新日志，并在说明里记录本次捆绑的上游 rdp2tcp 版本。

前端开发模式（热更新，API 代理到本机 8080）：

```bash
go run . -addr 127.0.0.1:8080 &     # 终端 1
cd frontend && npm run dev          # 终端 2，打开 http://localhost:5173
```

## 运行

发布包已内置 rdp2tcp；从源码运行时，把 `rdp2tcp` 客户端二进制放到程序旁边
（**程序目录**、`bin/` 或 `resources/` 均可；也可以在页面「日志与高级」里填写
完整路径），然后：

```bash
./tcp2rdp-web                       # 默认 127.0.0.1:8080（或页面里保存的工作台监听设置）
./tcp2rdp-web -addr 0.0.0.0:8080    # 显式指定监听地址（优先级最高，不写入保存的设置）
./tcp2rdp-web -token 我的秘密        # 开启访问令牌（URL 加 ?token=我的秘密）
./tcp2rdp-web -data /var/lib/tcp2rdp -addr 127.0.0.1:8080
```

浏览器打开显示的地址即可。**工作台的监听范围和端口也可以直接在页面右侧的「工作台」
卡片里改**（仅本机 ⇄ 局域网、换端口），点「应用」立即生效、无需重启进程，并自动
跟随放行/撤销防火墙规则；设置持久化，下次启动沿用。显式 `-addr` 参数始终优先于
保存的设置，且不写入保存值。改端口后当前页面地址会失效，需用新地址重新打开。

Linux 下的 systemd 服务示例：

```ini
[Unit]
Description=tcp2rdp web workbench
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/tcp2rdp-web
ExecStart=/opt/tcp2rdp-web/tcp2rdp-web -addr 127.0.0.1:8080
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

### 参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `-addr` | 上次保存的工作台监听设置（首次 `127.0.0.1:8080`） | HTTP 监听地址；显式指定时优先级最高 |
| `-data` | 程序目录 `data/` | SQLite 数据库、规则缓存、会话记录目录 |
| `-token` | 空 | 访问令牌；设置后 API 与页面都要携带（环境变量 `TCP2RDP_TOKEN` 亦可） |
| `-version` | — | 打印版本 |

> 安全提示：这个页面能启动隧道并读取日志。只绑定 `127.0.0.1`，或加 `-token`；
> 若监听 `0.0.0.0`，请务必设置令牌并用防火墙限制来源。

### Windows 防火墙自动放行

Windows 防火墙默认 `BlockInbound`：监听 `0.0.0.0` 成功后，局域网设备的连接会被**静默
丢弃**（超时），而本机回环访问一切正常。本程序会自动管理两条防火墙规则，随每次启动
重建（delete→add，端口与程序始终匹配）、停止或退出时撤销：

| 规则名 | 何时创建 | 放行内容 |
|---|---|---|
| `tcp2rdp-web` | `-addr` 绑定 `0.0.0.0`/`::` 时，或在页面「工作台」卡片切到「局域网」时 | 工作台页面端口（仅限本程序 exe） |
| `tcp2rdp-web-tunnel` | 隧道以 `0.0.0.0` 监听启动时 | 隧道端口（仅限 rdp2tcp 客户端 exe） |

- 管理员身份运行时直接用 `netsh` 创建；普通权限会弹**一次 UAC 授权**，取消则放行失败
  （日志有提示，重新启动可再弹）。
- 规则同时限定 `program=` 与 `localport=`，比系统「允许访问」弹窗生成的更严格。
- 第三方安全软件（火绒/360 等）自带防火墙不归 `netsh` 管：有规则仍连不通时先查它。
- Linux 不做任何防火墙操作（交给你自己的 ufw/firewalld）。

诊断：局域网设备 `nc -z -w 3 <主机IP> <端口>` 秒回为通；**等满 3 秒失败**即被防火墙
丢弃（无规则或程序路径不匹配）；立即拒绝则说明包已到达但该地址没有监听。

## 数据与文件

| 文件 | 说明 |
|---|---|
| `data/tcp2rdp-web.db` | SQLite：设置、节点、会话日志尾部（3000 行） |
| `data/session.txt` | 最近一次会话的完整命令行（可直接粘贴到终端复现） |
| `data/rules/` | 「在线清单」下载缓存（7 天有效期） |
| `data/geo-cache.json` | 节点国旗的 IP 归属地缓存 |

## API 一览（供脚本调用）

所有接口返回 JSON；设置 `-token` 后需带 `Authorization: Bearer <token>` 或 `?token=`。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/boot` | 一次拿全 info + settings + status + logs |
| GET/POST | `/api/settings` | 读取 / 保存设置（含节点数组） |
| POST | `/api/start` · `/api/stop` | 启动（body 为 settings）/ 停止隧道 |
| POST | `/api/nodes/select` · `/api/nodes/route` | 切换节点 / 切换分流（运行中会重连） |
| POST | `/api/probe` | 服务器探测（`{"server":"1.2.3.4:3389"}`） |
| POST | `/api/latency` | 延迟检测设置（`{"mode","target","interval"}`） |
| POST | `/api/sysproxy` | Windows 系统代理开关（`{"on":true}`） |
| POST | `/api/tun` · `/api/tun/test` | TUN 开关（`{"on":true}`）/ 运行多点自检 |
| GET/DELETE | `/api/logs` | 读取 / 清空会话日志 |
| POST | `/api/weblisten` | 工作台监听热切换（`{"host":"0.0.0.0","port":"8080"}`），联动防火墙并持久化 |
| GET | `/api/events` | SSE 事件流（`status` / `logs`） |
| GET | `/api/health` | 健康检查 |

## 与桌面版的差异

- 去掉了系统托盘（Web 服务端没有窗口，托盘无从谈起）；其余能力与桌面版对齐
- 设置从 JSON 文件改为 **SQLite** 存储（节点单独成表，日志可跨重启保留尾部）
- 「选择客户端文件」改为直接填写路径（服务端无文件对话框）
- 新增可选访问令牌、`0.0.0.0` 监听与页面内热切换工作台监听（暴露给局域网时建议配合 `-token`）

### TUN 模式（虚拟网卡接管整机流量）

页面右侧「出口」卡片的 TUN 开关与桌面版同一套语义：

- **预设**：隧道未运行时也可先打开；等待器每秒探测，隧道**真正承载数据后**才创建
  虚拟网卡并安装 `0.0.0.0/1 + 128.0.0.0/1` 默认路由——不会在隧道不可用时把机器断网
- **旁路**：隧道服务器与系统 DNS 自动加 `/32` 旁路路由，保持在物理网卡
- **撤销**：停止隧道、关闭开关或进程退出（含 Ctrl+C）都会先撤路由再杀 tun2socks；
  异常崩溃留下的路由由下次启动自动清理
- **自检**：接管后可运行多点自检（进程/网卡/地址/默认路由/物理网关/服务器旁路/DNS/
  TCP·TLS 经 TUN/UDP 经 TUN）

依赖与权限：

| 平台 | 依赖 | 权限 |
|---|---|---|
| Windows | `tun2socks.exe` + `wintun.dll`（放程序目录或 `bin/`，发布包可在上游 Releases 下载） | 管理员身份运行 |
| Linux | `tun2socks`（[xjasonlyu/tun2socks](https://github.com/xjasonlyu/tun2socks) 对应架构二进制，重命名放置）+ iproute2 | root（sudo）运行 |
| macOS | 暂不支持（界面上开关会显示"当前平台不支持"，代理与端口转发不受影响） | — |

开启后 tun2socks 的日志会以 `TUN` 前缀出现在会话日志里。

## 许可

与上游 tcp2rdp / tcp2rdp-client 保持一致（MIT）。
