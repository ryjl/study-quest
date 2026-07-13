import 'package:device_info_plus/device_info_plus.dart';
import 'package:flutter/foundation.dart';

/// Resolves a human-friendly device name for the login payload's device_name
/// field. The result is what the admin sees in the per-user device-session
/// list, so we prefer the OS-level label a user would recognize (the name they
/// chose in Settings), falling back to a model/platform string when that's not
/// available.
///
/// This is best-effort: any failure returns null, in which case the backend
/// records only the HTTP User-Agent. Login never blocks on this.
class DeviceInfoService {
  static Future<String?> getDeviceName() async {
    try {
      final info = await DeviceInfoPlugin().deviceInfo;
      if (info is AndroidDeviceInfo) {
        // AndroidDeviceInfo has no "user-set name" field. The best stable,
        // recognizable identifier is the model (e.g. "SM-T970") or device
        // codename. Prefer model since it usually maps to a market name.
        // 'model' can be empty on some emulators; fall back to device/brand.
        final model = info.model;
        final device = info.device;
        final brand = info.brand;
        if (model.isNotEmpty && model != device) {
          return _capitalize(model);
        }
        if (device.isNotEmpty) return _capitalize(device);
        if (brand.isNotEmpty) return '${_capitalize(brand)} device';
        return 'Android device';
      }
      if (info is IosDeviceInfo) {
        // iosInfo.name is the user-set device name ("小明 的 iPad") — exactly
        // what we want surfaced in the admin list.
        if (info.name.isNotEmpty) return info.name;
        // Fall back to model (e.g. "iPad14,1") if name is somehow empty.
        if (info.model.isNotEmpty) return info.model;
        return 'iOS device';
      }
    } catch (e) {
      if (kDebugMode) {
        debugPrint('DeviceInfoService.getDeviceName failed: $e');
      }
      return null;
    }
    // Unknown platform (desktop, web) — caller passes null and backend uses UA.
    return null;
  }

  /// Capitalize the first letter so "SM-T970" stays as-is (already caps) but
  /// lowercase brand names like "xiaomi" become "Xiaomi". No-op for already-
  /// capitalized strings.
  static String _capitalize(String s) {
    if (s.isEmpty) return s;
    return s[0].toUpperCase() + s.substring(1);
  }
}
