# 配置参数

中文 | [English](config_en.md)

本文列出命令行核心的参数，供手动运行核心或对照 GUI **设置 → 高级设置**时参考。完整示例见 [config.toml.example](../config.toml.example)，运行方式见[从源码构建](build.md#命令行核心)。

## 通用参数

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

+ `tun-mode`: TUN 模式（实验性）。见下方 [FDU TUN 模式](#fdu-tun-模式)

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

## EasyConnect 相关参数

+ `cert-file`: p12 证书文件路径，如果服务器要求证书验证，需要配置此参数

+ `cert-password`: 证书密码 (敏感信息建议使用环境变量 `ZJU_CONNECT_CERT_PASSWORD` 给定)

+ `skip-domain-resource`: 不使用服务端下发的域名资源分流，一般不需要加此参数

+ `disable-multi-line`: 禁用自动根据延时选择线路。加此参数后，使用 `server` 参数指定的线路

+ `disable-zju-config`: 禁用 ZJU 相关配置，非 ZJU 用户可能需要添加此参数

+ `custom-proxy-domain`: 指定自定义域名使用 RVPN 代理，格式为 `域名,域名,...`，例如 `nature.com,science.org`。多个域名用 `,` 分隔

+ `twf-id`: twfID 登录，调试用途，一般不需要加此参数

## aTrust 相关参数

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

## 选择性分流配置

以下为 TOML 配置文件的 `[routing]` 配置项；完整示例见 [config.toml.example](../config.toml.example)。

GUI 会自动生成专用监听和规则文件，并自动检测底层网卡；在“校内资源”中管理域名和 IP 即可。以下配置供手动使用命令行核心时参考。

+ `enabled`: 是否启用选择性分流，默认关闭。需使用 aTrust，并配置 `bind_interface` 或 `auto_detect_interface`，保持 `tun_mode`、`add_route`、`dns_hijack`、`fake_ip` 关闭

+ `domains`: 精确匹配的域名，不包含协议、端口或路径

+ `domain_suffixes`: 域名后缀，同时匹配该域名本身及其子域名

+ `ip_cidrs`: IPv4 网段，只填写需要走 FDU 的资源。内联规则中的单个 IP 请写为 `/32`；GUI 规则文件也接受单个 IPv4 地址

+ `upstream_proxy`: 启用分流时必填，填写自己的 Clash 专用 SOCKS5 监听地址。必须使用本机回环 IP 和实际端口，该监听需固定转发到外网组；默认留空

+ `rules_file`: 可选的 JSON 规则文件路径，设置后替代以上三类内联规则，并在保存后自动加载。可通过桌面 GUI 管理每条规则的启用状态，新规则对新连接生效

## FDU TUN 模式

1. **与 Clash TUN 配合分流时，只开启 Clash 的系统 TUN，FDU 的 `tun_mode` 和 `add_route` 保持关闭。** 本分支使用进程内网络栈访问校内资源，无需额外创建 FDU 虚拟网卡。
2. 以下说明适用于单独启用 FDU TUN 的通用模式，不能与本分支的选择性分流同时启用。
3. 单独启用 FDU TUN 需要管理员权限；Windows 系统还需要从 [Wintun 官网](https://www.wintun.net)下载 `wintun.dll` 并放在可执行文件同目录。
4. aTrust 单独使用 TUN 时，建议配合 `dns-hijack` 和 `fake-ip`，以便正确处理域名资源。
