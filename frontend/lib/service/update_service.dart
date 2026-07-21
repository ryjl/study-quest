import 'dart:convert';
import 'dart:io';
import 'package:flutter/services.dart';
import 'package:http/http.dart' as http;
import 'package:package_info_plus/package_info_plus.dart';
import 'package:path_provider/path_provider.dart';
import '../config.dart';
import 'api_service.dart';

/// MethodChannel bridge to native Android. Provides:
///  - "getAbi": the device's primary ABI (e.g. "arm64-v8a") for the OTA check.
///  - "installApk": launches the system package installer for a downloaded APK
///    using a FileProvider content:// URI with the correct grant flag. This MUST
///    be native because Android 7+ (API 24+) forbids file:// URIs across
///    processes (FileUriExposedException) and url_launcher cannot set the
///    FLAG_GRANT_READ_URI_PERMISSION that the installer requires.
const _deviceChannel = MethodChannel('study_quest/device');

/// UpdateInfo is the result of an OTA check. [hasUpdate] is the only field the
/// UI needs to gate on; the rest are display/install details.
class UpdateInfo {
  final bool hasUpdate;
  final bool forceUpdate;
  final int versionCode;
  final String versionName;
  final String downloadUrl; // absolute URL (baseUrl + relative contract path)
  final int downloadSize;
  final String releaseNotes;

  UpdateInfo({
    required this.hasUpdate,
    required this.forceUpdate,
    required this.versionCode,
    required this.versionName,
    required this.downloadUrl,
    required this.downloadSize,
    required this.releaseNotes,
  });
}

/// Progress callback for [downloadAndInstall]: receives 0..100.
typedef ProgressCallback = void Function(int percent);

/// AppUpdateService handles OTA self-update against the FROZEN client contract
/// `/api/v1/app/latest`. The contract paths/fields are owned by the backend and
/// must never change here in a way that diverges from it.
class AppUpdateService {
  /// Checks the server for a newer build than the currently-installed one.
  ///
  /// Returns an [UpdateInfo] with [hasUpdate]=false if no update is available,
  /// the ABI has no release, or the check fails for any reason (treat network
  /// errors as "no update" so a flaky check never blocks the user).
  static Future<UpdateInfo> checkForUpdate() async {
    final abi = await _deviceAbi();
    final info = await PackageInfo.fromPlatform();
    final currentVersionCode = int.tryParse(info.buildNumber) ?? 0;

    final uri = Uri.parse(
      '${AppConfig.baseUrlRef}/api/v1/app/latest'
      '?abi=$abi&version_code=$currentVersionCode',
    );

    try {
      // NOTE: bypasses ApiService because OTA runs pre-auth (no session token
      // yet) and only needs the bare Accept header.
      final response = await http.get(uri, headers: {'Accept': 'application/json'});
      // 404 = no release for this ABI → up to date. Non-200 is treated the same:
      // never surface a check error as a hard failure.
      if (response.statusCode != 200) {
        return _noUpdate();
      }
      final data = jsonDecode(response.body) as Map<String, dynamic>;

      final serverVersionCode = (data['version_code'] as num?)?.toInt() ?? 0;
      final hasUpdate = serverVersionCode > currentVersionCode;

      return UpdateInfo(
        hasUpdate: hasUpdate,
        forceUpdate: (data['force_update'] as bool?) ?? false,
        versionCode: serverVersionCode,
        versionName: (data['version_name'] as String?) ?? '',
        // download_url is relative per the contract; resolve against baseUrl.
        downloadUrl: ApiService.absoluteUrl(data['download_url'] as String? ?? ''),
        downloadSize: (data['download_size'] as num?)?.toInt() ?? 0,
        releaseNotes: (data['release_notes'] as String?) ?? '',
      );
    } catch (_) {
      // Any failure (network, parse) → no update. The OTA check must never
      // break normal app usage.
      return _noUpdate();
    }
  }

  /// Downloads the APK to the app's files directory, then hands the path to the
  /// native layer which builds a FileProvider content:// URI and launches the
  /// system package installer with FLAG_GRANT_READ_URI_PERMISSION.
  ///
  /// [onProgress] receives 0..100 as bytes stream in. Throws on download or
  /// install-launch failure; callers should catch and surface a message.
  ///
  /// The APK is written under getApplicationDocumentsDirectory() (NOT a system
  /// temp dir) because the FileProvider in file_paths.xml only maps the app's
  // files path — a system temp dir lives outside that and the installer would
  // get a permission denial.
  static Future<void> downloadAndInstall(
    String downloadUrl, {
    ProgressCallback? onProgress,
  }) async {
    final dir = await getApplicationDocumentsDirectory();
    final file = File('${dir.path}/study_quest_update.apk');
    final sink = file.openWrite();

    try {
      // NOTE: bypasses ApiService because OTA streams an arbitrary release
      // asset URL with manual byte-counting for the progress callback.
      final request = http.Request('GET', Uri.parse(downloadUrl));
      final client = http.Client();
      final response = await client.send(request);

      if (response.statusCode != 200) {
        client.close();
        throw Exception('下载失败: HTTP ${response.statusCode}');
      }

      final total = response.contentLength ?? -1;
      var received = 0;
      await for (final chunk in response.stream) {
        sink.add(chunk);
        received += chunk.length;
        if (total > 0 && onProgress != null) {
          onProgress(((received / total) * 100).round().clamp(0, 100));
        }
      }
      await sink.flush();
      client.close();
    } finally {
      await sink.close();
    }

    // Hand the absolute path to native code, which wraps it in a FileProvider
    // content:// URI and launches ACTION_VIEW with the read grant. Returns a
    // bool; false means the installer couldn't be launched.
    final ok = await _deviceChannel.invokeMethod<bool>('installApk', {'path': file.path}) ?? false;
    if (!ok) {
      throw Exception('无法启动安装器，请检查未知来源安装权限');
    }
  }

  // ── helpers ───────────────────────────────────────────────────────────────

  static Future<String> _deviceAbi() async {
    try {
      final abi = await _deviceChannel.invokeMethod<String>('getAbi');
      // Normalize common variants to the three ABIs the backend distributes.
      // The kernel may report "arm64-v8a+aa" or similar; we only care about the
      // prefix that matches a build.
      if (abi == null || abi.isEmpty) return 'arm64-v8a';
      final lower = abi.toLowerCase();
      if (lower.startsWith('arm64')) return 'arm64-v8a';
      if (lower.startsWith('armeabi')) return 'armeabi-v7a';
      if (lower.startsWith('x86') || lower.startsWith('x64')) return 'x86_64';
      return abi;
    } on PlatformException {
      // Fallback: the overwhelming majority of modern devices are arm64.
      return 'arm64-v8a';
    }
  }

  static UpdateInfo _noUpdate() => UpdateInfo(
        hasUpdate: false,
        forceUpdate: false,
        versionCode: 0,
        versionName: '',
        downloadUrl: '',
        downloadSize: 0,
        releaseNotes: '',
      );
}
