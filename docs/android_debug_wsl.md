# Android 调试指南（WSL + MuMu 模拟器）

本文档说明在 **WSL2 里开发 Flutter、在 Windows 宿主机上跑 MuMu 模拟器** 的混合环境下，如何把 APK 推到模拟器里、如何看日志、以及如何在 WSL 里直接调 adb。

> 适用场景：开发者用 WSL 跑 Flutter 工具链（编译快、Linux 生态顺），但模拟器/真机接在 Windows 侧。

---

## 1. 环境拓扑

```
┌─────────────────────────────────┐     ┌──────────────────────────────┐
│  WSL2 (Linux)                   │     │  Windows 宿主机                │
│                                 │     │                              │
│  flutter / dart / go            │     │  MuMu 模拟器（Android）       │
│  编译 APK ←──── 你在这里工作     │     │   ↑ adb server 监听 5037      │
│                                 │     │   ↑ MuMu adb 端口 7555/16384  │
│  adb client（可选，见 §4）       │     │                              │
└─────────────────────────────────┘     └──────────────────────────────┘
              │                                        │
              └──── 通过 TCP 连 Windows 的 adb ─────────┘
```

**关键事实**：adb 是 C/S 架构。一台机器上只跑**一个** `adb server`（默认端口 5037）。WSL 和 Windows 各自的 adb client 必须连到**同一个** adb server，否则会互相 `kill-server` 打架。

最省心的方案：**adb server 只在 Windows 上跑**，WSL 用 `adb -H <windows_ip>` 远程连它。

---

## 2. Windows 侧：连上 MuMu 模拟器

### 2.1 安装 Platform Tools

下载 [Android Platform Tools](https://developer.android.com/tools/releases/platform-tools)，解压到例如 `C:\platform-tools`，加入 `PATH`。

### 2.2 连接 MuMu

MuMu 不同版本的 adb 端口不同。**MuMu Player 12** 默认 `16384`，**MuMu Player 6 / 经典版** 默认 `7555`。在 Windows 的 PowerShell / CMD 里：

```powershell
# 先确认 Windows 上的 adb server 在跑
adb start-server

# 连接 MuMu（12 版先试 16384，失败再试 7555）
adb connect 127.0.0.1:16384

# 验证
adb devices
# 应看到：emulator-xxxx  device   或   127.0.0.1:16384  device
```

> 如果 `adb devices` 为空或显示 `offline`，重启 adb server：`adb kill-server && adb start-server && adb connect 127.0.0.1:16384`。

### 2.3 拿到 Windows 的局域网 IP（给 WSL 用）

```powershell
ipconfig
# 找 "以太网适配器" 或 "无线局域网" 下的 IPv4 地址，例如 192.168.1.100
```

记下这个 IP，WSL 要连它。

---

## 3. Windows 侧：装 APK + 看日志（最常用）

这是日常 90% 的操作，**全程在 Windows PowerShell 里跑**，最稳。

### 3.1 安装 APK

WSL 编译产物路径（在 WSL 里看到的）：
```
/home/revin/repos/study-quest/frontend/build/app/outputs/flutter-apk/app-release.apk
```
对应 Windows 路径（WSL 文件系统在 Windows 里通过 `\\wsl$\` 访问）：
```
\\wsl$\<发行版名>\home\revin\repos\study-quest\frontend\build\app\outputs\flutter-apk\app-release.apk
```

在 Windows PowerShell 里直接装（用 `\\wsl$` 路径，不用拷来拷去）：
```powershell
adb install "\\wsl$\Ubuntu\home\revin\repos\study-quest\frontend\build\app\outputs\flutter-apk\app-release.apk"
```
> 替换 `Ubuntu` 为你的 WSL 发行版名（`wsl -l` 可查）。覆盖安装加 `-r`：`adb install -r "..."`。

### 3.2 看日志

```powershell
# 过滤 Flutter + StudyQuest 相关日志
adb logcat | Select-String "flutter|study_quest|media_kit|media_kit_video|pdfrx"

# 只看 crash
adb logcat *:E

# 清空日志缓冲后重新看
adb logcat -c ; adb logcat
```

### 3.3 启动 App

```powershell
adb shell am start -n com.revin.study_quest/.MainActivity
```

---

## 4. WSL 侧：直接调 adb（可选，进阶）

如果你想在 WSL 里直接跑 `adb install` / `adb logcat`（比如写脚本），有两种做法。

### 方案 A（推荐）：WSL adb 连 Windows 的 adb server

让 WSL 的 adb client 连到 Windows 上已经在跑的 adb server。Windows 侧需要让 adb server 监听所有网卡：

```powershell
# Windows 侧：让 adb server 监听 0.0.0.0:5037（默认只听 127.0.0.1）
# 设置环境变量后重启 server
$env:ADB_SERVER_SOCKET="0.0.0.0:5037"
adb kill-server
adb start-server
adb connect 127.0.0.1:16384   # Windows 自己也连上 MuMu
```

```bash
# WSL 侧：apt 装 adb
sudo apt install adb

# 连到 Windows 的 adb server（替换成你 Windows 的 IP）
export ADB_SERVER_SOCKET=tcp:192.168.1.100:5037

adb devices    # 应能看到 MuMu 设备
adb install build/app/outputs/flutter-apk/app-release.apk
adb logcat | grep -iE "flutter|study_quest|media_kit|pdfrx"
```

> 注意 WSL2 和 Windows 不共享 localhost，所以必须用 Windows 的局域网 IP，不能用 127.0.0.1。

### 方案 B：WSL 自己当 adb server，直连 MuMu

```bash
# WSL 里
adb kill-server
adb connect host.docker.internal:16384   # 或 Windows 局域网 IP:16384
```
> 缺点：Windows 和 WSL 两个 adb server 会抢，**不要在两边同时跑 adb**，否则反复 `kill-server`。

**建议统一用方案 A**，server 只在 Windows 跑。

---

## 5. Flutter 热重载调试（可选）

如果想边改边热重载到 MuMu，在 WSL 里：

```bash
cd /home/revin/repos/study-quest/frontend
# 让 flutter 用 Windows 的 adb server
export ANDROID_SDK_ROOT=/home/revin/android-sdk   # WSL 里的 SDK（仅用于 flutter 工具）
export ADB_SERVER_SOCKET=tcp:192.168.1.100:5037

flutter run -d <device-id>      # device-id 见 adb devices
```

> MuMu 上热重载偶尔不稳（模拟器 ARM/x86 翻译层），出问题就退回 `flutter build apk --debug` + `adb install`。

---

## 6. 常用速查

| 想做的事 | 命令（Windows PowerShell） |
|---------|---------------------------|
| 列设备 | `adb devices` |
| 装 APK | `adb install -r "\\wsl$\Ubuntu\home\revin\...\app-release.apk"` |
| 卸载 | `adb uninstall com.revin.study_quest` |
| 看日志 | `adb logcat \| Select-String "flutter\|study_quest"` |
| 启动 App | `adb shell am start -n com.revin.study_quest/.MainActivity` |
| 清日志 | `adb logcat -c` |
| 连 MuMu | `adb connect 127.0.0.1:16384` |
| 重启 adb | `adb kill-server && adb start-server` |

---

## 7. 踩坑记录

- **`adb devices` 在 WSL 和 Windows 显示不一致**：两边连了不同的 adb server。统一用方案 A（server 只在 Windows）。
- **`adb install` 报 `device offline`**：MuMu adb 端口变了（重启模拟器后可能换端口）。重新 `adb connect`，端口试 `16384` / `7555` / `5555`。
- **APK 装上后秒退**：检查 `adb logcat *:E`，常见是缺权限或 `minSdk` 不够。本项目用 media_kit，需 `minSdk ≥ 21`（Flutter 默认即满足）。
- **视频播放黑屏但有声音**：模拟器的硬件解码有问题。media_kit 在 MuMu 上若硬解失败，会自动降级软解；若仍黑屏，试真机。
- **MuMu 拿不到宿主机后端**：MuMu 里 `10.0.2.2` 指向**模拟器自己**，不是 Windows。真机/模拟器都要在 App「系统设置」里填 Windows 局域网 IP（如 `http://192.168.1.100:8080`）。
