import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:webview_flutter/webview_flutter.dart';
import '../../model/reading.dart';
import '../../theme.dart';

/// Full-screen WebView reader for web articles (公众号 H5, interactive pages).
///
/// **Portrait lock**: web articles are designed for phone portrait screens. In
/// landscape the layout breaks (fonts oversized, audio controls pushed
/// off-screen, viewport scripts miscompute). This screen forces portrait on
/// push and restores the app's default orientations on pop. No CSS injection,
/// no text zoom — the page renders at its intended design width.
///
/// **Navigation interception**: only domains in the article's whitelist (plus
/// the article's own host) are allowed; everything else is blocked via
/// NavigationDecision.prevent.
///
/// Phase 2 hook: when article.mirrorStatus == 'ready', load mirroredUrl.
class ArticleReaderScreen extends StatefulWidget {
  final int activeUserId;
  final ReadingArticle article;

  const ArticleReaderScreen({
    Key? key,
    required this.activeUserId,
    required this.article,
  }) : super(key: key);

  @override
  State<ArticleReaderScreen> createState() => _ArticleReaderScreenState();
}

class _ArticleReaderScreenState extends State<ArticleReaderScreen> {
  late final WebViewController _controller;
  double _progress = 0;
  bool _loading = true;
  String? _lastBlockedHost;
  DateTime _lastBlockedAt = DateTime.fromMillisecondsSinceEpoch(0);

  static const _defaultWhitelist = [
    'mp.weixin.qq.com',
    'mmbiz.qpic.cn',
    'mmbiz.qlogo.cn',
    'res.wx.qq.com',
  ];

  List<String> get _effectiveWhitelist {
    final wl = widget.article.whitelistDomains;
    // Always include the article's own source host — internal navigation within
    // the article's site must not be blocked.
    final sourceHost = Uri.tryParse(widget.article.effectiveUrl)?.host ?? '';
    final base = wl.isNotEmpty ? wl : _defaultWhitelist;
    if (sourceHost.isNotEmpty && !base.contains(sourceHost)) {
      return [...base, sourceHost];
    }
    return base;
  }

  bool _isAllowed(Uri uri) {
    final host = uri.host;
    for (final domain in _effectiveWhitelist) {
      if (host == domain || host.endsWith('.$domain')) {
        return true;
      }
    }
    return false;
  }

  @override
  void initState() {
    super.initState();
    // Force portrait — these pages are designed for phone portrait width.
    SystemChrome.setPreferredOrientations([
      DeviceOrientation.portraitUp,
    ]);

    _controller = WebViewController()
      ..setJavaScriptMode(JavaScriptMode.unrestricted)
      ..setNavigationDelegate(
        NavigationDelegate(
          onProgress: (progress) {
            if (mounted) {
              setState(() {
                _progress = progress / 100;
                _loading = progress < 100;
              });
            }
          },
          onPageFinished: (_) {
            if (mounted) setState(() => _loading = false);
          },
          onNavigationRequest: (request) {
            final uri = Uri.tryParse(request.url);
            if (uri == null) {
              return NavigationDecision.prevent;
            }
            if (uri.scheme == 'data' || uri.scheme == 'blob' || uri.scheme == 'about') {
              return NavigationDecision.navigate;
            }
            if (_isAllowed(uri)) {
              return NavigationDecision.navigate;
            }
            _notifyBlocked(uri.host);
            return NavigationDecision.prevent;
          },
        ),
      )
      ..loadRequest(Uri.parse(widget.article.effectiveUrl));
  }

  @override
  void dispose() {
    // Restore all orientations when leaving the reader.
    SystemChrome.setPreferredOrientations([
      DeviceOrientation.landscapeLeft,
      DeviceOrientation.landscapeRight,
      DeviceOrientation.portraitUp,
      DeviceOrientation.portraitDown,
    ]);
    super.dispose();
  }

  void _notifyBlocked(String host) {
    final now = DateTime.now();
    if (host == _lastBlockedHost && now.difference(_lastBlockedAt).inSeconds < 3) {
      return;
    }
    _lastBlockedHost = host;
    _lastBlockedAt = now;
    if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('已拦截外部链接：$host'),
          duration: const Duration(seconds: 2),
        ),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          widget.article.title,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: const TextStyle(fontWeight: FontWeight.bold),
        ),
        backgroundColor: AppTheme.primaryColor,
        foregroundColor: Colors.white,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_rounded),
          onPressed: () => Navigator.pop(context),
        ),
      ),
      body: Stack(
        children: [
          WebViewWidget(controller: _controller),
          if (_loading)
            Positioned(
              top: 0,
              left: 0,
              right: 0,
              child: LinearProgressIndicator(
                value: _progress > 0 ? _progress : null,
                minHeight: 3,
                backgroundColor: Colors.transparent,
                valueColor: const AlwaysStoppedAnimation<Color>(AppTheme.primaryColor),
              ),
            ),
        ],
      ),
    );
  }
}
