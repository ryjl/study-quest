# StudyQuest TV (tv-android)

Android TV 原生客户端,Kotlin + Jetpack Compose for TV + Media3 ExoPlayer。

## 为什么是原生(不是 Flutter)

PAD/手机端用 Flutter(`../frontend/`),但 TV 端换原生,因为:
- 业界主流 TV APP(腾讯视频 TV / 爱奇艺 TV / 网易爆米花 TV)全部原生 Kotlin/Java
- Flutter Focus 系统对 TV D-pad 场景支持不成熟(框架层问题,非代码问题)
- Google 官方推荐 Compose for TV(Leanback 已废弃)

详见 `../docs/handoff-tv-flavor.md`。

## 技术栈

| 层 | 技术 | 版本 |
|---|---|---|
| UI | Compose for TV(tv-material)+ Compose BOM | 1.0.0 / 2025.02.00 |
| 播放器 | Media3 ExoPlayer + ui-compose + datasource-okhttp | 1.6.0-beta01 |
| DI | Hilt | 2.60 |
| 网络 | Retrofit + OkHttp + kotlinx-serialization | 2.11.0 / 4.12.0 / 1.8.0 |
| 导航 | navigation-compose | 2.8.8 |
| 图片 | Coil | 2.7.0 |
| 存储 | EncryptedSharedPreferences | 1.1.0-alpha06 |
| 构建 | AGP / Kotlin / Gradle | 9.0.1 / 2.3.10 / 9.1.0 |

工具链版本对齐 `../frontend/` Flutter Android 工程(同 monorepo 统一),Kotlin 降一个 patch(KSP 滞后于 Kotlin 主版本)。

## 目录结构

```
tv-android/
├── app/src/main/java/com/revin/studyquest/tv/
│   ├── MainActivity.kt              # 单 Activity 入口
│   ├── StudyQuestApp.kt             # Application + Hilt
│   ├── data/                        # 数据层(网络+存储+DTO)
│   │   ├── remote/ (ApiService[Retrofit] / NetworkModule[Hilt] / dto/)
│   │   ├── local/TokenStore.kt      # EncryptedSharedPreferences
│   │   └── repo/Repositories.kt     # Auth/Course/UrlResolver
│   ├── domain/                      # 纯业务规则(跨端契约,见 docs/business-rules.md)
│   │   ├── TrackSelection.kt        # 字幕合并 + 音轨
│   │   ├── ChapterGrouper.kt        # 章节分组
│   │   └── ProgressRules.kt         # 进度上报防作弊决策
│   ├── player/                      # ExoPlayer 基础设施
│   │   ├── NetdiskHttpFactory.kt    # 网盘 Referer 注入(115 直链鉴权)
│   │   ├── Media3Module.kt          # Hilt 提供播放器专用 OkHttpClient
│   │   ├── ProgressReporter.kt      # 进度上报定时器(复刻 domain.ProgressRules)
│   │   └── ResumeWatchdog.kt        # 断点续播(CDN 回零重 seek)
│   └── ui/
│       ├── theme/ (Color/Type/Shape/Gradient/StudyQuestTheme)
│       ├── components/ (TvIconButton/FocusGlow/LoadingScreen/ErrorScreen)
│       ├── nav/AppNav.kt            # 路由表 + 登录态守卫
│       ├── auth/ (LoginScreen + PIN 键盘)
│       ├── home/ CourseHallScreen   # 课程大厅
│       ├── player/ VideoPlayerScreen # 播放器(seek+字幕+控制层)
│       ├── ai/ AiStudyScreen        # AI 学习页(只读)
│       ├── footprint/ FootprintScreen
│       └── settings/ SettingsScreen
└── app/src/test/                    # domain 层单测(33 用例)
```

## 构建

```bash
# debug APK(开发调试,applicationId 带 .debug 后缀)
make build-tv-apk-debug
# 或直接:
cd tv-android && ./gradlew :app:assembleDebug

# release APK
make build-tv-apk

# 装到已连接的 TV 设备(adb)
make install-tv
```

产物:`app/build/outputs/apk/debug/app-debug.apk`

**环境要求**:JDK 17、Android SDK(compileSdk 36)、`ANDROID_HOME` 指向 SDK 目录。

## 运行

1. **后端**:先启动 Go 后端(`make run` from 仓库根),确保 `:8080` 可达。
2. **TV 设备**:需要 Android TV 模拟器或真机(必须有 `leanback` feature —— 本 APK `leanback required=true`,普通手机装不上)。
3. **首次启动**:进设置页配置后端 IP(局域网地址,如 `http://192.168.1.100:8080`)。
4. **登录**:选用户 → 输 4 位 PIN(遥控器数字键盘)。

## 跨端契约

TV 端与 PAD 端(Flutter)共用业务规则和视觉 token,契约文档在 `../docs/`:
- **`docs/business-rules.md`** —— 业务规则(字幕合并/章节分组/进度上报/断点续播/字号档位/网盘鉴权)
- **`docs/design-tokens.md`** —— 视觉 token(色板/渐变/字体/圆角/焦点发光环)

改任一契约 → 改文档 → 两端同步。

## 砍掉的功能(TV 不要)

对照 PAD,TV 竍砍掉:阅读室(readingRoom)、错题本(wrongBook)、考试、quiz 做题、OTA 自更新(后续做)。

AI 学习页 TV 端**只读**(看 summary + advice,无 quiz)。

## 已知限制 / TODO

- **字幕/音轨/速度菜单**:播放器控制行目前只有播放/暂停按钮,菜单 UI 待补(底层 TrackSelection 逻辑已就绪)。
- **课程详情页**:点课程卡直接跳播放器(用 courseId 兜底),详情页(选集/章节)待建。
- **足迹数据**:学习时长/完成数/时间线已接 API,徽章墙待补(badge 结构复杂)。
- **Quicksand 字体**:暂用系统默认,后续接 res/font 或 Downloadable Fonts。
- **OTA**:暂未实现(后端契约需加 flavor 维度,见 `../docs/handoff-tv-flavor.md`)。
