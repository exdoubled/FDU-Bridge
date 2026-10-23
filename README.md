# FDU Bridge

> 🚫 **免责声明**：本程序**按原样提供**，作者**不保证其正确性或可靠性**。是否适用请自行判断，**使用造成的问题或后果由使用者自行承担**。

中文 | [English](README_en.md)

复旦大学桌面校园网工具。配合 Clash／Mihomo 客户端使用时，指定的校内资源走复旦 aTrust 隧道，其余流量沿用 Clash 的设置；也可单独提供本地 SOCKS5／HTTP 代理。基于 [ZJU Connect](https://github.com/Mythologyli/zju-connect) 开发，致谢见文末。

## 功能

- 在浏览器中完成 UIS 登录，自动接收认证回调。
- 按精确域名、域名后缀、IPv4 或网段，逐条选择走 FDU 的资源；保存后自动加载，新连接按新规则，已有连接保持原路径。
- 一键接入 Clash Verge Rev（含便携版），也支持其他 Mihomo 桌面客户端；自动跟随全局／规则模式和订阅切换，停用时恢复外网出口。
- 提供 Windows、macOS、Linux 桌面 GUI，集成登录、连接启停、日志、外网节点、校内资源和高级设置。

## 快速开始

1. 从 [Releases](https://github.com/exdoubled/FDU-Bridge/releases) 下载对应系统的压缩包并解压，GUI 与核心程序保持在同一目录。
2. 打开 Clash 客户端，选好订阅和外网节点，使用全局或规则模式。
3. 启动 GUI（见下表），在**设置 → 代理接入**确认接入目标；同时运行多个客户端时，选定一个并保存。
4. 在首页开启**校内访问**，首次确认后在弹出的浏览器中完成 UIS 登录。
5. 在**校内资源**添加域名或 IP，逐条勾选启用并保存。

校内资源页可导入／导出 JSON，包含所有条目及启用状态。导入预览可合并或替换当前草稿，点击**保存**后生效；合并时已有条目保留当前草稿的启用状态。导出包含未保存的修改，不含登录信息。

| 系统 | 启动程序 |
| --- | --- |
| Windows | `FDU-Connect-GUI.exe` |
| macOS | `FDU Connect.app` |
| Linux | `fdu-connect-gui` |

全局模式下在本应用的**外网节点**页切换出口，规则模式下在 Clash 中切换。模式和 TUN 开关都在 Clash 中操作，FDU 自动跟随。退出时先在首页关闭校内访问，再退出 Clash。

本文以当前源码为准，Release 压缩包以对应版本的说明为准。

## 接入方式

在**设置 → 代理接入**选择，保存后回首页启用；切换前需先停用当前连接。

- **自动接入**（默认）：识别正在运行的客户端及其本地控制接口；无法唯一确认目标时暂停。
- **手动接入**：选择客户端实际使用的完整运行 YAML（仅含节点的订阅不算），按需填写控制接口和密钥。保存后复用，连接期间跟随模式和该文件的更新。
- **仅本地代理**：无需 Clash。将需要访问校园资源的应用指向窗口显示的 SOCKS5／HTTP 地址，由应用或外部客户端自行分流；不会自动接管所有应用，也不使用校内资源和外网节点页面。

Clash Verge Rev 通过订阅扩展一键接入。其他客户端需支持 Mihomo 控制 API、规则集和专用监听入口，已加入 FlClash、Clash Party、Sparkle、Clash Nyanpasu、ClashX.Meta 的发现逻辑；不满足时改用仅本地代理。如需自行安装扩展，可在**设置 → 导出扩展（可选）**复制覆写 JS 或 YAML 片段（不是完整订阅），更改 FDU 监听端口后需重新导出。

**设置 → 高级设置**可调整服务器、认证方式、DNS、网卡、本地代理和端口转发，或导入 TOML 配置，含义见[配置参数](docs/config.md)。Clash 接入目前仅支持 aTrust，EasyConnect 可使用仅本地代理；与 Clash 分流冲突的选项会在界面禁用或在启用前提示。自定义网关和复杂认证组合需自行验证。

## 工作原理

Clash 通过 TUN、系统代理或应用代理接收流量；FDU Bridge 负责 UIS 认证、aTrust 隧道和指定资源的转发，GUI 通过本机规则接口同步已启用的校园规则。启用时，GUI 向 Clash 加入 `FDU-ROUTER` 节点、校园规则和专用回流入口：Clash Verge Rev 先备份再写入订阅扩展；其他客户端通过控制接口加载内存覆写，不改订阅文件或私有配置，客户端重载后自动重新接入。GUI 不修改系统代理、绕过列表或 Clash TUN 开关。

| Clash 模式 | 指定的校内资源 | 其他流量 |
| --- | --- | --- |
| 全局 | Clash → FDU Bridge → aTrust 隧道 → 校内资源 | Clash → FDU Bridge → Clash 专用回流入口 → `INTERNET` 所选出口 |
| 规则 | Clash 校园规则 → FDU Bridge → aTrust 隧道 → 校内资源 | 沿用 Clash 原有规则与节点，可代理、直连或拒绝 |

全局模式的回流入口固定使用 `INTERNET`，避免再次进入 `GLOBAL` 形成循环。FDU 使用用户态网络栈，不创建额外虚拟网卡；登录与 VPN 底层连接绑定物理网卡，避免被 Clash TUN 再次接管。仅本地代理方式由应用直连 FDU，不经过回流入口，也不使用 GUI 的校园规则。

## 注意事项

- **覆盖范围**：开启 Clash TUN 可覆盖所有应用；不开时只接管使用 Clash 代理的应用，系统代理绕过的内网地址仍会直连，需自行调整绕过规则。访问失败时先查看首页的**覆盖范围**。
- **TUN**：与 Clash TUN 配合时只开 Clash 的 TUN，FDU 的 `tun_mode` 和 `add_route` 保持关闭（GUI 不开放此开关）。单独使用 FDU TUN 见[配置参数](docs/config.md#fdu-tun-模式)。
- **回环**：手动配置分流时，需将 VPN 底层连接绑定到物理网卡，参考上游的[相关讨论](https://github.com/Mythologyli/zju-connect/issues/57)。
- **权限**：规则只决定流量路径，不会扩大学校授予的资源权限。按域名分流需要应用或 Clash 提供域名，只有 IP 的连接请添加 IP 规则。

## 常见问题

<details>
<summary>运行要求、登录浏览器、配置目录与升级</summary>

- **运行要求**：Windows 需要 WebView2 Runtime；macOS 应用未经公证，首次打开需在 Finder 中右键选择“打开”；Linux 需要 GTK 4 和 WebKitGTK 6.0。
- **登录浏览器**：UIS 自动登录需要 Edge、Chrome、Chromium 或 Brave，优先使用兼容的默认浏览器，无需粘贴回调地址。
- **自动识别失败**：改用手动接入；客户端不具备所需能力时改用仅本地代理。
- **端口被占用**：自动／手动接入会在启用前改用空闲端口；仅本地代理需自行调整监听端口。其他错误见**诊断**页。
- **配置目录**：Windows 通常在程序目录，不可写时使用 `%APPDATA%\FDU Connect`（已有用户目录继续沿用）；macOS 为 `~/Library/Application Support/FDU Connect`；Linux 为 `$XDG_CONFIG_HOME/fdu-connect`，未设置时为 `~/.config/fdu-connect`。
- **升级**：保留上述目录即可保留配置和登录状态。关闭窗口后程序可能仍在托盘运行，覆盖程序前请先退出。
- **托盘**：部分 Linux 桌面不显示托盘图标，因此 Linux 上关闭窗口默认直接退出，可在设置中改为最小化到托盘。

</details>

## 更多文档

- [配置参数](docs/config.md)：命令行与 TOML 配置项、选择性分流、FDU TUN
- [从源码构建](docs/build.md)：GUI 构建与打包、命令行核心、服务与 Docker
- [开发计划](docs/roadmap.md)

## 致谢

- [Mythologyli/zju-connect](https://github.com/Mythologyli/zju-connect)：本分支的直接上游。
- [AkiraSalvare/fdu-connect](https://github.com/AkiraSalvare/fdu-connect)：复旦适配的参考项目。
- [EasierConnect](https://github.com/lyc8503/EasierConnect)（已停止维护）：ZJU Connect 的基础，作者 [lyc8503](https://github.com/lyc8503)。
- [socks2http](https://github.com/zenhack/socks2http)
- [![image](docs/yxvm.png)](https://yxvm.com/) [NodeSupport](https://github.com/NodeSeekDev/NodeSupport) 赞助了上游项目。

感谢上游及原项目的所有贡献者：

<a href="https://github.com/mythologyli/zju-connect/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=mythologyli/zju-connect" />
</a>
