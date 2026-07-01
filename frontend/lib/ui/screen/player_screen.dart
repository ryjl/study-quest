import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:video_player/video_player.dart';
import '../../model/course.dart';
import '../../service/api_service.dart';
import '../../theme.dart';
import '../widget/focus_button.dart';

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
  bool _showExplorerBlocker = false;
  int _currentExplorerCardIndex = 0;

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
          // If pre-adventure cards exist, we show the blocker overlay
          if (content != null && content.preAdventureCards.isNotEmpty) {
            _showExplorerBlocker = true;
          }
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

  void _onExplorerCardNext() {
    if (_aiContent == null) return;
    
    if (_currentExplorerCardIndex < _aiContent!.preAdventureCards.length - 1) {
      setState(() {
        _currentExplorerCardIndex++;
      });
    } else {
      // Finished all cards, clear blocker and play
      setState(() {
        _showExplorerBlocker = false;
      });
      _controller?.play();
    }
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
                FocusButton(
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
      backgroundColor: Colors.black,
      body: Shortcuts(
        shortcuts: <LogicalKeySet, Intent>{
          LogicalKeySet(LogicalKeyboardKey.select): const ActivateIntent(),
          LogicalKeySet(LogicalKeyboardKey.enter): const ActivateIntent(),
        },
        child: Stack(
          children: [
            // Video Output layer
            Center(
              child: AspectRatio(
                aspectRatio: _controller!.value.aspectRatio,
                child: VideoPlayer(_controller!),
              ),
            ),

            // Custom Player Controls layer
            if (!_showExplorerBlocker && !_showQuizBlocker)
              _buildPlayerControls(),

            // AI Pre-adventure Explorer Card blocker
            if (_showExplorerBlocker && _aiContent != null)
              _buildExplorerBlockerOverlay(),

            // AI Review Quiz blocker
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
            colors: [Colors.transparent, Colors.black.withOpacity(0.8)],
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
                      style: const TextStyle(color: AppTheme.textWhite, fontWeight: FontWeight.bold),
                    ),
                  ],
                ),
                FocusButton(
                  padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                  baseColor: Colors.white12,
                  onPressed: () => Navigator.pop(context),
                  child: const Text('退出播放'),
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

  // 2. Pre-watch Explorer overlay builder
  Widget _buildExplorerBlockerOverlay() {
    final cards = _aiContent!.preAdventureCards;
    final currentCard = cards[_currentExplorerCardIndex];

    return Positioned.fill(
      child: Container(
        color: Colors.black.withOpacity(0.9),
        padding: const EdgeInsets.all(40),
        child: Center(
          child: Container(
            constraints: const BoxConstraints(maxWidth: 600),
            padding: const EdgeInsets.all(32),
            decoration: AppTheme.switchDecoration(hasFocus: false),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                // Header badge
                Row(
                  mainAxisAlignment: MainAxisAlignment.spaceBetween,
                  children: [
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
                      decoration: BoxDecoration(
                        color: AppTheme.accentOrange.withOpacity(0.2),
                        borderRadius: BorderRadius.circular(8),
                      ),
                      child: Row(
                        children: const [
                          Icon(Icons.explore, color: AppTheme.accentOrange, size: 18),
                          SizedBox(width: 8),
                          Text('课前探险卡', style: TextStyle(color: AppTheme.accentOrange, fontWeight: FontWeight.bold)),
                        ],
                      ),
                    ),
                    Text(
                      '${_currentExplorerCardIndex + 1} / ${cards.length}',
                      style: const TextStyle(color: AppTheme.textMuted, fontWeight: FontWeight.bold),
                    ),
                  ],
                ),
                const SizedBox(height: 32),

                // Card prompt text
                Text(
                  currentCard.prompt,
                  textAlign: TextAlign.center,
                  style: const TextStyle(fontSize: 22, fontWeight: FontWeight.bold, height: 1.5),
                ),
                const SizedBox(height: 48),

                // Explorer control button
                FocusButton(
                  autoFocus: true,
                  padding: const EdgeInsets.symmetric(horizontal: 48, vertical: 16),
                  baseColor: AppTheme.primaryColor,
                  onPressed: _onExplorerCardNext,
                  child: Text(
                    _currentExplorerCardIndex == cards.length - 1 ? '开始观看视频' : '下一张思考卡',
                    style: const TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
                  ),
                ),
              ],
            ),
          ),
        ),
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
        color: Colors.black.withOpacity(0.95),
        padding: const EdgeInsets.all(40),
        child: Center(
          child: Container(
            constraints: const BoxConstraints(maxWidth: 700),
            padding: const EdgeInsets.all(32),
            decoration: AppTheme.switchDecoration(hasFocus: false),
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
                        color: AppTheme.accentGreen.withOpacity(0.2),
                        borderRadius: BorderRadius.circular(8),
                      ),
                      child: Row(
                        children: const [
                          Icon(Icons.emoji_events, color: AppTheme.accentGreen, size: 18),
                          SizedBox(width: 8),
                          Text('课后小挑战', style: TextStyle(color: AppTheme.accentGreen, fontWeight: FontWeight.bold)),
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
                  style: const TextStyle(fontSize: 20, fontWeight: FontWeight.bold, height: 1.4),
                ),
                const SizedBox(height: 24),

                // Multiple choice options list
                Column(
                  children: List.generate(quiz.options.length, (index) {
                    final optionText = quiz.options[index];
                    final isSelected = _selectedAnswerIndex == index;
                    final isCorrect = quiz.answerIndex == index;

                    Color bg = AppTheme.cardColor;
                    Color border = AppTheme.borderMuted;

                    // Color indicators after selection lock-in
                    if (_selectedAnswerIndex != null) {
                      if (isCorrect) {
                        bg = AppTheme.accentGreen.withOpacity(0.15);
                        border = AppTheme.accentGreen;
                      } else if (isSelected) {
                        bg = Colors.redAccent.withOpacity(0.15);
                        border = Colors.redAccent;
                      }
                    }

                    return Container(
                      margin: const EdgeInsets.only(bottom: 12),
                      width: double.infinity,
                      child: FocusButton(
                        baseColor: bg,
                        borderColor: border,
                        onPressed: () => _onAnswerSelected(index),
                        child: Row(
                          children: [
                            Container(
                              width: 32,
                              height: 32,
                              alignment: Alignment.center,
                              decoration: BoxDecoration(
                                shape: BoxShape.circle,
                                color: isSelected ? AppTheme.primaryColor : Colors.white10,
                              ),
                              child: Text(
                                String.fromCharCode(65 + index), // A, B, C, D
                                style: const TextStyle(fontWeight: FontWeight.bold),
                              ),
                            ),
                            const SizedBox(width: 16),
                            Expanded(
                              child: Text(
                                optionText,
                                style: const TextStyle(fontSize: 16, color: AppTheme.textWhite),
                              ),
                            ),
                            if (_selectedAnswerIndex != null && isCorrect)
                              const Icon(Icons.check_circle, color: AppTheme.accentGreen),
                            if (_selectedAnswerIndex != null && isSelected && !isCorrect)
                              const Icon(Icons.cancel, color: Colors.redAccent),
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
                    child: FocusButton(
                      autoFocus: true,
                      padding: const EdgeInsets.symmetric(horizontal: 32, vertical: 12),
                      baseColor: AppTheme.primaryColor,
                      onPressed: _onQuizNext,
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Text(
                            _currentQuizIndex == _aiContent!.postReviewQuiz.length - 1 ? '完成测验' : '下一题',
                            style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 16),
                          ),
                          const SizedBox(width: 8),
                          const Icon(Icons.arrow_forward_rounded, size: 20),
                        ],
                      ),
                    ),
                  ),
              ],
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
        color: Colors.black.withOpacity(0.95),
        child: Center(
          child: Container(
            constraints: const BoxConstraints(maxWidth: 500),
            padding: const EdgeInsets.all(40),
            decoration: AppTheme.switchDecoration(hasFocus: false),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const Icon(Icons.stars, color: AppTheme.accentOrange, size: 80),
                const SizedBox(height: 24),
                const Text(
                  '恭喜你通关成功！',
                  style: TextStyle(fontSize: 28, fontWeight: FontWeight.bold, color: AppTheme.accentOrange),
                ),
                const SizedBox(height: 12),
                Text(
                  '您已看完了课时: ${widget.episode.title}',
                  style: const TextStyle(color: AppTheme.textMuted, fontSize: 16),
                  textAlign: TextAlign.center,
                ),
                const SizedBox(height: 32),

                // Points summary box
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 16),
                  decoration: BoxDecoration(
                    color: Colors.white.withOpacity(0.02),
                    borderRadius: BorderRadius.circular(12),
                    border: Border.all(color: AppTheme.borderMuted),
                  ),
                  child: Row(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: [
                      const Text('本次学习奖励积分: ', style: TextStyle(fontSize: 16)),
                      Text(
                        '+$_earnedPoints 分',
                        style: const TextStyle(fontSize: 20, fontWeight: FontWeight.bold, color: AppTheme.accentGreen),
                      ),
                    ],
                  ),
                ),
                const SizedBox(height: 40),

                // Complete and close button
                FocusButton(
                  autoFocus: true,
                  padding: const EdgeInsets.symmetric(horizontal: 48, vertical: 16),
                  baseColor: AppTheme.accentGreen,
                  onPressed: () {
                    Navigator.pop(context);
                  },
                  child: const Text(
                    '好的，关闭播放',
                    style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
