import 'dart:async';
import 'package:flutter/material.dart';
import 'package:media_kit/media_kit.dart';
import 'package:provider/provider.dart';
import 'config.dart';
import 'service/auth_service.dart';
import 'service/theme_prefs.dart';
import 'service/tv_mode.dart';
import 'service/ui_prefs.dart';
import 'theme.dart';
import 'ui/screen/login_screen.dart';
import 'ui/screen/main_navigation.dart';

void main() {
  // runZonedGuarded 必须包住 ensureInitialized 和 runApp —— 否则 binding 初始化
  // 的 zone 和 runApp 的 zone 不一致,触发 "Zone mismatch" 警告(FlutterError:
  // The Flutter bindings were initialized in a different zone),会导致
  // zone-specific 配置(异常捕获等)行为不确定。把所有初始化都放 zone 内部,
  // 保证 binding 与 runApp 同 zone。
  runZonedGuarded(() async {
    WidgetsFlutterBinding.ensureInitialized();

    // Required by media_kit before any Player/VideoController is created.
    MediaKit.ensureInitialized();

    // Initialize configurations and persistence services
    await AppConfig.init();
    // 全局 UI 偏好(字幕字号、AI 页字号)——本地持久化,启动时读一次进内存,
    // 之后播放器/AI 页直接读 instance 字段,不再 await。详见 ui_prefs.dart。
    await UiPrefs.instance.load();
    // Android TV 模式检测(自动识别真机 TV + 调试强制开关)。启动时读一次进内存,
    // 之后 UI 各处直接读 TvMode.instance.isActive 判断走 PAD 还是 TV 布局。
    await TvMode.instance.load();
    // 主题偏好(浅色/深色/跟随系统)——读一次进内存,之后通过 provider 驱动
    // MaterialApp.themeMode;设置页改完 notifyListeners 即时生效。
    await ThemePrefs.instance.load();

    final authService = AuthService();
    await authService.init();

    runApp(
      MultiProvider(
        providers: [
          ChangeNotifierProvider<AuthService>.value(value: authService),
          ChangeNotifierProvider<ThemePrefs>.value(value: ThemePrefs.instance),
        ],
        child: const StudyQuestApp(),
      ),
    );
  }, (error, stack) {
    // 全局异常兜底:runZonedGuarded 捕获 Timer/Future 里抛的未处理异步异常,
    // 补全 FlutterError.onError(只覆盖渲染阶段)覆盖不到的部分。
    // 用 print 不是 debugPrint——release 模式 debugPrint 被吞。
    // ignore: avoid_print
    print('🔥 Uncaught: $error');
    // ignore: avoid_print
    print(stack.toString());
  });

  // 全局异常捕获(长期保留):Flutter 渲染阶段(build/layout/paint)的 silent
  // exception 默认在 release 模式下无输出。重写 onError 让它 print 到 logcat,
  // 用于诊断 MarkdownView 渲染表格/SVG 时可能抛的隐性异常。
  // 放在 runZonedGuarded 外面:onError 是全局静态回调,不依赖 zone,在哪注册都行。
  FlutterError.onError = (details) {
    FlutterError.presentError(details);
    // ignore: avoid_print
    print('🔥 FlutterError: ${details.exception}');
    // ignore: avoid_print
    print(details.stack.toString());
  };
}

class StudyQuestApp extends StatelessWidget {
  const StudyQuestApp({Key? key}) : super(key: key);

  @override
  Widget build(BuildContext context) {
    final auth = Provider.of<AuthService>(context);
    final themePrefs = Provider.of<ThemePrefs>(context);

    return MaterialApp(
      title: 'StudyQuest',
      theme: AppTheme.lightTheme,
      darkTheme: AppTheme.darkTheme,
      themeMode: themePrefs.themeMode,
      debugShowCheckedModeBanner: false,
      home: auth.isAuthenticated ? const MainNavigation() : const LoginScreen(),
    );
  }
}
