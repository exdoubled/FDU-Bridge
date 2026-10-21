# FDU Connect

> 🚫 **Disclaimer**
>
> This program is provided **as is**, and the author **does not guarantee the correctness or reliability of the program**. Please judge whether the specific scenario is suitable for using this program. **The problems or consequences caused by using this program are borne by the user**!

---

[中文](README.md) | English

This program builds on the aTrust implementation in [ZJU Connect](https://github.com/Mythologyli/zju-connect), with reference to the Fudan adaptation in [AkiraSalvare/fdu-connect](https://github.com/AkiraSalvare/fdu-connect). Thanks to the original authors [AkiraSalvare](https://github.com/AkiraSalvare), [Mythologyli](https://github.com/Mythologyli), and the contributors to the original projects.

**ZJU Connect is based on [EasierConnect](https://github.com/lyc8503/EasierConnect) (no longer maintained). Thanks to its original author [lyc8503](https://github.com/lyc8503).**

This fork targets Fudan University's aTrust / UIS login. It adds browser callback capture, selective routing, and a Windows management window. VPN protocols, local proxy services, and other general capabilities are inherited from upstream.

**Upstream QQ group: 1037726410**, welcome to join the discussion.

## New adaptation features

- Complete UIS login in an isolated Edge / Chrome window and capture the authentication callback automatically.
- Route selected exact domains, domain suffixes, IPv4 addresses, and subnets through FDU; forward other application traffic to Clash's Internet nodes.
- Switch Internet nodes, add or remove rules, and enable or disable individual rules in a Windows management window.
- Reload saved rules automatically. New connections use the updated rules; existing connections keep their original routes.

## Usage

### FDU client

Use the Go version required by [go.mod](go.mod). For the first run, save [config.toml.example](config.toml.example) as `config.toml`. Existing configurations can be reused.

```powershell
.\fdu-connect.exe -config .\config.toml
```

These are Windows commands. On Linux/macOS, build with `go build -o fdu-connect .`, then run `./fdu-connect -config ./config.toml` and follow the terminal prompts to log in.

The configuration uses `vpn.fudan.edu.cn:443`, authentication type `auth/httpsOauth2`, and login domain `id.fudan.edu.cn`. On Windows, complete UIS authentication in the login window. There is no need to store the UIS password in the configuration.

The example runs independently as a local proxy, with selective routing disabled. Once started, set your application's SOCKS5 proxy to `127.0.0.1:1080`, or its HTTP proxy to `127.0.0.1:1081`. See the argument descriptions to change the listening addresses or ports.

Pass `-config` explicitly: the program's built-in defaults still follow upstream. Use [config.toml.example](config.toml.example) for Fudan settings. Users from other universities should refer to the original [ZJU Connect](https://github.com/Mythologyli/zju-connect).

### Run as a service

Refer to the upstream [service instructions](docs/service_en.md). Replace the executable path and configuration file with the actual paths used by this fork.

### Run in Docker

Refer to the upstream [Docker instructions](docs/docker_en.md). The images documented there belong to upstream and do not include this fork's additions. To run this fork, build an image from the current source. Windows browser callback capture and the management window are not available in containers.

## ⚠️ Warning

1. When using Clash TUN, configure routing correctly and bind VPN underlay connections to a physical interface to avoid proxy loops. See the upstream [discussion](https://github.com/Mythologyli/zju-connect/issues/57).
2. In selective routing mode, selected destinations use FDU; other TCP/UDP traffic entering the proxy is forwarded to `routing.upstream_proxy`. This upstream must be a dedicated Clash listener pinned to `INTERNET`, and must not route back to `GLOBAL` or FDU.
3. Rules determine traffic routes and do not extend campus resource permissions. Domain routing requires the application or Clash to provide the domain name. Add IP rules for connections that provide only IP addresses.

## ⚠️ TUN mode precautions

1. **When routing with Clash TUN, enable only Clash's system TUN and keep FDU's `tun_mode` and `add_route` disabled.** This fork uses a userspace network stack to access campus resources without creating another FDU virtual adapter.
2. The following notes apply to ordinary mode with FDU's own TUN enabled. This mode cannot be combined with this fork's selective routing.
3. FDU TUN requires administrator privileges. On Windows, download `wintun.dll` from the [Wintun website](https://www.wintun.net) and place it next to the executable.
4. When using aTrust with FDU's own TUN, enable `dns-hijack` and `fake-ip` to handle domain resources correctly.

## Arguments

### General Arguments

+ `protocol`: Login protocol, either `easyconnect` or `atrust`. The built-in default is `easyconnect`; the Fudan example configuration uses `atrust`

+ `server`: VPN server address. Fudan aTrust uses `vpn.fudan.edu.cn`. Pass the Fudan configuration explicitly; without it, upstream defaults still apply

+ `port`: VPN server port, default is `443`

+ `username`: Network account, such as a student ID. Leave blank for Fudan UIS browser login

+ `password`: Network account password. Leave blank for Fudan UIS browser login. For other password-based authentication, use the `ZJU_CONNECT_PASSWORD` environment variable

+ `graph-code-file`: Graphic captcha file path, empty by default. In aTrust mode, leave blank to complete the captcha in a browser. If a path is set, the program saves the captcha image there and prompts for JSON input in the terminal

+ `totp-secret`: TOTP secret, can be used to automatically complete TOTP verification. If the server doesn't require TOTP or you want to manually enter the code, leave blank. (Sensitive information is recommended to be provided via env `ZJU_CONNECT_TOTP_SECRET`)

+ `disable-remote-dns`: Disable remote DNS and use local DNS instead, generally no need to add this argument

+ `disable-server-config`: Disable server configuration, generally no need to add this argument

+ `socks-bind`: SOCKS5 proxy listening address. The built-in default is `:1080`; the Fudan example configuration uses `127.0.0.1:1080`

+ `socks-user`: SOCKS5 proxy username, leave blank if no authentication is required

+ `socks-passwd`: SOCKS5 proxy password, leave blank if no authentication is required

+ `http-bind`: HTTP proxy listening address. The built-in default is `:1081`; the Fudan example configuration uses `127.0.0.1:1081`. Set to `""` to disable the HTTP proxy

+ `shadowsocks-url`: Shadowsocks server URL. For example: `ss://aes-128-gcm:password@server:port`. Format [refer to here](https://github.com/shadowsocks/go-shadowsocks2)

+ `dial-direct-proxy`: Upstream proxy for destinations that do not match VPN rules in ordinary mode. Supports `http://` and `socks://`, for example `http://127.0.0.1:7890`. Use `routing.upstream_proxy` in selective routing mode

+ `tcp-tunnel-mode`: TCP tunnel mode, default is `false`. When enabled, only TCP traffic can be proxied through the TCP tunnel. Since only aTrust supports TCP tunneling, this mode is ineffective under EasyConnect. Enabling this will disable TUN mode

+ `tun-mode`: TUN mode (experimental). Read the TUN mode precautions

+ `add-route`: Add routes according to the configuration issued by the server when TUN mode is enabled

+ `dns-ttl`: DNS cache time, default is `3600` seconds

+ `disable-keep-alive`: Disable periodic keep-alive, generally no need to add this argument

+ `keep-alive-url`: Uses HTTP keep-alive, suitable for situations where the server does not provide DNS. Set the URL to visit, for example, `https://www.cnki.net/favicon.ico`. The default is empty, in which case the server-provided DNS is used for keep-alive.

+ `remote-dns-server`: Remote DNS server address, default is `auto`. Set to `auto` to use the DNS server obtained from the server; disable remote DNS if it fails to obtain

+ `secondary-dns-server`: Standby DNS server used when the remote DNS server cannot resolve. The default `auto` uses the second server supplied by VPN policy, then falls back to `114.114.114.114`. Leave blank to use system default DNS, but it must be set when `dns-hijack` is enabled

+ `dns-server-bind`: DNS server listening address, default is empty (disabled). For example, set to `127.0.0.1:53`, then you can send DNS requests to `127.0.0.1:53`

+ `local-dns-server`: Local DNS server used to resolve the VPN server hostname, as IP or IP:port. Leave blank to use system DNS. Routable DNS addresses are bound to the underlay interface after a successful probe; local DNS stubs keep their loopback route

+ `dns-hijack`: Hijack DNS requests when TUN mode is enabled, it's recommended to add this argument when using TUN mode

+ `fake-ip`: Enable Fake IP together with `dns-hijack`. Recommended when using aTrust with TUN mode. Has no effect with the EasyConnect protocol

+ `debug-dump`: Whether to enable debugging, generally no need to add this argument

+ `debug-pcap-file`: Reconstruct a PCAP from data read and written on VPN underlay TCP connections; a full capture queue blocks network I/O, kernel handshakes and retransmissions are omitted, and TLS payloads remain encrypted

+ `debug-tls-log-file`: Export TLS session secrets in NSS key log format for decrypting `debug-pcap-file` traffic in Wireshark. The file contains session secrets and must only be used for debugging and stored securely

+ `bind-interface`: Manually bind VPN underlay connections to this network interface for either EasyConnect or aTrust. A non-empty value takes precedence over automatic detection.

+ `auto-detect-interface`: Automatically detect and bind the VPN underlay interface; defaults to `false`. Set it to `true` to enable automatic detection. If disabled and `bind-interface` is empty, underlay connections use system routing. **This feature may not work correctly while another VPN with Fake IP enabled is in use.**

+ `tcp-port-forwarding`: TCP port forwarding, format is `local address-remote address,local address-remote address,...`, for example `127.0.0.1:9898-10.10.98.98:80,0.0.0.0:9899-10.10.98.98:80`. Multiple forwardings are separated by `,`

+ `udp-port-forwarding`: UDP port forwarding, format is `local address-remote address,local address-remote address,...`, for example `127.0.0.1:53-10.10.0.21:53`. Multiple forwardings are separated by `,`

+ `custom-dns`: Custom DNS records in the format `domain:IP,domain:IP,...`, for example `host.internal.example:192.0.2.10`. Separate records with `,` and replace the example with an actual resource

+ `proxy-all`: Whether to proxy all traffic, generally no need to add this argument

+ `config`: Specify the configuration file, referring to `config.toml.example`

Configuration sources are merged with the precedence “built-in defaults < TOML file < environment variables < command-line flags”. Only explicitly supplied command-line flags override other sources; for example, `-tun-mode=false` disables a value enabled by the file or environment.

Every configuration key can be set with an uppercase environment variable prefixed by `ZJU_CONNECT_`. For example, `server_address` maps to `ZJU_CONNECT_SERVER_ADDRESS`, and `tun_mode` maps to `ZJU_CONNECT_TUN_MODE`. Collection values use the same names and syntax as command-line flags, for example `ZJU_CONNECT_TCP_PORT_FORWARDING=127.0.0.1:9898-10.10.98.98:80`, `ZJU_CONNECT_CUSTOM_DNS=host.internal.example:192.0.2.10`, and `ZJU_CONNECT_CUSTOM_PROXY_DOMAIN=nature.com,science.org`.

The old `disable_zju_dns` and `zju_dns_server` names, including their command-line flags and environment variables, remain supported but are deprecated. Use `disable_remote_dns` and `remote_dns_server` instead.

### EasyConnect Related Arguments

+ `cert-file`: p12 certificate file path, if the server requires certificate verification, this parameter needs to be configured

+ `cert-password`: Certificate password. (Sensitive information is recommended to be provided via env `ZJU_CONNECT_CERT_PASSWORD`)

+ `skip-domain-resource`: Do not use the domain resource provided by the server for split tunneling, generally no need to add this argument

+ `disable-multi-line`: Disable automatic line selection based on latency. When added, use the line specified by the `server` parameter

+ `disable-zju-config`: Disable ZJU related configuration, non-ZJU users may need to add this argument

+ `custom-proxy-domain`: Specify custom domains to use RVPN proxy, format is `domain,domain,...`, for example `nature.com,science.org`. Multiple domains are separated by `,`

+ `twf-id`: twfID login, for debugging purposes, generally no need to add this argument

### aTrust Related Arguments

+ `auth-type`: aTrust authentication type. Supports `auth/psw` (password), `auth/cas` (CAS), `auth/smsCheckCode` (SMS verification), and `auth/httpsOauth2` (browser OAuth2 verification). Fudan UIS uses `auth/httpsOauth2`

+ `login-domain`: Login domain. The built-in default is `Radius`; the Fudan UIS configuration uses `id.fudan.edu.cn`

+ `client-data-file`: Client data file path, used to save login status to avoid repeated verification.

+ `cas-ticket`: CAS verification ticket. Empty by default, which triggers interactive verification

+ `oauth2-code`: Manually supply the code from the OAuth2 callback. Not required for automatic Fudan UIS login

+ `phone`: Phone number used for SMS verification code login.

+ `update-best-nodes-interval`: Interval for updating the optimal line automatically, in seconds, default is `300`. Set to `0` to disable automatic optimal line selection.

+ `session-refresh-interval`: aTrust session refresh interval in seconds, default `1800`. Set to `0` to disable periodic refresh

+ `auth-info`: Only get aTrust authentication information without logging in, generally no need to add this argument. Can be used to check supported authentication methods.

+ `trust-device`: Trust the current device (requires logged-in `-client-data-file`), does not start the tunnel.

+ `untrust-device`: Untrust the current device (requires logged-in `-client-data-file`), does not start the tunnel.

+ `sid`: aTrust SID, for debugging purposes, generally no need to add this argument.

+ `device-id`: aTrust device ID, for debugging purposes, generally no need to add this argument.

+ `sign-key`: aTrust signature key, for debugging purposes, generally no need to add this argument.

+ `resource-file`: aTrust resource file, for debugging purposes, generally no need to add this argument.

### Selective routing configuration

The following settings belong to the `[routing]` section of the TOML configuration file. See [config.toml.example](config.toml.example) for a complete example.

To use Clash TUN, configure a dedicated SOCKS5 listener, the underlay interface, and the campus resources to access before enabling selective routing. Interface names, listener ports, and resource permissions vary between users, so the example leaves these values empty. For management-window rules, save the file first and point `rules_file` to it; an absolute path is recommended.

+ `enabled`: Enable selective routing, disabled by default. Requires aTrust and either `bind_interface` or `auto_detect_interface`. Keep `tun_mode`, `add_route`, `dns_hijack`, and `fake_ip` disabled

+ `domains`: Exact domain names, without a scheme, port, or path

+ `domain_suffixes`: Domain suffixes, matching both the suffix itself and its subdomains

+ `ip_cidrs`: IPv4 subnets for resources that should use FDU. Use `/32` for a single IP in inline rules; management-window rules files also accept individual IPv4 addresses

+ `upstream_proxy`: Required when selective routing is enabled. Use your own dedicated Clash SOCKS5 listener with a literal loopback IP and its actual port, pinned to an Internet group. Empty by default

+ `rules_file`: Optional path to a JSON rules file. When set, it replaces the three inline rule lists above and reloads automatically after saving. Use the Windows management window to enable or disable individual rules. Updated rules apply to new connections


### Upstream contributors

<a href="https://github.com/mythologyli/zju-connect/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=mythologyli/zju-connect" />
</a>

### Acknowledgements

+ [Mythologyli/zju-connect](https://github.com/Mythologyli/zju-connect): direct upstream of this fork.

+ [AkiraSalvare/fdu-connect](https://github.com/AkiraSalvare/fdu-connect): reference project for the Fudan adaptation.

+ [EasierConnect](https://github.com/lyc8503/EasierConnect)

+ [socks2http](https://github.com/zenhack/socks2http)

+ [![image](docs/yxvm.png)](https://yxvm.com/)

  [NodeSupport](https://github.com/NodeSeekDev/NodeSupport) sponsored the upstream project
