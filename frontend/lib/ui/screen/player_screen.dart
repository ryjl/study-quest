import 'dart:async';
import 'dart:math';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:video_player/video_player.dart';
import '../../model/course.dart';
import '../../service/api_service.dart';
import '../../theme.dart';
import '../widget/focus_button.dart';
import '../widget/glass_panel.dart';
import '../widget/button_3d.dart';

class PlayerScreen extends StatefulWidget {
  final int activeUserId;
  final Episode episode;

  const PlayerScreen({
    Key? key,
    required this.activeUserId,
    required this.episode,
  }) : super(key: key);

  @override
  State<PlayerScreen> createState() => _PlayerScreenState();
}

class _PlayerScreenState extends State<PlayerScreen> {
  VideoPlayerController? _controller;
  bool _isInitialized = false;
  String _errorMessage = '';

  // AI Content State
  AILessonContent? _aiContent;
  bool _loadingAI = true;

  // Quiz State
  bool _showQuizBlocker = false;
  int _currentQuizIndex = 0;
  int? _selectedAnswerIndex;
  bool _quizAnsweredCorrectly = false;
  bool _quizFinished = false;
  int _earnedPoints = 0;

  // Progress Logging State
  Timer? _progressTimer;
  int _lastLoggedPosition = 0;
  bool _hasTriggered80Completed = false;

  @override
  void initState() {
    super.initState();
    _initializeVideo();
    _loadAIContent();
  }

  @override
  void dispose() {
    _progressTimer?.cancel();
    _controller?.dispose();
    super.dispose();
  }

  // Fetch AI content and explorer cards
  Future<void> _loadAIContent() async {
    try {
      final content = await ApiService.fetchAILesson(widget.activeUserId, widget.episode.id);
      if (mounted) {
        setState(() {
          _aiContent = content;
          _loadingAI = false;
        });
      }
    } catch (_) {
      if (mounted) {
        setState(() => _loadingAI = false);
      }
    }
  }

  // Initialize Video Controller using play info (resolving direct URL and headers)
  Future<void> _initializeVideo() async {
    try {
      final playInfo = await ApiService.fetchPlayInfo(widget.activeUserId, widget.episode.id);
      final String resolvedUrl = playInfo['url'] ?? '';
      
      // Parse custom HTTP headers
      final Map<String, String> headers = {};
      final dynamic headersRaw = playInfo['headers'];
      if (headersRaw is Map) {
        headersRaw.forEach((key, value) {
          headers[key.toString()] = value.toString();
        });
      }

      _controller = VideoPlayerController.networkUrl(
        Uri.parse(resolvedUrl),
        httpHeaders: headers,
      );
      await _controller!.initialize();
      
      _controller!.addListener(_videoListener);

      if (mounted) {
        setState(() {
          _isInitialized = true;
        });
        
        // Start playback automatically once initialized
        _controller!.play();
        
        // Start watching logging timer (sync progress every 5 seconds)
        _startProgressTimer();
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _errorMessage = '视频初始化失败: ${e.toString()}';
        });
      }
    }
  }

  void _videoListener() {
    if (_controller == null || !_controller!.value.isInitialized) return;

    final position = _controller!.value.position;
    final duration = _controller!.value.duration;

    // Check if video has reached >80% completion and trigger Quiz blocker
    if (duration.inSeconds > 0 && !_hasTriggered80Completed) {
      final pct = position.inSeconds / duration.inSeconds;
      if (pct >= 0.8) {
        _hasTriggered80Completed = true;
        _pauseAndTriggerQuiz();
      }
    }
  }

  // Start periodic 5s watch reporting to prevent points cheating
  void _startProgressTimer() {
    _progressTimer = Timer.periodic(const Duration(seconds: 5), (timer) async {
      if (_controller == null || !_controller!.value.isPlaying) return;

      final currentPos = _controller!.value.position.inSeconds;
      final delta = currentPos - _lastLoggedPosition;
      
      // Only report if they actually watched forward
      if (delta > 0 && delta <= 10) {
        try {
          await ApiService.reportProgress(
            activeUserId: widget.activeUserId,
            episodeId: widget.episode.id,
            positionSeconds: currentPos,
            deltaWatchSeconds: delta,
          );
          _lastLoggedPosition = currentPos;
        } catch (_) {}
      } else {
        _lastLoggedPosition = currentPos;
      }
    });
  }

  void _pauseAndTriggerQuiz() {
    _controller?.pause();
    setState(() {
      if (_aiContent != null && _aiContent!.postReviewQuiz.isNotEmpty) {
        _showQuizBlocker = true;
      } else {
        // If no quiz ready, directly complete progress silently
        _completeProgressWithoutQuiz();
        _quizFinished = true;
        _showQuizBlocker = true; // Show final completed modal anyway
      }
    });
  }

  Future<void> _completeProgressWithoutQuiz() async {
    try {
      await ApiService.reportProgress(
        activeUserId: widget.activeUserId,
        episodeId: widget.episode.id,
        positionSeconds: _controller?.value.position.inSeconds ?? 0,
        deltaWatchSeconds: 1, // small ping to complete
      );
    } catch (_) {}
  }

  void _onAnswerSelected(int index) {
    if (_selectedAnswerIndex != null) return; // Answer already lock-submitted
    
    final quiz = _aiContent!.postReviewQuiz[_currentQuizIndex];
    final isCorrect = index == quiz.answerIndex;

    setState(() {
      _selectedAnswerIndex = index;
      _quizAnsweredCorrectly = isCorrect;
      if (isCorrect) {
        _earnedPoints += 10; // 10 points per correct answer
      }
    });
  }

  void _onQuizNext() async {
    if (_aiContent == null) return;

    if (_currentQuizIndex < _aiContent!.postReviewQuiz.length - 1) {
      setState(() {
        _currentQuizIndex++;
        _selectedAnswerIndex = null;
        _quizAnsweredCorrectly = false;
      });
    } else {
      // Quiz fully completed, report points and unlock
      setState(() {
        _quizFinished = true;
      });
      
      // Grant points transactionally in database
      try {
        await ApiService.reportProgress(
          activeUserId: widget.activeUserId,
          episodeId: widget.episode.id,
          positionSeconds: _controller?.value.position.inSeconds ?? 0,
          deltaWatchSeconds: 1, // triggers completion grant on server
        );
      } catch (_) {}
    }
  }

  @override
  Widget build(BuildContext context) {
    // Show loading full screen
    if (!_isInitialized && _errorMessage.isEmpty) {
      return const Scaffold(
        backgroundColor: Colors.black,
        body: Center(
          child: CircularProgressIndicator(color: AppTheme.primaryColor),
        ),
      );
    }

    // Show error screen
    if (_errorMessage.isNotEmpty) {
      return Scaffold(
        backgroundColor: Colors.black,
        body: Center(
          child: Padding(
            padding: const EdgeInsets.all(32.0),
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                const Icon(Icons.error_outline, color: Colors.redAccent, size: 64),
                const SizedBox(height: 16),
                Text(_errorMessage, style: const TextStyle(fontSize: 18, color: Colors.redAccent)),
                const SizedBox(height: 32),
                Button3D.white(
                  onPressed: () => Navigator.pop(context),
                  child: const Text('返回上一页'),
                ),
              ],
            ),
          ),
        ),
      );
    }

    return Scaffold(
      backgroundColor: const Color(0xFFF8FAFC), // slate-50 base background
      body: Shortcuts(
        shortcuts: <LogicalKeySet, Intent>{
          LogicalKeySet(LogicalKeyboardKey.select): const ActivateIntent(),
          LogicalKeySet(LogicalKeyboardKey.enter): const ActivateIntent(),
        },
        child: Stack(
          children: [
            // 70/30 Split Layout
            Row(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                // Left 70%: Video player container
                Expanded(
                  flex: 7,
                  child: Container(
                    color: Colors.black,
                    child: Stack(
                      alignment: Alignment.center,
                      children: [
                        AspectRatio(
                          aspectRatio: _controller!.value.aspectRatio,
                          child: VideoPlayer(_controller!),
                        ),
                        // Custom Player Controls layer
                        if (!_showQuizBlocker)
                          _buildPlayerControls(),
                      ],
                    ),
                  ),
                ),

                // Right 30%: "随堂助手" Sidebar Resource Panel (360px wide)
                Container(
                  width: 360,
                  decoration: const BoxDecoration(
                    color: Colors.white,
                    border: Border(
                      left: BorderSide(color: Color(0xFFE2E8F0), width: 2.0),
                    ),
                  ),
                  child: _buildSidebarHelper(),
                ),
              ],
            ),

            // AI Review Quiz blocker overlay (floated over split screen)
            if (_showQuizBlocker && _aiContent != null)
              _buildQuizBlockerOverlay(),
          ],
        ),
      ),
    );
  }

  // 1. Controller Overlay (Timeline seek & play toggle)
  Widget _buildPlayerControls() {
    return Positioned(
      bottom: 0,
      left: 0,
      right: 0,
      child: Container(
        padding: const EdgeInsets.all(24),
        decoration: BoxDecoration(
          gradient: LinearGradient(
            colors: [Colors.transparent, Colors.black.withOpacity(0.85)],
            begin: Alignment.topCenter,
            end: Alignment.bottomCenter,
          ),
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            // Timeline bar
            VideoProgressIndicator(
              _controller!,
              allowScrubbing: true,
              colors: const VideoProgressColors(
                playedColor: AppTheme.primaryColor,
                bufferedColor: Colors.white24,
                backgroundColor: Colors.white10,
              ),
            ),
            const SizedBox(height: 12),

            // Controls buttons
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Row(
                  children: [
                    IconButton(
                      icon: Icon(
                        _controller!.value.isPlaying ? Icons.pause_rounded : Icons.play_arrow_rounded,
                        color: Colors.white,
                        size: 32,
                      ),
                      onPressed: () {
                        setState(() {
                          _controller!.value.isPlaying ? _controller!.pause() : _controller!.play();
                        });
                      },
                    ),
                    const SizedBox(width: 16),
                    Text(
                      _formatDuration(_controller!.value.position) +
                          ' / ' +
                          _formatDuration(_controller!.value.duration),
                      style: const TextStyle(color: Colors.white, fontWeight: FontWeight.bold),
                    ),
                  ],
                ),
                Button3D.dark(
                  padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 8),
                  onPressed: () => Navigator.pop(context),
                  child: const Text('退出播放', style: TextStyle(color: Colors.white)),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  String _formatDuration(Duration d) {
    String twoDigits(int n) => n.toString().padLeft(2, '0');
    final minutes = twoDigits(d.inMinutes.remainder(60));
    final seconds = twoDigits(d.inSeconds.remainder(60));
    return '$minutes:$seconds';
  }

  // 2. 30% Sidebar helper panel
  Widget _buildSidebarHelper() {
    return SingleChildScrollView(
      physics: const BouncingScrollPhysics(),
      padding: const EdgeInsets.all(24.0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Sidebar Title Header
          Row(
            children: [
              Container(
                padding: const EdgeInsets.all(8),
                decoration: const BoxDecoration(
                  color: Color(0xFFEFF6FF),
                  shape: BoxShape.circle,
                ),
                child: const Icon(Icons.psychology_rounded, color: Color(0xFF2563EB), size: 24),
              ),
              const SizedBox(width: 12),
              const Text(
                '随堂助手',
                style: TextStyle(
                  fontSize: 20,
                  fontWeight: FontWeight.w900,
                  color: AppTheme.textWhite,
                ),
              ),
            ],
          ),
          const SizedBox(height: 32),

          // Section: Learning Attachments
          const Text(
            '配套学习资料',
            style: TextStyle(fontSize: 15, fontWeight: FontWeight.w900, color: AppTheme.textWhite),
          ),
          const SizedBox(height: 12),
          // PDF Attachment Button
          Button3D.white(
            padding: const EdgeInsets.symmetric(vertical: 12, horizontal: 16),
            onPressed: () => _openResourceModal('pdf'),
            child: Row(
              children: const [
                Icon(Icons.picture_as_pdf_rounded, color: Color(0xFFF97316), size: 18),
                SizedBox(width: 10),
                Text('配套讲义课件.pdf', style: TextStyle(color: Color(0xFFC2410C), fontWeight: FontWeight.w800, fontSize: 13)),
              ],
            ),
          ),
          const SizedBox(height: 12),
          // AI Summary Button
          Button3D.white(
            padding: const EdgeInsets.symmetric(vertical: 12, horizontal: 16),
            onPressed: () => _openResourceModal('summary'),
            child: Row(
              children: const [
                Icon(Icons.auto_awesome_rounded, color: Color(0xFF8B5CF6), size: 18),
                SizedBox(width: 10),
                Text('AI 重点提炼总结', style: TextStyle(color: Color(0xFF6D28D9), fontWeight: FontWeight.w800, fontSize: 13)),
              ],
            ),
          ),
          const SizedBox(height: 36),

          // Section: Pre-watch Tasks List
          const Text(
            '本节探索任务',
            style: TextStyle(fontSize: 15, fontWeight: FontWeight.w900, color: AppTheme.textWhite),
          ),
          const SizedBox(height: 16),
          _buildSidebarTaskCard(1, '雨来被抓住后，他是怎么跟敌人周旋的？'),
          const SizedBox(height: 12),
          _buildSidebarTaskCard(2, '找出视频里雨来使用的一个成语。'),
          const SizedBox(height: 12),
          _buildSidebarTaskCard(3, '如果你是雨来，你会怎么把信送出去？'),
        ],
      ),
    );
  }

  Widget _buildSidebarTaskCard(int index, String text) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: const Color(0xFFF8FAFC),
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: const Color(0xFFE2E8F0)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 24,
            height: 24,
            alignment: Alignment.center,
            decoration: const BoxDecoration(
              color: Color(0xFFEFF6FF),
              shape: BoxShape.circle,
            ),
            child: Text(
              '$index',
              style: const TextStyle(fontWeight: FontWeight.w900, color: Color(0xFF2563EB), fontSize: 11),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              text,
              style: const TextStyle(color: Color(0xFF475569), fontWeight: FontWeight.bold, fontSize: 13, height: 1.4),
            ),
          ),
        ],
      ),
    );
  }

  // 3. Post-watch Quiz overlay builder
  Widget _buildQuizBlockerOverlay() {
    if (_quizFinished) {
      return _buildQuizFinishedScreen();
    }

    final quiz = _aiContent!.postReviewQuiz[_currentQuizIndex];

    return Positioned.fill(
      child: Container(
        color: const Color(0x900F172A), // Dim background overlay
        child: Center(
          child: Container(
            constraints: const BoxConstraints(maxWidth: 650),
            child: GlassPanel(
              borderRadius: 32,
              baseColor: Colors.white,
              borderColor: Colors.white,
              borderWidth: 2,
              padding: const EdgeInsets.all(36),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  // Header details
                  Row(
                    mainAxisAlignment: MainAxisAlignment.spaceBetween,
                    children: [
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
                        decoration: BoxDecoration(
                          color: AppTheme.accentGreen.withOpacity(0.15),
                          borderRadius: BorderRadius.circular(12),
                        ),
                        child: Row(
                          children: const [
                            Icon(Icons.emoji_events_rounded, color: AppTheme.accentGreen, size: 18),
                            SizedBox(width: 8),
                            Text('课后小挑战', style: TextStyle(color: AppTheme.accentGreen, fontWeight: FontWeight.bold, fontFamily: 'Quicksand')),
                          ],
                        ),
                      ),
                      Text(
                        '问题 ${_currentQuizIndex + 1} / ${_aiContent!.postReviewQuiz.length}',
                        style: const TextStyle(color: AppTheme.textMuted, fontWeight: FontWeight.bold),
                      ),
                    ],
                  ),
                  const SizedBox(height: 24),

                  // Question Title
                  Text(
                    quiz.question,
                    style: const TextStyle(fontSize: 20, fontWeight: FontWeight.w900, height: 1.4, color: AppTheme.textWhite),
                  ),
                  const SizedBox(height: 24),

                  // Multiple choice options list
                  Column(
                    children: List.generate(quiz.options.length, (index) {
                      final optionText = quiz.options[index];
                      final isSelected = _selectedAnswerIndex == index;
                      final isCorrect = quiz.answerIndex == index;

                      Color bg = Colors.white;
                      Color shadow = const Color(0xFFE2E8F0);
                      Border? customBorder = Border.all(color: const Color(0xFFF1F5F9), width: 2.0);

                      // Color indicators after selection lock-in
                      if (_selectedAnswerIndex != null) {
                        if (isCorrect) {
                          bg = const Color(0xFFECFDF5);
                          shadow = const Color(0xFFA7F3D0);
                          customBorder = Border.all(color: AppTheme.accentGreen, width: 2.0);
                        } else if (isSelected) {
                          bg = const Color(0xFFFEF2F2);
                          shadow = const Color(0xFFFCA5A5);
                          customBorder = Border.all(color: Colors.redAccent, width: 2.0);
                        }
                      }

                      return Container(
                        margin: const EdgeInsets.only(bottom: 12),
                        width: double.infinity,
                        child: Button3D(
                          borderRadius: 20,
                          backgroundColor: bg,
                          shadowColor: shadow,
                          border: customBorder,
                          onPressed: () => _onAnswerSelected(index),
                          padding: const EdgeInsets.all(16),
                          child: Row(
                            children: [
                              Container(
                                width: 32,
                                height: 32,
                                alignment: Alignment.center,
                                decoration: BoxDecoration(
                                  shape: BoxShape.circle,
                                  color: isSelected ? AppTheme.primaryColor : const Color(0xFFF1F5F9),
                                ),
                                child: Text(
                                  String.fromCharCode(65 + index), // A, B, C, D
                                  style: TextStyle(
                                    fontWeight: FontWeight.w900,
                                    color: isSelected ? Colors.white : AppTheme.textWhite,
                                  ),
                                ),
                              ),
                              const SizedBox(width: 16),
                              Expanded(
                                child: Text(
                                  optionText,
                                  style: const TextStyle(fontSize: 15, color: AppTheme.textWhite, fontWeight: FontWeight.bold),
                                ),
                              ),
                              if (_selectedAnswerIndex != null && isCorrect)
                                const Icon(Icons.check_circle_rounded, color: AppTheme.accentGreen),
                              if (_selectedAnswerIndex != null && isSelected && !isCorrect)
                                const Icon(Icons.cancel_rounded, color: Colors.redAccent),
                            ],
                          ),
                        ),
                      );
                    }),
                  ),
                  const SizedBox(height: 24),

                  // Next Button (shows after option is selected)
                  if (_selectedAnswerIndex != null)
                    Align(
                      alignment: Alignment.centerRight,
                      child: Button3D.blue(
                        onPressed: _onQuizNext,
                        padding: const EdgeInsets.symmetric(horizontal: 32, vertical: 12),
                        child: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: const [
                            Text(
                              '下一题',
                              style: TextStyle(fontWeight: FontWeight.w900, fontSize: 16, color: Colors.white),
                            ),
                            SizedBox(width: 8),
                            Icon(Icons.arrow_forward_rounded, size: 20, color: Colors.white),
                          ],
                        ),
                      ),
                    ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  // 4. Success Completion splash view
  Widget _buildQuizFinishedScreen() {
    return Positioned.fill(
      child: Container(
        color: const Color(0x900F172A),
        child: Center(
          child: Container(
            constraints: const BoxConstraints(maxWidth: 500),
            child: GlassPanel(
              borderRadius: 36,
              baseColor: Colors.white,
              borderColor: Colors.white,
              borderWidth: 2,
              padding: const EdgeInsets.all(40),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  const Icon(Icons.stars_rounded, color: AppTheme.accentOrange, size: 80),
                  const SizedBox(height: 24),
                  const Text(
                    '恭喜你通关成功！',
                    style: TextStyle(fontSize: 28, fontWeight: FontWeight.w900, color: AppTheme.accentOrange, fontFamily: 'Quicksand'),
                  ),
                  const SizedBox(height: 12),
                  Text(
                    '您已看完了课时: ${widget.episode.title}',
                    style: const TextStyle(color: AppTheme.textMuted, fontSize: 16, fontWeight: FontWeight.bold),
                    textAlign: TextAlign.center,
                  ),
                  const SizedBox(height: 32),

                  // Points summary box
                  Container(
                    padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 16),
                    decoration: BoxDecoration(
                      color: const Color(0xFFF8FAFC),
                      borderRadius: BorderRadius.circular(20),
                      border: Border.all(color: const Color(0xFFE2E8F0)),
                    ),
                    child: Row(
                      mainAxisAlignment: MainAxisAlignment.center,
                      children: [
                        const Text('本次学习奖励积分: ', style: TextStyle(fontSize: 16, fontWeight: FontWeight.bold, color: AppTheme.textWhite)),
                        Text(
                          '+$_earnedPoints 星币',
                          style: const TextStyle(fontSize: 20, fontWeight: FontWeight.w900, color: AppTheme.accentGreen),
                        ),
                      ],
                    ),
                  ),
                  const SizedBox(height: 40),

                  // Complete and close button
                  Button3D.blue(
                    onPressed: () {
                      Navigator.pop(context);
                    },
                    padding: const EdgeInsets.symmetric(horizontal: 48, vertical: 16),
                    child: const Text(
                      '好的，关闭播放',
                      style: TextStyle(fontSize: 18, fontWeight: FontWeight.w900, color: Colors.white),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  // 5. Open resource modals
  void _openResourceModal(String type) {
    final isPdf = type == 'pdf';
    showDialog(
      context: context,
      barrierColor: const Color(0x900F172A),
      builder: (context) {
        return Center(
          child: Container(
            constraints: const BoxConstraints(maxWidth: 700),
            height: MediaQuery.of(context).size.height * 0.75,
            child: GlassPanel(
              borderRadius: 32,
              baseColor: Colors.white,
              borderColor: Colors.white,
              borderWidth: 2,
              padding: const EdgeInsets.all(0),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  // Title Bar
                  Container(
                    padding: const EdgeInsets.all(24),
                    decoration: BoxDecoration(
                      color: isPdf ? const Color(0xFFFFF7ED) : const Color(0xFFF5F3FF),
                      borderRadius: const BorderRadius.only(
                        topLeft: Radius.circular(30),
                        topRight: Radius.circular(30),
                      ),
                    ),
                    child: Row(
                      children: [
                        Container(
                          width: 44,
                          height: 44,
                          decoration: BoxDecoration(
                            color: isPdf ? const Color(0xFFFFF0E0) : const Color(0xFFEDE9FE),
                            borderRadius: BorderRadius.circular(12),
                          ),
                          child: Icon(
                            isPdf ? Icons.picture_as_pdf_rounded : Icons.auto_awesome_rounded,
                            color: isPdf ? const Color(0xFFF97316) : const Color(0xFF8B5CF6),
                          ),
                        ),
                        const SizedBox(width: 16),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(
                                isPdf ? '随堂讲义预览' : 'AI 随堂重点提炼',
                                style: TextStyle(
                                  fontWeight: FontWeight.w900,
                                  fontSize: 16,
                                  color: isPdf ? const Color(0xFF7C2D12) : const Color(0xFF4C1D95),
                                ),
                              ),
                              Text(
                                widget.episode.title,
                                style: TextStyle(color: isPdf ? const Color(0xFFC2410C) : const Color(0xFF6D28D9), fontSize: 12),
                              ),
                            ],
                          ),
                        ),
                        IconButton(
                          onPressed: () => Navigator.pop(context),
                          icon: const Icon(Icons.close_rounded),
                        ),
                      ],
                    ),
                  ),

                  // Content Body
                  Expanded(
                    child: Container(
                      color: const Color(0xFFF8FAFC),
                      padding: const EdgeInsets.all(32),
                      child: isPdf
                          ? Container(
                              decoration: BoxDecoration(
                                color: Colors.white,
                                borderRadius: BorderRadius.circular(20),
                                border: Border.all(color: const Color(0xFFE2E8F0)),
                              ),
                              alignment: Alignment.center,
                              child: Column(
                                mainAxisAlignment: MainAxisAlignment.center,
                                children: [
                                  const Icon(Icons.picture_as_pdf_outlined, color: Color(0xFFF97316), size: 64),
                                  const SizedBox(height: 16),
                                  const Text('PDF 文件渲染器加载中...', style: TextStyle(fontWeight: FontWeight.w900)),
                                  const SizedBox(height: 8),
                                  const Text('这里将同步渲染配套 PDF 课件讲义内容', style: TextStyle(color: AppTheme.textMuted, fontSize: 12)),
                                  const SizedBox(height: 24),
                                  Button3D.blue(
                                    onPressed: () => Navigator.pop(context),
                                    child: const Text('好的，关闭', style: TextStyle(color: Colors.white)),
                                  ),
                                ],
                              ),
                            )
                          : SingleChildScrollView(
                              physics: const BouncingScrollPhysics(),
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  _buildSummarySection('核心要点梳理', '1. 小英雄雨来的性格特点分析：机智、勇敢、爱国。\n2. 重点字词积累：扫荡、周旋、晋察冀边区。\n3. 学会通过细节描写，感受雨来面临鬼子威胁时的从容应对。'),
                                  const SizedBox(height: 20),
                                  _buildSummarySection('探险任务自查', '结合课前探险任务，想一想：\n- 雨来在河湾里跟鬼子周旋，体现了什么战术？\n- 你找到了几个课时中用到的四字成语？'),
                                ],
                              ),
                            ),
                    ),
                  ),
                ],
              ),
            ),
          ),
        );
      },
    );
  }

  Widget _buildSummarySection(String title, String content) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(20),
      decoration: BoxDecoration(
        color: Colors.white,
        borderRadius: BorderRadius.circular(20),
        border: Border.all(color: const Color(0xFFE2E8F0)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Container(width: 4, height: 16, decoration: BoxDecoration(color: const Color(0xFF8B5CF6), borderRadius: BorderRadius.circular(2))),
              const SizedBox(width: 8),
              Text(title, style: const TextStyle(fontWeight: FontWeight.w900, fontSize: 15, color: Color(0xFF4C1D95))),
            ],
          ),
          const SizedBox(height: 12),
          Text(content, style: const TextStyle(color: Color(0xFF475569), fontSize: 13, height: 1.5, fontWeight: FontWeight.bold)),
        ],
      ),
    );
  }
}
