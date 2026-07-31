import 'package:flutter/material.dart';

import '../../service/update_service.dart';
import '../../theme.dart';

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
    } catch (_) {
      if (mounted) {
        setState(() {
          _downloading = false;
          // 不把 e.toString()(如 SocketException 堆栈)直接展示给 K12 用户,改成人话
          // 文案。具体错误已在 service 层按场景抛出(HTTP 码/安装器),这里统一兜底。
          _error = '下载失败，请检查网络后重试';
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final u = widget.update;
    final colors = context.colors;
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
                    style: TextStyle(fontSize: 12, color: colors.textMuted)),
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
          // 下载中给非强制更新一个取消出口:原版下载中两个按钮都被隐藏,若下载
          // 卡住(进度停滞又不抛异常)用户会被钉死在弹窗里。取消=关闭弹窗放弃这次
          // 更新(底层下载是孤儿,但下次启动会重新检查、且同文件名会被覆盖)。
          // force-update 不给出口(产品意图:必须更新)。
          if (!widget.forceUpdate && _downloading)
            TextButton(
              onPressed: () => Navigator.of(context).pop(),
              child: const Text('取消'),
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
