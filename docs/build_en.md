# Build from source

[中文](build.md) | English

## Windows GUI

Install the Go version required by [gui/go.mod](../gui/go.mod), Node.js, and pnpm, then run from the repository root:

```powershell
.\build.ps1
```

The build script produces `dist\FDU-Connect-GUI.exe` and its `fdu-connect.exe` core; keep them in the same directory. Exit programs running from the output directory before building, or use `-OutputDirectory` to choose another directory. Use `./build.ps1 -Test` to also run the existing tests.

Packaging also requires [NSIS 3](https://nsis.sourceforge.io/Download). Ensure `makensis.exe` can be found, or set `NSIS_MAKENSIS` to its absolute path. Use `./build.ps1 -Package` to create a Windows x64 setup executable and update ZIP. Personal configurations and login caches are excluded:

- `FDU-Connect-Windows-x64-setup.exe`: setup for first installations, containing the GUI, core, example configuration, license, and Microsoft's full WebView2 x64 offline installer. The wizard handles the dependency during installation, defaults to `%LOCALAPPDATA%\Programs\FDU Connect`, and creates Start menu shortcuts and an uninstall entry.
- `FDU-Connect-Windows-x64.zip`: the lightweight in-app update package, containing the same GUI, core, example configuration, and license. It requires an existing Runtime. Packaging checks that this ZIP stays within the updater's 256 MiB download limit.

With `-Version vX.Y.Z`, the version is inserted after `FDU-Connect` in both filenames. Packaging downloads the WebView2 installer, verifies its Microsoft signature, records its SHA256, then embeds it in setup. Application startup only checks the dependency and prompts users to rerun setup if it is missing. Uninstall removes program files while preserving configuration, rules, login state, and the shared Runtime. The first Clash import carries over the example routing rules; existing rule files are preserved.

A development build without `-Package` does not require NSIS or download the offline installer, and requires an existing WebView2 Runtime. Distribute `*-Windows-x64-setup.exe` to first-time users.

## macOS / Linux GUI

The same Go, Node.js, and pnpm are required. macOS also needs the Xcode command line tools (`xcode-select --install`); Debian/Ubuntu also need:

```bash
sudo apt install build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev
```

Run `bash build.sh` from the repository root; `--test` and `--package` work as on Windows.

- macOS produces `dist/FDU Connect.app` for both Apple silicon and Intel. The app is not notarized, so open it the first time by right-clicking it in Finder and choosing Open.
- Linux produces `dist/fdu-connect-gui` and the core `dist/fdu-connect`; keep them in the same directory.

Enable campus access as on Windows; the GUI leaves system proxies, bypass lists and Clash TUN unchanged. Some Linux desktops hide tray icons, so closing the window quits by default on Linux; enable "minimize to tray" in Settings to change it.

## Command-line core

Use the Go version required by [go.mod](../go.mod). For the first run, save [config.toml.example](../config.toml.example) as `config.toml`. Existing configurations can be reused.

```powershell
.\fdu-connect.exe -config .\config.toml
```

These are Windows commands. On Linux/macOS, build with `go build -o fdu-connect .`, then run `./fdu-connect -config ./config.toml` and follow the terminal prompts to log in.

The configuration uses `vpn.fudan.edu.cn:443`, authentication type `auth/httpsOauth2`, and login domain `id.fudan.edu.cn`. With a compatible browser installed, UIS callbacks can be captured automatically; otherwise follow the terminal prompts. There is no need to store the UIS password in the configuration.

The example runs independently as a local proxy, with selective routing disabled. Once started, set your application's SOCKS5 proxy to `127.0.0.1:1080`, or its HTTP proxy to `127.0.0.1:1081`. See the [configuration reference](config_en.md) to change the listening addresses or ports.

Pass `-config` explicitly: the program's built-in defaults still follow upstream. Use [config.toml.example](../config.toml.example) for Fudan settings. Users from other universities should refer to the original [ZJU Connect](https://github.com/Mythologyli/zju-connect).

## Run as a service

Refer to the upstream [service instructions](service_en.md). Replace the executable path and configuration file with the actual paths used by this fork.

## Run in Docker

Refer to the upstream [Docker instructions](docker_en.md). The images documented there belong to upstream and do not include this fork's additions. To run this fork, build an image from the current source. Desktop browser callback capture and the management window are not available in containers.
