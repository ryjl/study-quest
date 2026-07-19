import 'dart:async';
import 'package:flutter/material.dart';
import 'package:media_kit/media_kit.dart';
import 'package:provider/provider.dart';
import 'config.dart';
import 'service/auth_service.dart';
import 'service/tv_mode.dart';
import 'service/ui_prefs.dart';
import 'theme.dart';
import 'ui/screen/login_screen.dart';
import 'ui/screen/main_navigation.dart';

void main() async {
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

  final authService = AuthService();
  await authService.init();

  // 全局异常捕获(长期保留):Flutter 渲染阶段(build/layout/paint)的 silent
  // exception 默认在 release 模式下无输出。重写 onError 让它 print 到 logcat,
  // 用于诊断 MarkdownView 渲染表格/SVG 时可能抛的隐性异常。
  // 用 print 不是 debugPrint——release 模式 debugPrint 被吞。
  FlutterError.onError = (details) {
    FlutterError.presentError(details);
    // ignore: avoid_print
    print('🔥 FlutterError: ${details.exception}');
    // ignore: avoid_print
    print(details.stack.toString());
  };

  // runZonedGuarded 兜底异步未处理异常(Timer/Future 里抛的),补全 onError 覆盖不到的。
  runZonedGuarded(() {
    runApp(
      MultiProvider(
        providers: [
          ChangeNotifierProvider<AuthService>.value(value: authService),
        ],
        child: const StudyQuestApp(),
      ),
    );
  }, (error, stack) {
    // ignore: avoid_print
    print('🔥 Uncaught: $error');
    // ignore: avoid_print
    print(stack.toString());
  });
}

class StudyQuestApp extends StatelessWidget {
  const StudyQuestApp({Key? key}) : super(key: key);

  @override
  Widget build(BuildContext context) {
    final auth = Provider.of<AuthService>(context);

    return MaterialApp(
      title: 'StudyQuest',
      theme: AppTheme.lightTheme,
      debugShowCheckedModeBanner: false,
      home: auth.isAuthenticated ? const MainNavigation() : const LoginScreen(),
    );
  }
}
