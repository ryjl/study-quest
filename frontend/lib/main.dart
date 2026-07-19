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

  runApp(
    MultiProvider(
      providers: [
        ChangeNotifierProvider<AuthService>.value(value: authService),
      ],
      child: const StudyQuestApp(),
    ),
  );
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
