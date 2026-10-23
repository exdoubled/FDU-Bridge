# FDU Bridge

> 🚫 **Disclaimer**: This program is provided **as is**, and the author **does not guarantee its correctness or reliability**. Judge for yourself whether it suits your situation; **you bear any problems or consequences of using it**.

[中文](README.md) | English

A desktop campus-network tool for Fudan University. With a Clash/Mihomo client, selected campus resources use Fudan's aTrust tunnel while other traffic follows Clash's settings; it can also run on its own as a local SOCKS5/HTTP proxy. Built on [ZJU Connect](https://github.com/Mythologyli/zju-connect); see the acknowledgements at the end.

## Features

- Complete UIS login in a browser and capture the authentication callback automatically.
- Choose, entry by entry, which exact domains, domain suffixes, IPv4 addresses, or subnets use FDU. Saved rules reload automatically; new connections use them, existing connections keep their routes.
- Connect to Clash Verge Rev (including portable installs) with one switch, or to other Mihomo desktop clients. Global/rule mode and subscription changes are followed, and the outside exit is restored when disabled.
- Desktop GUI for Windows, macOS, and Linux with login, connection control, logs, Internet nodes, campus resources, and advanced settings.

## Quick start

1. Download the package for your system from [Releases](https://github.com/exdoubled/FDU-Bridge/releases) and extract it, keeping the GUI and core programs in the same directory.
2. Open your Clash client, pick a subscription and outside node, and use global or rule mode.
3. Launch the GUI (see the table) and confirm the target in **Settings → Proxy Integration (代理接入)**. If several clients are running, select one and save.
4. Enable **campus access (校内访问)** on Home, confirm the first-use prompt, and complete UIS login in the browser window.
5. In **Campus Resources (校内资源)**, add domains or IPs, enable entries, and save.

Campus Resources can import/export JSON with every entry and its enabled state. Preview an import, then merge or replace the current draft; click **Save (保存)** to apply it. Merge keeps the current draft's switches for existing entries. Export includes unsaved edits and excludes login information.

| System | Program |
| --- | --- |
| Windows | `FDU-Connect-GUI.exe` |
| macOS | `FDU Connect.app` |
| Linux | `fdu-connect-gui` |

In global mode, switch the outside exit on this app's **Internet Nodes (外网节点)** page; in rule mode, switch it in Clash. Change the mode and TUN in Clash; FDU follows. To quit, disable campus access on Home first, then quit Clash.

This README describes the current source; for downloaded packages, see the release notes of that version.

## Integration methods

Choose in **Settings → Proxy Integration (代理接入)**, save, then enable on Home. Disable the current connection before switching.

- **Automatic (自动接入)** (default): discover running clients and their local controllers. Integration pauses when the target cannot be identified uniquely.
- **Manual (手动接入)**: select the complete runtime YAML the client actually uses (a node-only subscription does not qualify) and optionally its controller and secret. The binding is reused, and mode changes and updates to that file are followed while connected.
- **Local proxy only (仅本地代理)**: no Clash needed. Point applications that need campus access at the SOCKS5/HTTP address shown in the window; they or an external client handle routing. This does not capture every application, and the Campus Resources and Internet Nodes pages do not apply.

Clash Verge Rev connects through its subscription extensions. Other clients need the Mihomo control API, rule providers, and dedicated listeners; discovery covers FlClash, Clash Party, Sparkle, Clash Nyanpasu, and ClashX.Meta. Otherwise use local proxy only. To install an extension yourself, copy the override script or YAML fragment (not a complete subscription) from **Settings → Export Extension (Optional) (导出扩展（可选）)**, and export again after changing FDU's listening ports.

**Settings → Advanced Settings (高级设置)** adjusts the server, authentication, DNS, network interface, local proxies, and port forwarding, or imports a TOML configuration; see the [configuration reference](docs/config_en.md). Clash integration currently requires aTrust; EasyConnect can use local proxy only. Options that conflict with Clash routing are disabled in the interface or rejected before enabling. Custom gateways and complex authentication combinations need your own verification.

## How it works

Clash receives traffic through TUN, the system proxy, or per-application proxies; FDU Bridge handles UIS authentication, the aTrust tunnel, and forwarding for selected resources, and the GUI syncs enabled campus rules through a local rule endpoint. When enabled, the GUI adds the `FDU-ROUTER` node, campus rules, and a dedicated return listener to Clash: for Clash Verge Rev it backs up and writes subscription extensions; for other clients it loads an in-memory overlay through the controller, leaving subscription files and private configuration untouched, and reconnects after the client reloads. The GUI does not change the system proxy, bypass lists, or the Clash TUN switch.

| Clash mode | Selected campus resources | Other traffic |
| --- | --- | --- |
| Global | Clash → FDU Bridge → aTrust tunnel → campus resource | Clash → FDU Bridge → dedicated Clash return listener → exit selected in `INTERNET` |
| Rule | Clash campus rules → FDU Bridge → aTrust tunnel → campus resource | Existing Clash rules and node selections, including proxy, direct, or reject |

In global mode, the return listener is pinned to `INTERNET` to avoid looping back through `GLOBAL`. FDU uses a userspace network stack without an extra virtual adapter; login and VPN underlay connections are bound to a physical interface so Clash TUN does not capture them again. In local proxy mode, applications connect to FDU directly, without the return listener or the GUI campus rules.

## Notes

- **Coverage**: Clash TUN covers all applications. Without it, only applications using Clash's proxy are covered, and campus addresses bypassed by the system proxy still connect directly; adjust those bypass rules yourself. Check **Coverage (覆盖范围)** on Home if access fails.
- **TUN**: with Clash TUN, enable only Clash's TUN and keep FDU's `tun_mode` and `add_route` off (the GUI does not expose them). For FDU's own TUN, see the [configuration reference](docs/config_en.md#fdu-tun-mode).
- **Loops**: when configuring routing manually, bind VPN underlay connections to a physical interface; see the upstream [discussion](https://github.com/Mythologyli/zju-connect/issues/57).
- **Permissions**: rules only choose traffic routes and do not extend campus resource permissions. Domain routing needs the application or Clash to provide the domain name; add IP rules for connections that use only IP addresses.

## FAQ

<details>
<summary>Requirements, login browser, data directories, and upgrades</summary>

- **Requirements**: Windows needs WebView2 Runtime. The macOS app is not notarized; open it the first time by right-clicking it in Finder and choosing Open. Linux needs GTK 4 and WebKitGTK 6.0.
- **Login browser**: automatic UIS login needs Edge, Chrome, Chromium, or Brave. A compatible default browser is preferred, and no callback URL needs pasting.
- **Recovery and expired login**: selective Clash connections retry transient failures automatically. An expired session pauses retries; click **Relogin (重新登录)** on Home to authenticate again. Disabling campus access stops retries and keeps rules and settings.
- **Discovery fails**: use manual integration, or local proxy only if the client lacks the required capabilities.
- **Port in use**: automatic/manual integration switches to free ports before enabling; in local proxy mode, adjust the listening ports yourself. See **Diagnostics (诊断)** for other errors.
- **Data directories**: Windows normally uses the program directory, falling back to `%APPDATA%\FDU Connect` when it is not writable (an existing per-user directory keeps being used). macOS uses `~/Library/Application Support/FDU Connect`. Linux uses `$XDG_CONFIG_HOME/fdu-connect`, or `~/.config/fdu-connect` when unset.
- **Upgrades**: keep those directories to keep your configuration and login state. Closing the window may leave the program in the tray; quit it before replacing the files.
- **Tray**: some Linux desktops hide tray icons, so closing the window quits by default on Linux; enable minimize to tray in Settings to change this.

</details>

## More documentation

- [Configuration reference](docs/config_en.md): command-line and TOML options, selective routing, FDU TUN
- [Build from source](docs/build_en.md): GUI builds and packages, command-line core, service, and Docker
- [Roadmap](docs/roadmap_en.md)

## Acknowledgements

- [Mythologyli/zju-connect](https://github.com/Mythologyli/zju-connect): direct upstream of this fork.
- [AkiraSalvare/fdu-connect](https://github.com/AkiraSalvare/fdu-connect): reference project for the Fudan adaptation.
- [EasierConnect](https://github.com/lyc8503/EasierConnect) (no longer maintained): the basis of ZJU Connect, by [lyc8503](https://github.com/lyc8503).
- [socks2http](https://github.com/zenhack/socks2http)
- [![image](docs/yxvm.png)](https://yxvm.com/) [NodeSupport](https://github.com/NodeSeekDev/NodeSupport) sponsored the upstream project.

Thanks to all contributors to the upstream and original projects:

<a href="https://github.com/mythologyli/zju-connect/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=mythologyli/zju-connect" />
</a>
