# StudyQuest — Frontend(Flutter 学生端)

学生端 App,跑在 Android PAD(也兼容手机/桌面)。看课程视频、闯关做题、
和 AI 一起复盘学习弱点。

## 技术栈

- **Flutter / Dart**
- **media_kit**(libmpv 后端)做视频播放 —— 比 `video_player` 更稳,TV/PAD 通吃
- **pdfrx**(pdfium 后端)看讲义 PDF —— 不用 `syncfusion_flutter_pdfviewer`,
  因为 syncfusion 与 AGP9 不兼容
- **printing** —— 阅读室的"打印 PDF"按钮(试卷/绘本 → 纸质),走 Android PrintManager
- TV D-Pad 焦点导航 + Nintendo Switch / Duolingo 风格 UI(大圆角、响应式主题)

> 注:TV 端是**独立的 Kotlin 原生工程**(`tv-android/`,Compose for TV),
> 不在本目录。本目录只做 PAD/手机/桌面。

## 开发

```bash
cd frontend
flutter pub get
flutter run                       # 连真机/模拟器跑

flutter analyze                   # 静态检查
flutter test                      # dart 测试
```

构建 release APK 走仓库根的 Makefile:`make build-apk`(按 ABI split)。

## 代码布局

`model/` / `service/` / `ui/{screen,widget,ai}/` 分层。
`service/api_service.dart` 是唯一 HTTP 出口(OTA 更新、PDF 流等少数场景例外)。
跨层契约(Go json tag ↔ TS 接口 ↔ Dart 类)是手维护的,详见根 `CLAUDE.md`。
