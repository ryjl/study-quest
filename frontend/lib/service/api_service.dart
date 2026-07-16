import 'dart:convert';
import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;
import '../config.dart';
import '../model/user.dart';
import '../model/course.dart';
import '../model/subject.dart';
import '../model/tag.dart';
import '../model/progress.dart';
import '../model/badge.dart';
import '../model/reading.dart';
import '../model/quiz.dart';

class ApiService {
  /// Opaque session token issued by the backend at login. Carried in the
  /// `Authorization: Bearer <token>` header on every authenticated request.
  /// Set by [loginUser], cleared by [logout], persisted by AuthService.
  static String? authToken;

  /// Invoked once when any authenticated request comes back 401 (token expired
  /// or revoked by admin). AuthService registers itself here so the whole app
  /// returns to the login screen instead of looping on dead credentials.
  /// Calling this is best-effort: if it throws, the error is swallowed so it
  /// never masks the original 401 the caller will surface.
  static Future<void> Function()? onUnauthorized;

  /// The HTTP client used for all requests. Defaults to a real client; tests
  /// swap in a MockClient via [bindTestClient]. Routing through a single
  /// injectable client is what makes the 401 / token plumbing testable without
  /// monkey-patching package:http.
  static http.Client _httpClient = http.Client();

  /// Test-only: replace the HTTP client (e.g. with a MockClient). Reset to the
  /// real client in tearDown by calling [resetTestClient].
  @visibleForTesting
  static void bindTestClient(http.Client client) {
    _httpClient = client;
  }

  /// Test-only: restore the real HTTP client. Call in tearDown to avoid
  /// leaking a mock across tests.
  @visibleForTesting
  static void resetTestClient() {
    _httpClient = http.Client();
  }

  /// Common headers builder. activeUserId is retained as a parameter for URL
  /// construction in callers but is NO LONGER used for auth — auth is solely
  /// the Bearer token below. The legacy X-User-ID header is intentionally not
  /// sent (the backend rejects it).
  static Map<String, String> _headers([int? activeUserId]) {
    final Map<String, String> hdrs = {
      'Content-Type': 'application/json',
      'Accept': 'application/json',
    };
    if (authToken != null && authToken!.isNotEmpty) {
      hdrs['Authorization'] = 'Bearer $authToken';
    }
    return hdrs;
  }

  /// Fire-and-forget 401 hook. Called from each authenticated method's failure
  /// path when the server says the session is gone. Wrapped so a misbehaving
  /// callback can't replace the real error.
  static void _signalUnauthorizedIfNeeded(int status) {
    if (status == 401 && onUnauthorized != null) {
      // Don't await — callers want to surface their own exception, not block
      // on the logout flow.
      onUnauthorized!().catchError((_) {});
    }
  }

  /// Centralized failure handler: fires the 401 hook (if applicable), then
  /// throws the caller-supplied message. Every authenticated method routes its
  /// non-200 path through here so the session-expiry signal can't drift out of
  /// sync with the error paths.
  static Never _fail(int statusCode, String message) {
    _signalUnauthorizedIfNeeded(statusCode);
    throw Exception(message);
  }

  // 1. Fetch public users list (for profile selection screen)
  static Future<List<User>> fetchUsers() async {
    try {
      final response = await _httpClient.get(
        Uri.parse('${AppConfig.baseUrl}/api/v1/users'),
        headers: _headers(),
      ).timeout(const Duration(seconds: 4));
      if (response.statusCode == 200) {
        final List<dynamic> list = jsonDecode(response.body);
        return list.map((e) => User.fromJson(e)).toList();
      }
      _fail(response.statusCode, '获取用户列表失败: ${response.statusCode}');
    } catch (e) {
      throw Exception('无法连接到服务器，请检查局域网连接或配置正确的服务器 IP。');
    }
  }

  // 2. Perform PIN authentication. Returns the opaque session token on
  // success (and stores it in [authToken] for subsequent requests), or null on
  // any failure. deviceName is the OS-level device label surfaced to the admin
  // device list (see DeviceInfoService).
  static Future<String?> loginUser(int userId, String pin, {String? deviceName}) async {
    final body = <String, dynamic>{
      'user_id': userId,
      'pin': pin,
    };
    if (deviceName != null && deviceName.isNotEmpty) {
      body['device_name'] = deviceName;
    }
    final response = await _httpClient.post(
      Uri.parse('${AppConfig.baseUrl}/api/v1/users/login'),
      headers: _headers(),
      body: jsonEncode(body),
    );
    if (response.statusCode == 200) {
      final Map<String, dynamic> data = jsonDecode(response.body);
      final tok = data['token'] as String?;
      if (tok != null && tok.isNotEmpty) {
        authToken = tok;
        return tok;
      }
    }
    return null;
  }

  /// Logout: ask the server to revoke the current token, then clear it locally
  /// regardless of the server response (a network failure must not strand a
  /// bad token in memory). Idempotent.
  static Future<void> logout() async {
    try {
      await _httpClient.post(
        Uri.parse('${AppConfig.baseUrl}/api/v1/users/logout'),
        headers: _headers(),
      ).timeout(const Duration(seconds: 4));
    } catch (_) {
      // Swallow: local clear below is what matters for the client.
    }
    // Clear locally regardless of the server response (a network failure must
    // not strand a bad token in memory). Done AFTER the request so the
    // Authorization header above carries the real token.
    authToken = null;
  }

  // 3. Fetch courses authorized for this student. Pass contentType to filter
  // by learning (default) or entertainment.
  static Future<List<Course>> fetchCourses(int activeUserId, {String contentType = 'learning'}) async {
    final response = await _httpClient.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/courses?content_type=$contentType'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      final List<dynamic> list = jsonDecode(response.body);
      return list.map((e) => Course.fromJson(e)).toList();
    }
    _fail(response.statusCode, '获取课程库失败: ${response.statusCode}');
  }

  // 3b. Fetch the subject catalog (for filter chips + card labels/gradients).
  // Requires the student's auth header since /api/v1/subjects is restricted.
  static Future<List<Subject>> fetchSubjects(int activeUserId) async {
    final response = await _httpClient.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/subjects'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      final List<dynamic> list = jsonDecode(response.body);
      return list.map((e) => Subject.fromJson(e as Map<String, dynamic>)).toList();
    }
    // Non-fatal: callers fall back to the raw subject key string.
    return const [];
  }

  // 3c. Fetch the tag catalog (for multi-select filter chips). Tags are
  // DB-driven and editable from the admin Tags page.
  static Future<List<Tag>> fetchTags(int activeUserId) async {
    final response = await _httpClient.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/tags'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      final List<dynamic> list = jsonDecode(response.body);
      return list.map((e) => Tag.fromJson(e as Map<String, dynamic>)).toList();
    }
    return const [];
  }

  // 4. Fetch episodes for a given course
  static Future<List<Episode>> fetchEpisodes(int activeUserId, int courseId) async {
    final response = await _httpClient.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/courses/$courseId/episodes'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      final List<dynamic> list = jsonDecode(response.body);
      return list.map((e) => Episode.fromJson(e)).toList();
    }
    _fail(response.statusCode, '获取课时明细失败: ${response.statusCode}');
  }

  // AI summary + quiz. These hit the Step-3 LLM-generated endpoints.
  // The summary is read once; the quiz is lazily generated on first GET
  // (returns status=generating, caller polls).

  static Future<EpisodeSummary?> fetchEpisodeSummary(int activeUserId, int episodeId) async {
    final response = await _httpClient.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/episodes/$episodeId/ai-summary'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      return EpisodeSummary.fromJson(jsonDecode(response.body) as Map<String, dynamic>);
    }
    if (response.statusCode == 404) return null; // no summary yet / AI off
    _fail(response.statusCode, '获取总结失败: ${response.statusCode}');
  }

  static Future<QuizResponse> fetchEpisodeQuiz(int activeUserId, int episodeId) async {
    final response = await _httpClient.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/episodes/$episodeId/ai-quiz'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200 || response.statusCode == 202) {
      return QuizResponse.fromJson(jsonDecode(response.body) as Map<String, dynamic>);
    }
    if (response.statusCode == 404) {
      return QuizResponse(status: QuizStatus.unavailable);
    }
    _fail(response.statusCode, '获取练习失败: ${response.statusCode}');
  }

  static Future<QuizAnswerResult> submitQuizAnswer({
    required int activeUserId,
    required int episodeId,
    required int questionId,
    int? answerIndex,
    String? answerText,
  }) async {
    final body = <String, dynamic>{'question_id': questionId};
    if (answerIndex != null) body['answer_index'] = answerIndex;
    if (answerText != null && answerText.isNotEmpty) body['answer_text'] = answerText;
    final response = await _httpClient.post(
      Uri.parse('${AppConfig.baseUrl}/api/v1/episodes/$episodeId/ai-quiz/submit'),
      headers: _headers(activeUserId),
      body: jsonEncode(body),
    );
    if (response.statusCode == 200) {
      return QuizAnswerResult.fromJson(jsonDecode(response.body) as Map<String, dynamic>);
    }
    _fail(response.statusCode, '提交答案失败: ${response.statusCode}');
  }

  /// Phase B 统一交卷:一次提交全部题,后端逐题判分 + 锁定 quiz。
  /// answers 为一整张卷子的作答;选择题给 answerIndex,填空题给 answerText。
  /// 返回每题结果(顺序与后端 quiz 题序一致)。已交卷(409)时抛异常。
  static Future<List<QuizAnswerResult>> submitAllQuizAnswers({
    required int activeUserId,
    required int episodeId,
    required List<Map<String, dynamic>> answers,
  }) async {
    final response = await _httpClient.post(
      Uri.parse('${AppConfig.baseUrl}/api/v1/episodes/$episodeId/ai-quiz/submit-all'),
      headers: _headers(activeUserId),
      body: jsonEncode({'answers': answers}),
    );
    if (response.statusCode == 200) {
      final body = jsonDecode(response.body);
      final list = (body is Map && body['results'] is List) ? body['results'] as List : const [];
      return list.map((e) => QuizAnswerResult.fromJson(e as Map<String, dynamic>)).toList();
    }
    if (response.statusCode == 409) {
      _fail(response.statusCode, '这套题已交卷,不能重复提交');
    }
    _fail(response.statusCode, '提交失败: ${response.statusCode}');
  }

  static Future<void> regenerateQuiz(int activeUserId, int episodeId) async {
    final response = await _httpClient.post(
      Uri.parse('${AppConfig.baseUrl}/api/v1/episodes/$episodeId/ai-quiz/regenerate'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode != 202 && response.statusCode != 200) {
      _fail(response.statusCode, '重新生成失败: ${response.statusCode}');
    }
  }

  // Phase 3 — archived quiz history (read-only). Returns the user's superseded
  // quiz generations for an episode; each is fully revealed (correct answers
  // shown). Empty list when there's no history yet (normal before first regen).
  static Future<List<ArchivedQuizView>> fetchQuizHistory(int activeUserId, int episodeId) async {
    final response = await _httpClient.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/episodes/$episodeId/ai-quiz/history'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      final body = jsonDecode(response.body);
      final list = (body is Map && body['history'] is List) ? body['history'] as List : const [];
      return list
          .map((e) => ArchivedQuizView.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    if (response.statusCode == 404) return const []; // AI off / no history
    _fail(response.statusCode, '获取历史练习失败: ${response.statusCode}');
  }

  // 6. Fetch play info (resolves direct streaming URL and custom HTTP headers for netdisk bypass)
  static Future<PlayInfo> fetchPlayInfo(int activeUserId, int episodeId) async {
    final response = await _httpClient.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/episodes/$episodeId/play-info'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      final Map<String, dynamic> data = jsonDecode(response.body);
      return PlayInfo.fromJson(data);
    }
    _fail(response.statusCode, '获取视频播放地址及请求头失败: ${response.statusCode}');
  }

  // 6b. Fetch attachments list (real PDF / supplementary materials) for an episode.
  // The backend stores these as a JSON array of relative storage paths, so we
  // wrap each entry into an [Attachment] carrying its index for stream URL use.
  static Future<List<Attachment>> fetchAttachments(int activeUserId, int episodeId) async {
    final response = await _httpClient.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/episodes/$episodeId/attachments'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      final List<dynamic> list = jsonDecode(response.body);
      final result = <Attachment>[];
      for (var i = 0; i < list.length; i++) {
        final entry = list[i];
        if (entry is Map) {
          result.add(Attachment.fromJson({
            'index': i,
            'path': entry['path'] ?? entry['name'] ?? '',
          }));
        } else {
          result.add(Attachment(index: i, path: entry.toString()));
        }
      }
      return result;
    }
    _fail(response.statusCode, '获取课时附件失败: ${response.statusCode}');
  }

  /// Resolve a backend-relative subtitle/attachment URL to an absolute one,
  /// so media_kit / url_launcher can reach it from the device.
  static String absoluteUrl(String relativeOrAbsolute) {
    if (relativeOrAbsolute.isEmpty) return relativeOrAbsolute;
    if (relativeOrAbsolute.startsWith('http://') ||
        relativeOrAbsolute.startsWith('https://')) {
      return relativeOrAbsolute;
    }
    if (relativeOrAbsolute.startsWith('/')) {
      return AppConfig.baseUrl + relativeOrAbsolute;
    }
    return AppConfig.baseUrl + '/' + relativeOrAbsolute;
  }

  // 7. Report progress updates and sync watch hours
  static Future<UserProgress> reportProgress({
    required int activeUserId,
    required int episodeId,
    required int positionSeconds,
    required int deltaWatchSeconds,
  }) async {
    final response = await _httpClient.post(
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
    _fail(response.statusCode, '汇报播放进度失败: ${response.statusCode}');
  }

  // 8. Fetch user points ledger total balance
  static Future<UserPoint> fetchUserPoints(int activeUserId) async {
    final response = await _httpClient.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/progress/points'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      return UserPoint.fromJson(jsonDecode(response.body));
    }
    _fail(response.statusCode, '获取积分余额失败: ${response.statusCode}');
  }

  // 9. Fetch user progress list overview
  static Future<List<UserProgress>> fetchProgressOverview(int activeUserId) async {
    final response = await _httpClient.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/progress'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      final List<dynamic> list = jsonDecode(response.body);
      return list.map((e) => UserProgress.fromJson(e)).toList();
    }
    _fail(response.statusCode, '获取进度列表失败: ${response.statusCode}');
  }

  // 10. Fetch chapters for a course (real chapter tree, replaces mock split)
  static Future<List<Chapter>> fetchChapters(int activeUserId, int courseId) async {
    final response = await _httpClient.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/courses/$courseId/chapters'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      final List<dynamic> list = jsonDecode(response.body);
      return list.map((e) => Chapter.fromJson(e)).toList();
    }
    _fail(response.statusCode, '获取章节列表失败: ${response.statusCode}');
  }

  // 11. Fetch points ledger (transaction history) for the growth-footprint screen
  static Future<List<PointsLedger>> fetchPointsLedger(
    int activeUserId, {
    int limit = 20,
    int offset = 0,
  }) async {
    final response = await _httpClient.get(
      Uri.parse(
          '${AppConfig.baseUrl}/api/v1/progress/ledger?limit=$limit&offset=$offset'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      final List<dynamic> list = jsonDecode(response.body);
      return list.map((e) => PointsLedger.fromJson(e)).toList();
    }
    _fail(response.statusCode, '获取积分流水失败: ${response.statusCode}');
  }

  // 12. Fetch all badges with their unlocked state for the current user
  static Future<List<BadgeStatus>> fetchUserBadges(int activeUserId) async {
    final response = await _httpClient.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/users/$activeUserId/badges'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      final List<dynamic> list = jsonDecode(response.body);
      return list
          .map((e) => BadgeStatus.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    _fail(response.statusCode, '获取成就徽章失败: ${response.statusCode}');
  }

  // ── Reading Room ──

  /// Fetch the aggregated reading-room shelf view (series + standalone books/articles).
  static Future<ReadingRoomView> fetchReadingRoom(int activeUserId) async {
    final response = await _httpClient.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/readings'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      return ReadingRoomView.fromJson(jsonDecode(response.body));
    }
    _fail(response.statusCode, '获取阅读室失败: ${response.statusCode}');
  }

  /// Fetch a series with its child books and articles.
  static Future<ReadingSeriesDetail> fetchReadingSeries(
    int activeUserId,
    int seriesId,
  ) async {
    final response = await _httpClient.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/readings/series/$seriesId'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      return ReadingSeriesDetail.fromJson(jsonDecode(response.body));
    }
    _fail(response.statusCode, '获取阅读系列失败: ${response.statusCode}');
  }

  /// Fetch the last-read page of a PDF book (returns 0 if no progress yet).
  static Future<int> fetchBookProgress(int activeUserId, int bookId) async {
    final response = await _httpClient.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/readings/books/$bookId/progress'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      final data = jsonDecode(response.body);
      return (data['lastPage'] ?? data['last_page'] ?? 0) as int;
    }
    _fail(response.statusCode, '获取阅读进度失败: ${response.statusCode}');
  }

  /// Report the current page of a PDF book (page memory).
  static Future<void> reportBookProgress({
    required int activeUserId,
    required int bookId,
    required int lastPage,
  }) async {
    final response = await _httpClient.post(
      Uri.parse('${AppConfig.baseUrl}/api/v1/readings/books/$bookId/progress'),
      headers: _headers(activeUserId),
      body: jsonEncode({'lastPage': lastPage}),
    );
    if (response.statusCode != 200) {
      _fail(response.statusCode, '汇报阅读进度失败: ${response.statusCode}');
    }
  }

  /// Fetch a single article (for the WebView reader).
  static Future<ReadingArticle> fetchArticle(
    int activeUserId,
    int articleId,
  ) async {
    final response = await _httpClient.get(
      Uri.parse('${AppConfig.baseUrl}/api/v1/readings/articles/$articleId'),
      headers: _headers(activeUserId),
    );
    if (response.statusCode == 200) {
      return ReadingArticle.fromJson(jsonDecode(response.body));
    }
    _fail(response.statusCode, '获取文章失败: ${response.statusCode}');
  }

  /// Build the PDF stream URL for a book (302 → Alist direct link).
  /// The PDF reader downloads via http with the X-User-ID header, then opens
  /// the local cached file with pdfrx (not PdfDocumentRefUri).
  static String bookStreamUrl(int bookId) {
    return '${AppConfig.baseUrlRef}/api/v1/readings/books/$bookId/stream';
  }
}
