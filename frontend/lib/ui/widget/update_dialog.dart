import 'package:flutter/material.dart';

import '../../service/update_service.dart';

/// Update dialog shown when a newer APK build is available. Downloads the APK
/// with a progress bar, then hands off to the system installer.
///
/// When [forceUpdate] is true the dialog cannot be dismissed — the user must
/// install before continuing (used for critical fixes).
class UpdateDialog extends StatefulWidget {
  final UpdateInfo update;
  final bool forceUpdate;

  const UpdateDialog({
    super.key,
    required this.update,
    required this.forceUpdate,
  });

  @override
  State<UpdateDialog> createState() => _UpdateDialogState();
}

class _UpdateDialogState extends State<UpdateDialog> {
  bool _downloading = false;
  int _progress = 0;
  String? _error;

  void _startDownload() async {
    setState(() {
      _downloading = true;
      _error = null;
      _progress = 0;
    });
    try {
      await AppUpdateService.downloadAndInstall(
        widget.update.downloadUrl,
        onProgress: (p) {
          if (mounted) setState(() => _progress = p);
        },
      );
      // The system installer is now showing; leave the dialog as-is. If the
      // user cancels the install, they'll re-trigger the check next launch.
    } catch (e) {
      if (mounted) {
        setState(() {
          _downloading = false;
          _error = e.toString();
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final u = widget.update;
    return PopScope(
      // Prevent back-button dismissal when force-update.
      canPop: !widget.forceUpdate,
      child: AlertDialog(
        title: Text(u.forceUpdate ? '需要更新到新版本' : '发现新版本'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('${u.versionName} (build ${u.versionCode})',
                style: const TextStyle(fontWeight: FontWeight.bold)),
            const SizedBox(height: 8),
            if (u.releaseNotes.isNotEmpty)
              ConstrainedBox(
                constraints: const BoxConstraints(maxHeight: 160),
                child: SingleChildScrollView(
                  child: Text(u.releaseNotes, style: const TextStyle(fontSize: 13)),
                ),
              ),
            if (u.downloadSize > 0)
              Padding(
                padding: const EdgeInsets.only(top: 4),
                child: Text('大小: ${_formatBytes(u.downloadSize)}',
                    style: TextStyle(fontSize: 12, color: Colors.grey[600])),
              ),
            if (_downloading) ...[
              const SizedBox(height: 16),
              LinearProgressIndicator(value: _progress / 100),
              const SizedBox(height: 4),
              Text('下载中 $_progress%', style: const TextStyle(fontSize: 12)),
            ],
            if (_error != null) ...[
              const SizedBox(height: 12),
              Text(_error!, style: const TextStyle(color: Colors.red, fontSize: 12)),
            ],
          ],
        ),
        actions: [
          if (!widget.forceUpdate && !_downloading)
            TextButton(
              onPressed: () => Navigator.of(context).pop(),
              child: const Text('稍后'),
            ),
          if (!_downloading)
            ElevatedButton(
              onPressed: _startDownload,
              child: const Text('立即更新'),
            ),
        ],
      ),
    );
  }
}

String _formatBytes(int n) {
  if (n < 1024) return '$n B';
  if (n < 1024 * 1024) return '${(n / 1024).toStringAsFixed(1)} KB';
  return '${(n / 1024 / 1024).toStringAsFixed(1)} MB';
}
