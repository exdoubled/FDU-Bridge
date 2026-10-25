# 从源码构建

中文 | [English](build_en.md)

## Windows GUI

安装 [gui/go.mod](../gui/go.mod) 要求的 Go 版本、Node.js 和 pnpm，在仓库根目录运行：

```powershell
.\build.ps1
```

构建脚本会同时生成 `dist\FDU-Connect-GUI.exe` 和 `fdu-connect.exe` 核心，两者需放在同一目录。构建前先退出该目录中的程序；也可用 `-OutputDirectory` 输出到其他目录。运行 `./build.ps1 -Test` 可同时执行现有测试。

打包还需安装 [NSIS 3](https://nsis.sourceforge.io/Download)，确保能够找到 `makensis.exe`，也可通过 `NSIS_MAKENSIS` 指定其绝对路径。运行 `./build.ps1 -Package` 会生成 Windows x64 安装程序和更新压缩包，不会收录个人配置或登录缓存：

- `FDU-Connect-Windows-x64-setup.exe`：首次分发用的安装程序，包含 GUI、核心、示例配置、许可证及微软官方 WebView2 x64 完整离线安装器。安装向导在安装阶段按需处理依赖，默认安装到 `%LOCALAPPDATA%\Programs\FDU Connect`，并创建开始菜单快捷方式和卸载项。
- `FDU-Connect-Windows-x64.zip`：应用内更新用的精简包，包含相同的 GUI、核心、示例配置和许可证，需要已有运行库。打包时检查此包不超过升级器的 256 MiB 下载上限。

指定 `-Version vX.Y.Z` 时，文件名中的 `FDU-Connect` 后会加上版本号。打包时会下载 WebView2 安装器，校验其微软签名并记录 SHA256，再嵌入安装程序；应用启动时只检查依赖，缺失时提示重新运行安装包。卸载仅删除程序文件，保留配置、规则、登录状态及共享运行库。首次导入 Clash 时会带入示例中的分流规则，已有规则文件保持原样。

不带 `-Package` 的开发构建不需要 NSIS，也不下载离线安装器，需要本机已有 WebView2 Runtime。向首次使用的用户分发时请使用 `*-Windows-x64-setup.exe`。

## macOS／Linux GUI

需要与 Windows 相同的 Go、Node.js 和 pnpm。macOS 另需 Xcode 命令行工具（`xcode-select --install`）；Debian／Ubuntu 另需：

```bash
sudo apt install build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev
```

在仓库根目录运行 `bash build.sh`，可加 `--test`、`--package`，含义与 Windows 相同。

- macOS 生成 `dist/FDU Connect.app`，同时支持 Apple 芯片和 Intel。应用未经公证，首次打开需在 Finder 中右键选择“打开”。
- Linux 生成 `dist/fdu-connect-gui` 和核心 `dist/fdu-connect`，两者需放在同一目录。

启用流程与 Windows 相同，不改系统代理、绕过列表或 Clash TUN。部分 Linux 桌面默认不显示托盘图标，因此 Linux 上关闭窗口默认直接退出，可在设置中改为最小化到托盘。

## 命令行核心

Go 版本需满足 [go.mod](../go.mod)。首次使用时，将 [config.toml.example](../config.toml.example) 另存为 `config.toml`；已有配置可继续使用。

```powershell
.\fdu-connect.exe -config .\config.toml
```

以上为 Windows 命令。Linux/macOS 使用 `go build -o fdu-connect .` 构建，再运行 `./fdu-connect -config ./config.toml`，按终端提示完成登录。

配置使用 `vpn.fudan.edu.cn:443`，认证类型为 `auth/httpsOauth2`，登录域为 `id.fudan.edu.cn`。安装兼容浏览器时可自动完成 UIS 回调，否则按终端提示操作，无需把 UIS 密码写入配置。

示例默认独立运行，提供本地代理，分流功能保持关闭。启动后，将应用的 SOCKS5 代理设为 `127.0.0.1:1080`，或 HTTP 代理设为 `127.0.0.1:1081`。修改监听地址和端口见[配置参数](config.md)。

请显式传入 `-config`：程序的内置默认值仍沿用上游，复旦配置以 [config.toml.example](../config.toml.example) 为准。其他学校的用户建议参考原版 [ZJU Connect](https://github.com/Mythologyli/zju-connect)。

## 作为服务运行

参考上游的[服务运行说明](service.md)，其中的可执行文件路径和配置文件需换成本分支的实际路径。

## Docker 运行

参考上游的 [Docker 说明](docker.md)。文中的镜像属于上游，不包含本分支新增功能；运行本分支需从当前源码构建镜像。桌面浏览器自动捕获和管理窗口不适用于容器。
