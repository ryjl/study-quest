import 'dart:async';
import 'dart:io';
import 'package:flutter/material.dart';
import 'package:http/http.dart' as http;
import 'package:path_provider/path_provider.dart';
import 'package:pdfrx/pdfrx.dart';
import 'package:printing/printing.dart';
import '../../model/reading.dart';
import '../../service/api_service.dart';
import '../../theme.dart';

/// Full-screen PDF reader with:
/// - Local caching: first open downloads to the app documents dir, subsequent
///   opens read from disk (keyed by bookId + fileHash for cache invalidation).
/// - Page progress memory: saves the last-read page, restores on next open.
/// - Native printing via Android PrintManager (printing package).
class PdfReaderScreen extends StatefulWidget {
  final int activeUserId;
  final ReadingBook book;

  const PdfReaderScreen({
    Key? key,
    required this.activeUserId,
    required this.book,
  }) : super(key: key);

  @override
  State<PdfReaderScreen> createState() => _PdfReaderScreenState();
}

class _PdfReaderScreenState extends State<PdfReaderScreen> {
  File? _cachedFile;
  bool _downloading = false;
  double _downloadProgress = 0;
  String? _error;
  int _lastPage = 0;
  Timer? _progressDebounce;
  PdfViewerController? _controller;

  @override
  void initState() {
    super.initState();
    _init();
  }

  @override
  void dispose() {
    _progressDebounce?.cancel();
    super.dispose();
  }

  Future<void> _init() async {
    // Load saved progress FIRST (await), so _lastPage is populated before the
    // PdfViewer mounts and onViewerReady fires. Otherwise the restore guard
    // sees _lastPage == 0 and the user always lands on page 1.
    try {
      final page = await ApiService.fetchBookProgress(widget.activeUserId, widget.book.id);
      if (mounted) _lastPage = page;
    } catch (_) {
      // Non-fatal — start from page 1.
    }
    await _prepareFile();
  }

  /// Returns the local cache path for this book. The filename includes the
  /// fileHash so a backend file replacement (different hash) invalidates the
  /// cache automatically — the old file is orphaned and a fresh download occurs.
  String _cacheKey() {
    final hash = widget.book.fileHash.isNotEmpty ? widget.book.fileHash : '${widget.book.id}';
    return 'reading_${widget.book.id}_$hash.pdf';
  }

  Future<void> _prepareFile() async {
    final dir = await getApplicationDocumentsDirectory();
    final cacheDir = Directory('${dir.path}/reading_cache');
    if (!cacheDir.existsSync()) {
      cacheDir.createSync(recursive: true);
    }
    final file = File('${cacheDir.path}/${_cacheKey()}');

    if (file.existsSync()) {
      // Cache hit — render immediately.
      if (mounted) setState(() => _cachedFile = file);
      return;
    }

    // Cache miss — download the PDF from the stream endpoint.
    // Write to a .part temp file first; only rename to the final name after
    // the download completes successfully. This prevents a partial/corrupt
    // file from being served as a "cache hit" on the next retry.
    if (mounted) setState(() => _downloading = true);
    final partFile = File('${file.path}.part');
    // Clean up any stale .part from a previous failed download.
    if (partFile.existsSync()) {
      partFile.deleteSync();
    }
    try {
      final url = ApiService.bookStreamUrl(widget.book.id);
      final request = http.Request('GET', Uri.parse(url));
      request.headers['X-User-ID'] = widget.activeUserId.toString();

      final client = http.Client();
      final response = await client.send(request);

      if (response.statusCode != 200) {
        throw Exception('下载失败: ${response.statusCode}');
      }

      final totalBytes = response.contentLength ?? 0;
      var receivedBytes = 0;
      final sink = partFile.openWrite();

      await for (final chunk in response.stream) {
        sink.add(chunk);
        receivedBytes += chunk.length;
        if (totalBytes > 0 && mounted) {
          setState(() => _downloadProgress = receivedBytes / totalBytes);
        }
      }
      await sink.close();
      client.close();

      // Download complete — promote the .part file to the final cache name.
      await partFile.rename(file.path);

      if (mounted) {
        setState(() {
          _cachedFile = file;
          _downloading = false;
        });
      }
    } catch (e) {
      // Clean up the partial file so a retry starts fresh.
      try {
        if (partFile.existsSync()) partFile.deleteSync();
      } catch (_) {}
      if (mounted) {
        setState(() {
          _downloading = false;
          _error = e.toString();
        });
      }
    }
  }

  void _onPageChanged(int page) {
    _lastPage = page;
    // Debounce progress reports — don't spam the server on every swipe.
    _progressDebounce?.cancel();
    _progressDebounce = Timer(const Duration(milliseconds: 500), () {
      ApiService.reportBookProgress(
        activeUserId: widget.activeUserId,
        bookId: widget.book.id,
        lastPage: page,
      ).catchError((_) {});
    });
  }

  Future<void> _printPdf() async {
    if (_cachedFile == null) return;
    await Printing.layoutPdf(
      name: widget.book.title,
      onLayout: (format) async => _cachedFile!.readAsBytesSync(),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          widget.book.title,
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
        actions: [
          if (_cachedFile != null)
            IconButton(
              icon: const Icon(Icons.print_rounded),
              onPressed: _printPdf,
              tooltip: '打印',
            ),
        ],
      ),
      body: _buildBody(),
    );
  }

  Widget _buildBody() {
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.error_outline, size: 64, color: Colors.red),
              const SizedBox(height: 16),
              Text(_error!, textAlign: TextAlign.center,
                  style: const TextStyle(color: AppTheme.textMuted)),
              const SizedBox(height: 16),
              ElevatedButton(
                onPressed: () {
                  setState(() => _error = null);
                  _prepareFile();
                },
                child: const Text('重试'),
              ),
            ],
          ),
        ),
      );
    }

    if (_downloading) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const CircularProgressIndicator(),
              const SizedBox(height: 16),
              const Text('正在下载 PDF...', style: TextStyle(color: AppTheme.textMuted)),
              if (_downloadProgress > 0) ...[
                const SizedBox(height: 8),
                Text(
                  '${(_downloadProgress * 100).round()}%',
                  style: const TextStyle(
                    color: AppTheme.primaryColor,
                    fontWeight: FontWeight.bold,
                  ),
                ),
              ],
            ],
          ),
        ),
      );
    }

    if (_cachedFile == null) {
      return const Center(child: CircularProgressIndicator());
    }

    // PDF ready — render with pdfrx, restoring the last-read page.
    return PdfViewer.file(
      _cachedFile!.path,
      controller: _controller,
      params: PdfViewerParams(
        enableTextSelection: true,
        onViewerReady: (document, controller) {
          _controller = controller;
          // Jump to the last-read page (1-indexed in pdfrx; _lastPage is 0-indexed).
          if (_lastPage > 0 && _lastPage < document.pages.length) {
            controller.goToPage(pageNumber: _lastPage + 1);
          }
        },
        onPageChanged: (pageNumber) {
          // pdfrx uses 1-indexed pages; store 0-indexed for consistency.
          if (pageNumber != null && pageNumber > 0) {
            _onPageChanged(pageNumber - 1);
          }
        },
      ),
    );
  }
}
