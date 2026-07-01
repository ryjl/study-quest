import 'dart:convert';
import 'package:http/http.dart' as http;
import '../config.dart';
import '../model/user.dart';
import '../model/course.dart';
import '../model/progress.dart';

class ApiService {
  // Common headers builder
  static Map<String, String> _headers([int? activeUserId]) {
    final Map<String, String> hdrs = {
      'Content-Type': 'application/json',
      'Accept': 'application/json',
    };
    if (activeUserId != null && activeUserId > 0) {
      hdrs['X-User-ID'] = activeUserId.toString();
    }
    return hdrs;
  }

  // 1. Fetch public users list (for profile selection screen)
  static Future<List<User>> fetchUsers() async {
    final response = await http.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/users'),
      headers: _headers(),
    );
    if (response.statusCode == 200) {
      final List<dynamic> list = jsonDecode(response.body);
      return list.map((e) => User.fromJson(e)).toList();
    }
    throw Exception('获取用户列表失败: ${response.statusCode}');
  }

  // 2. Perform PIN authentication
  static Future<bool> loginUser(int userId, String pin) async {
    final response = await http.post(
      Uri.parse('${AppConfig.baseUrl}/api/v1/users/login'),
      headers: _headers(),
      body: jsonEncode({
        'user_id': userId,
        'pin': pin,
      }),
    );
    if (response.statusCode == 200) {
      final Map<String, dynamic> data = jsonDecode(response.body);
      return data.containsKey('token') || data['status'] == 'success' || data['authenticated'] == true;
    }
    return false;
  }

  // 3. Fetch courses authorized for this student
  static Future<List<Course>> fetchCourses(int activeUserId) async {
    final response = await http.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/courses'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      final List<dynamic> list = jsonDecode(response.body);
      return list.map((e) => Course.fromJson(e)).toList();
    }
    throw Exception('获取课程库失败: ${response.statusCode}');
  }

  // 4. Fetch episodes for a given course
  static Future<List<Episode>> fetchEpisodes(int activeUserId, int courseId) async {
    final response = await http.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/courses/$courseId/episodes'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      final List<dynamic> list = jsonDecode(response.body);
      return list.map((e) => Episode.fromJson(e)).toList();
    }
    throw Exception('获取课时明细失败: ${response.statusCode}');
  }

  // 5. Load AI cards and quiz questions for an episode
  static Future<AILessonContent?> fetchAILesson(int activeUserId, int episodeId) async {
    final response = await http.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/episodes/$episodeId/ai-content'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      final data = jsonDecode(response.body);
      // Backend returns null or empty JSON if no cards exist
      if (data == null || data['EpisodeID'] == 0 || data['episode_id'] == 0) {
        return null;
      }
      return AILessonContent.fromJson(data);
    }
    return null; // Gracefully fallback to skip blockers
  }

  // 6. Fetch play info (resolves direct streaming URL and custom HTTP headers for netdisk bypass)
  static Future<Map<String, dynamic>> fetchPlayInfo(int activeUserId, int episodeId) async {
    final response = await http.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/episodes/$episodeId/play-info'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      final Map<String, dynamic> data = jsonDecode(response.body);
      return data;
    }
    throw Exception('获取视频播放地址及请求头失败: ${response.statusCode}');
  }

  // 7. Report progress updates and sync watch hours
  static Future<UserProgress> reportProgress({
    required int activeUserId,
    required int episodeId,
    required int positionSeconds,
    required int deltaWatchSeconds,
  }) async {
    final response = await http.post(
      Uri.parse('${AppConfig.baseUrl}/api/v1/progress/report'),
      headers: _headers(activeUserId),
      body: jsonEncode({
        'episode_id': episodeId,
        'position_seconds': positionSeconds,
        'delta_watch_seconds': deltaWatchSeconds,
      }),
    );
    if (response.statusCode == 200) {
      return UserProgress.fromJson(jsonDecode(response.body));
    }
    throw Exception('汇报播放进度失败: ${response.statusCode}');
  }

  // 8. Fetch user points ledger total balance
  static Future<UserPoint> fetchUserPoints(int activeUserId) async {
    final response = await http.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/progress/points'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      return UserPoint.fromJson(jsonDecode(response.body));
    }
    throw Exception('获取积分余额失败: ${response.statusCode}');
  }

  // 9. Fetch user progress list overview
  static Future<List<UserProgress>> fetchProgressOverview(int activeUserId) async {
    final response = await http.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/progress'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      final List<dynamic> list = jsonDecode(response.body);
      return list.map((e) => UserProgress.fromJson(e)).toList();
    }
    throw Exception('获取进度列表失败: ${response.statusCode}');
  }
}
