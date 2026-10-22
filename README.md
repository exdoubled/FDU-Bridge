# FDU Connect

> 🚫 **免责声明**
>
> 本程序**按原样提供**，作者**不对程序的正确性或可靠性提供保证**，请使用者自行判断具体场景是否适合使用该程序，**使用该程序造成的问题或后果由使用者自行承担**！

---

中文 | [English](README_en.md)

本程序参考 [AkiraSalvare/fdu-connect](https://github.com/AkiraSalvare/fdu-connect) 的复旦适配，基于 [ZJU Connect](https://github.com/Mythologyli/zju-connect) 的 aTrust 实现继续开发，感谢原作者 [AkiraSalvare](https://github.com/AkiraSalvare)、[Mythologyli](https://github.com/Mythologyli) 以及原项目的贡献者。

**ZJU Connect 基于 [EasierConnect](https://github.com/lyc8503/EasierConnect)（现已停止维护）完成，感谢原作者 [lyc8503](https://github.com/lyc8503)。**

本分支面向复旦大学 aTrust／UIS 登录，新增浏览器回调捕获、选择性分流和桌面管理窗口（Windows、macOS、Linux）。VPN 协议、本地代理及其他通用能力继承自上游。

## 新适配功能

- 在独立的 Edge／Chrome／Chromium 窗口完成 UIS 登录，自动接收认证回调。
- 按精确域名、域名后缀、IPv4 或网段分流；指定目标走 FDU，其余进入 Clash 的流量沿用外网设置，流量在本应用启用后为：Clash -> FduConnect -> Clash -> 外网。
- 桌面 GUI 集成登录、连接启停、日志、外网节点、逐条规则开关及全部客户端设置，支持 Windows、macOS 和 Linux。
- 一键为现有 Clash （目前仅支持 Clash Verge Rev，后续会陆续适配）添加复旦内网，自动跟随全局／规则 / TUN 模式和订阅切换，保留外网设置，停用时恢复出口。
- 保存规则后自动加载，新连接使用新规则；已有连接保留原路径。

## 使用方法

### Windows GUI

安装 [gui/go.mod](gui/go.mod) 要求的 Go 版本、Node.js 和 pnpm，在仓库根目录运行：

```powershell
.\build.ps1
```

先启动 Clash Verge Rev 并选择订阅，再双击 `dist\FDU-Connect-GUI.exe`，在首页开启校内访问。首次确认后自动保存默认配置、备份并接入 Clash，然后在浏览器完成 UIS 登录。指定资源走 FDU，其他流量沿用 Clash；校内资源支持逐条开关，也可粘贴网址添加。高级 VPN 设置和诊断保留在窗口内。

构建脚本会同时生成 GUI 和 `fdu-connect.exe` 核心，两者需放在同一目录，无需单独构建或启动核心。配置、规则与登录状态保存在该目录，重新构建会保留；构建前先退出该目录中的程序。运行 `./build.ps1 -Test` 可同时执行现有测试。

运行 `./build.ps1 -Package` 可生成 Windows x64 便携压缩包，仅包含 GUI、核心、示例配置和许可证，不会收录个人配置或登录缓存。首次导入 Clash 时会带入示例中的分流规则，已有规则文件保持原样。

### macOS／Linux GUI

需要与 Windows 相同的 Go、Node.js 和 pnpm。macOS 另需 Xcode 命令行工具（`xcode-select --install`）；Debian／Ubuntu 另需：

```bash
sudo apt install build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev
```

在仓库根目录运行 `bash build.sh`，可加 `--test`、`--package`，含义与 Windows 相同。

- macOS 生成 `dist/FDU Connect.app`，同时支持 Apple 芯片和 Intel。应用未经公证，首次打开需在 Finder 中右键选择“打开”。
- Linux 生成 `dist/fdu-connect-gui` 和核心 `dist/fdu-connect`，两者需放在同一目录。

配置、规则与登录状态保存在 `~/Library/Application Support/FDU Connect`（macOS）或 `~/.config/fdu-connect`（Linux）。启用流程与 Windows 相同，不改系统代理、绕过列表或 Clash TUN。不开 TUN 时，仅接管进入 Clash 的应用流量。部分 Linux 桌面默认不显示托盘图标，因此 Linux 上关闭窗口默认直接退出，可在设置中改为最小化到托盘。

### 命令行核心（可选）

Go 版本需满足 [go.mod](go.mod)。首次使用时，将 [config.toml.example](config.toml.example) 另存为 `config.toml`；已有配置可继续使用。

```powershell
.\fdu-connect.exe -config .\config.toml
```

以上为 Windows 命令。Linux/macOS 使用 `go build -o fdu-connect .` 构建，再运行 `./fdu-connect -config ./config.toml`，按终端提示完成登录。

配置使用 `vpn.fudan.edu.cn:443`，认证类型为 `auth/httpsOauth2`，登录域为 `id.fudan.edu.cn`。Windows 下按弹出的登录窗口完成 UIS 认证，无需把 UIS 密码写入配置。

示例默认独立运行，提供本地代理，分流功能保持关闭。启动后，将应用的 SOCKS5 代理设为 `127.0.0.1:1080`，或 HTTP 代理设为 `127.0.0.1:1081`。修改监听地址和端口请参考参数说明。

请显式传入 `-config`：程序的内置默认值仍沿用上游，复旦配置以 [config.toml.example](config.toml.example) 为准。其他学校的用户建议参考原版 [ZJU Connect](https://github.com/Mythologyli/zju-connect)。

### 作为服务运行

参考上游的[服务运行说明](docs/service.md)，其中的可执行文件路径和配置文件需换成本分支的实际路径。

### Docker 运行

参考上游的 [Docker 说明](docs/docker.md)。文中的镜像属于上游，不包含本分支新增功能；运行本分支需从当前源码构建镜像。Windows 浏览器自动捕获和管理窗口不适用于容器。

## ⚠️ 警告

1. 与 Clash TUN 配合使用时，需要正确配置分流并将 VPN 底层连接绑定到物理网卡，避免流量回环，可参考上游的[相关讨论](https://github.com/Mythologyli/zju-connect/issues/57)。
2. GUI 自动接入当前全局或规则模式，无需手动选择前置模式或导入节点。全局模式下可在 GUI 切换外网节点；规则模式下沿用 Clash 现有规则和节点。停用时先恢复外网出口，再关闭 VPN。不开 Clash TUN 时，系统代理绕过的内网地址不会进入代理，需要自行调整绕过规则。
3. 规则只决定流量路径，不会扩大学校授予的资源权限。按域名分流需要应用或 Clash 提供域名，只有 IP 的连接请添加 IP 规则。

## ⚠️ TUN 模式注意事项

1. **与 Clash TUN 配合分流时，只开启 Clash 的系统 TUN，FDU 的 `tun_mode` 和 `add_route` 保持关闭。** 本分支使用进程内网络栈访问校内资源，无需额外创建 FDU 虚拟网卡。
2. 以下说明适用于单独启用 FDU TUN 的通用模式，不能与本分支的选择性分流同时启用。
3. 单独启用 FDU TUN 需要管理员权限；Windows 系统还需要从 [Wintun 官网](https://www.wintun.net)下载 `wintun.dll` 并放在可执行文件同目录。
4. aTrust 单独使用 TUN 时，建议配合 `dns-hijack` 和 `fake-ip`，以便正确处理域名资源。

## 参数说明

### 通用参数

+ `protocol`: 登录协议，支持 `easyconnect`/`atrust`。内置默认值为 `easyconnect`，复旦示例配置使用 `atrust`

+ `server`: VPN 服务端地址。复旦 aTrust 使用 `vpn.fudan.edu.cn`；请显式传入复旦配置，未传配置时仍沿用上游默认值

+ `port`: VPN 服务端端口，默认为 `443`

+ `username`: 网络账户，例如学号。复旦 UIS 浏览器登录时可留空

+ `password`: 网络账户密码。复旦 UIS 浏览器登录时可留空；其他密码认证可使用环境变量 `ZJU_CONNECT_PASSWORD` 给定

+ `graph-code-file`: 图形验证码文件路径。默认为空。在 aTrust 模式下，留空时使用浏览器完成验证码，设置路径则登录时会将图形验证码保存至该文件，由用户手动输入 JSON

+ `totp-secret`: TOTP 密钥，可用于自动完成 TOTP 验证。如服务端无需 TOTP 验证或希望手动输入验证码，可不填（敏感信息建议使用环境变量 `ZJU_CONNECT_TOTP_SECRET` 给定）

+ `disable-remote-dns`: 禁用远端 DNS 改用本地 DNS，一般不需要加此参数

+ `disable-server-config`: 禁用服务端配置，一般不需要加此参数

+ `socks-bind`: SOCKS5 代理监听地址，内置默认值为 `:1080`，复旦示例配置使用 `127.0.0.1:1080`

+ `socks-user`: SOCKS5 代理用户名，不填则不需要认证

+ `socks-passwd`: SOCKS5 代理密码，不填则不需要认证

+ `http-bind`: HTTP 代理监听地址，内置默认值为 `:1081`，复旦示例配置使用 `127.0.0.1:1081`。为 `""` 时不启用 HTTP 代理

+ `shadowsocks-url`: Shadowsocks 服务端 URL。例如：`ss://aes-128-gcm:password@server:port`。格式[参考此处](https://github.com/shadowsocks/go-shadowsocks2)

+ `dial-direct-proxy`: 普通模式下未命中 VPN 规则时使用的上游代理，支持 `http://` 和 `socks://`，例如 `http://127.0.0.1:7890`。选择性分流模式请使用 `routing.upstream_proxy`

+ `tcp-tunnel-mode`: TCP 隧道模式，默认为 `false`。启用后仅可通过 TCP 隧道代理 TCP 流量。由于只有 aTrust 支持 TCP 隧道，此模式在 EasyConnect 下无效。启用后会禁用 TUN 模式

+ `tun-mode`: TUN 模式（实验性）。请阅读 TUN 模式注意事项

+ `add-route`: 启用 TUN 模式时根据服务端下发配置添加路由

+ `dns-ttl`: DNS 缓存时间，默认为 `3600` 秒

+ `disable-keep-alive`: 禁用定时保活，一般不需要加此参数

+ `keep-alive-url`: 使用 HTTP 保活，适用于服务端不下发 DNS 的情况。填写要访问的 URL，例如 `https://www.cnki.net/favicon.ico` 。默认为空，此时使用服务端下发的 DNS 保活

+ `remote-dns-server`: 远端 DNS 服务器地址，默认为 `auto`。设置为 auto 时使用从服务端获取的 DNS 服务器，如果未能获取则禁用远端 DNS

+ `secondary-dns-server`: 当远端 DNS 无法解析时使用的备用服务器。默认值 `auto` 优先采用 VPN 策略下发的第二 DNS，否则回退到 `114.114.114.114`。留空则使用系统默认 DNS，但在开启 `dns-hijack` 时必须设置

+ `dns-server-bind`: DNS 服务器监听地址，默认为空即禁用。例如，设置为 `127.0.0.1:53`，则可向 `127.0.0.1:53` 发起 DNS 请求

+ `local-dns-server`: 指定用于解析 VPN 服务器域名的本地 DNS，格式为 IP 或 IP:port；留空时使用系统 DNS，可路由的 DNS 地址在探测成功后绑定到底层网卡，本地 DNS stub 保持 loopback 路由

+ `dns-hijack`: 启用 TUN 模式时劫持 DNS 请求，建议在启用 TUN 模式时添加此参数

+ `fake-ip`: 启用 Fake IP 功能，与 dns-hijack 配合使用，建议在使用 aTrust 协议并启用 TUN 模式时添加此参数。此参数在 EasyConnect 协议下无效

+ `debug-dump`: 是否开启调试，一般不需要加此参数

+ `debug-pcap-file`: 根据 VPN 底层 TCP 连接实际收发的数据重建 PCAP 文件，仅用于调试；捕获队列满时会阻塞网络读写，不包含内核握手和重传，TLS 内容仍为密文

+ `debug-tls-log-file`: 将 TLS 会话密钥导出为 NSS key log 格式，可配合 `debug-pcap-file` 在 Wireshark 中解密 TLS 流量。该文件包含会话密钥，仅用于调试并应妥善保管

+ `bind-interface`: 手动指定 VPN 底层连接使用的网卡接口，支持 EasyConnect 和 aTrust。非空时优先使用该接口，不再自动探测

+ `auto-detect-interface`: 自动探测并绑定 VPN 底层网卡，默认为 `false`。设为 `true` 时启用自动探测；未启用且未指定 `bind-interface` 时，底层连接使用系统路由。**若同时使用其他启用了 Fake IP 的 VPN，此功能可能无法正常工作**

+ `tcp-port-forwarding`: TCP 端口转发，格式为 `本地地址-远程地址,本地地址-远程地址,...`，例如 `127.0.0.1:9898-10.10.98.98:80,0.0.0.0:9899-10.10.98.98:80`。多个转发用 `,` 分隔

+ `udp-port-forwarding`: UDP 端口转发，格式为 `本地地址-远程地址,本地地址-远程地址,...`，例如 `127.0.0.1:53-10.10.0.21:53`。多个转发用 `,` 分隔

+ `custom-dns`: 指定自定义 DNS 解析结果，格式为 `域名:IP,域名:IP,...`，例如 `host.internal.example:192.0.2.10`。多个解析用 `,` 分隔，请替换为实际资源

+ `proxy-all`: 是否代理所有流量，一般不需要加此参数

+ `config`: 指定配置文件，内容参考 `config.toml.example`

配置按“内置默认值 < TOML 配置文件 < 环境变量 < 命令行参数”的优先级合并。只有显式提供的命令行参数会覆盖其他来源，例如 `-tun-mode=false` 可以关闭配置文件或环境变量中启用的选项。

配置项均可使用带 `ZJU_CONNECT_` 前缀的大写环境变量设置，例如 `server_address` 对应 `ZJU_CONNECT_SERVER_ADDRESS`，`tun_mode` 对应 `ZJU_CONNECT_TUN_MODE`。集合项使用与命令行参数相同的语法和名称，例如 `ZJU_CONNECT_TCP_PORT_FORWARDING=127.0.0.1:9898-10.10.98.98:80`、`ZJU_CONNECT_CUSTOM_DNS=host.internal.example:192.0.2.10` 和 `ZJU_CONNECT_CUSTOM_PROXY_DOMAIN=nature.com,science.org`。

旧名称 `disable_zju_dns`、`zju_dns_server` 及其命令行参数和环境变量仍然兼容，但已弃用；请改用 `disable_remote_dns` 和 `remote_dns_server`。

### EasyConnect 相关参数

+ `cert-file`: p12 证书文件路径，如果服务器要求证书验证，需要配置此参数

+ `cert-password`: 证书密码 (敏感信息建议使用环境变量 `ZJU_CONNECT_CERT_PASSWORD` 给定)

+ `skip-domain-resource`: 不使用服务端下发的域名资源分流，一般不需要加此参数

+ `disable-multi-line`: 禁用自动根据延时选择线路。加此参数后，使用 `server` 参数指定的线路

+ `disable-zju-config`: 禁用 ZJU 相关配置，非 ZJU 用户可能需要添加此参数

+ `custom-proxy-domain`: 指定自定义域名使用 RVPN 代理，格式为 `域名,域名,...`，例如 `nature.com,science.org`。多个域名用 `,` 分隔

+ `twf-id`: twfID 登录，调试用途，一般不需要加此参数

### aTrust 相关参数

+ `auth-type`: aTrust 登录验证类型，支持 `auth/psw`（密码验证）、`auth/cas`（CAS 验证）、`auth/smsCheckCode`（短信验证）和 `auth/httpsOauth2`（浏览器 OAuth2 验证）。复旦 UIS 使用 `auth/httpsOauth2`

+ `login-domain`: 登录域，内置默认值为 `Radius`，复旦 UIS 配置使用 `id.fudan.edu.cn`

+ `client-data-file`: 客户端数据文件路径，可用于保存登录状态，避免重复验证

+ `cas-ticket`: CAS 验证票据，默认为空，此时进入交互式验证

+ `oauth2-code`: 手动提供 OAuth2 回调中的 code；复旦 UIS 自动登录时无需填写

+ `phone`: 短信验证码登录时使用的手机号

+ `update-best-nodes-interval`: 自动选择最优线路的更新间隔，单位为秒，默认为 `300` 秒。设置为 `0` 则禁用自动选择最优线路

+ `session-refresh-interval`: aTrust 会话刷新间隔，单位为秒，默认为 `1800`。设置为 `0` 则禁用定时刷新。

+ `auth-info`: 仅获取 aTrust 验证信息而不登录，一般不需要加此参数。可用于查看服务端支持的验证方式

+ `trust-device`: 设置当前设备为授信终端（需要已登录的 `-client-data-file`），不启用隧道

+ `untrust-device`: 从授信终端中移除当前设备（需要已登录的 `-client-data-file`），不启用隧道

+ `sid`: aTrust SID，调试用途，一般不需要加此参数

+ `device-id`: aTrust 设备 ID，调试用途，一般不需要加此参数

+ `sign-key`: aTrust 签名密钥，调试用途，一般不需要加此参数

+ `resource-file`: aTrust 资源文件，调试用途，一般不需要加此参数

### 选择性分流配置

以下为 TOML 配置文件的 `[routing]` 配置项；完整示例见 [config.toml.example](config.toml.example)。

GUI 会自动生成专用监听和规则文件，并自动检测底层网卡；在“校内资源”中管理域名和 IP 即可。以下配置供手动使用命令行核心时参考。

+ `enabled`: 是否启用选择性分流，默认关闭。需使用 aTrust，并配置 `bind_interface` 或 `auto_detect_interface`，保持 `tun_mode`、`add_route`、`dns_hijack`、`fake_ip` 关闭

+ `domains`: 精确匹配的域名，不包含协议、端口或路径

+ `domain_suffixes`: 域名后缀，同时匹配该域名本身及其子域名

+ `ip_cidrs`: IPv4 网段，只填写需要走 FDU 的资源。内联规则中的单个 IP 请写为 `/32`；GUI 规则文件也接受单个 IPv4 地址

+ `upstream_proxy`: 启用分流时必填，填写自己的 Clash 专用 SOCKS5 监听地址。必须使用本机回环 IP 和实际端口，该监听需固定转发到外网组；默认留空

+ `rules_file`: 可选的 JSON 规则文件路径，设置后替代以上三类内联规则，并在保存后自动加载。可通过桌面 GUI 管理每条规则的启用状态，新规则对新连接生效


### 上游贡献者

<a href="https://github.com/mythologyli/zju-connect/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=mythologyli/zju-connect" />
</a>

### 感谢

+ [Mythologyli/zju-connect](https://github.com/Mythologyli/zju-connect)：本分支的直接上游。

+ [AkiraSalvare/fdu-connect](https://github.com/AkiraSalvare/fdu-connect)：复旦适配的参考项目。

+ [EasierConnect](https://github.com/lyc8503/EasierConnect)

+ [socks2http](https://github.com/zenhack/socks2http)

+ [![image](docs/yxvm.png)](https://yxvm.com/)

  [NodeSupport](https://github.com/NodeSeekDev/NodeSupport) 赞助了上游项目
