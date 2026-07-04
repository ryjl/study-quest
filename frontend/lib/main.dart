import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'config.dart';
import 'service/auth_service.dart';
import 'theme.dart';
import 'ui/screen/login_screen.dart';
import 'ui/screen/main_navigation.dart';

void main() async {
  WidgetsFlutterBinding.ensureInitialized();
  
  // Initialize configurations and persistence services
  await AppConfig.init();
  
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
