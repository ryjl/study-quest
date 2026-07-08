import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';
import 'package:pdfrx/pdfrx.dart';
import 'package:url_launcher/url_launcher.dart';
import '../../config.dart';
import '../../model/course.dart';
import '../../service/api_service.dart';
import '../../theme.dart';
import '../widget/button_3d.dart';
import '../widget/focus_button.dart';
import '../widget/glass_panel.dart';

/// Immersive video playback screen.
///
/// Replaces the old video_player implementation with [media_kit] (libmpv),
/// which gives us broader codec support, hardware decoding, and native
/// subtitle/audio track switching — all essential for netdisk videos.
///
/// Pipeline:
///   1. fetchPlayInfo() → resolves direct streaming URL + headers + resume
///      position + subtitle track list.
///   2. media_kit Player opens the URL with custom HTTP headers (Referer for
///      115 netdisk etc.).
///   3. Seeks to the last saved position (断点续播) once metadata is loaded.
///   4. Reports progress every 5s (anti-cheat) and triggers the 80% quiz gate.
class PlayerScreen extends StatefulWidget {
  final int activeUserId;
  final Episode episode;

  /// Optional real pre-adventure prompts (AI-generated). When empty the panel
  /// falls back to placeholder copy.
  final List<String> preAdventureTasks;

  const PlayerScreen({
    Key? key,
    required this.activeUserId,
    required this.episode,
    this.preAdventureTasks = const [],
  }) : super(key: key);

  @override
  State<PlayerScreen> createState() => _PlayerScreenState();
}

class _PlayerScreenState extends State<PlayerScreen> {
  // media_kit core
  late final Player _player;
  late final VideoController _controller;
  bool _engineReady = false;
  String _errorMessage = '';

  // Resolved playback metadata
  PlayInfo? _playInfo;

  // Resume (断点续播) target. Set from playInfo before open(); applied via
  // open(play:false) → [duration ready] → seek → play sequence.
  Duration? _pendingResume;
  Duration? _resumeTarget; // active target while the resume seek is pending
  bool _resumeSeekDone = false;
  int _resumeRetries = 0;
  DateTime? _lastResumeRetry;
  List<Attachment> _attachments = [];
  AILessonContent? _aiContent;
  bool _loadingExtras = true;

  // Subtitle selection (0 = off, otherwise 1-based into _playInfo.subtitles)
  int _selectedSubtitle = 0;

  // Anti-cheat progress logging
  Timer? _progressTimer;
  int _lastLoggedPosition = 0;
  bool _hasTriggered80Percent = false;
  // Resume bookkeeping — the seek must wait until media_kit reports a real
  // duration, otherwise the seek silently no-ops.

  // Quiz gate state
  bool _showQuizBlocker = false;
  int _currentQuizIndex = 0;
  int? _selectedAnswerIndex;
  bool _quizFinished = false;
  int _earnedPoints = 0;

  // Auto-hide controls
  bool _controlsVisible = true;
  Timer? _hideTimer;

  // Fullscreen + extra controls state
  bool _isFullscreen = false;
  bool _controlsLocked = false;
  double _rate = 1.0;
  double? _volumeBeforeMute; // null = not muted
  // Drag-to-seek transient state. While the user is dragging the seek bar we
  // only update a local preview position (no seek); a single seek is committed
  // on change-end. Without this, dragging fires dozens of seek() calls and
  // tears the libmpv demuxer down on every frame.
  bool _isDraggingSeek = false;
  Duration _dragPosition = Duration.zero;

  @override
  void initState() {
    super.initState();
    // Create the player up front so the Video widget has something to render
    // into while we resolve the stream URL asynchronously.
    _player = Player(
      configuration: PlayerConfiguration(
        title: widget.episode.title,
        // Demuxer read-ahead cap. ~1 minute of forward buffer is a good
        // balance for netdisk streams: enough headroom for 4K/high-bitrate
        // without wasting bandwidth if the user quits early. See
        // `_tuneMpvForNetdisk` for the related cache-secs setting.
        bufferSize: 64 * 1024 * 1024,
      ),
    );
    // Hardware acceleration OFF by default.
    //
    // media_kit's HW decoder delegates to MediaCodec/OMX. On emulators (MuMu is
    // x86 pretending to be ARM) the emulated OMX.qcom.* decoders are unstable:
    // we observed the whole Flutter engine freezing mid-playback with repeated
    // `PlayerBase::stop()` and no further render ticks. Software decoding is
    // slower but reliable across devices; revisit for real ARM hardware if a
    // high-bitrate file actually stutters.
    _controller = VideoController(_player,
        configuration: const VideoControllerConfiguration(
          enableHardwareAcceleration: false,
        ));

    _initializeVideo();
    _loadExtras();
    _setupKeyListeners();
    _tuneMpvForNetdisk();
  }

  /// Tune libmpv properties for netdisk (AList → 302 → cloud CDN) streams.
  ///
  /// Set via [NativePlayer.setProperty], the public escape hatch media_kit
  /// exposes for raw mpv options. Two reasons:
  ///
  /// 1. Some cloud CDNs (e.g. 天翼 cloudcube) answer HEAD/probe requests with
  ///    HTTP 403 even though GET Range works fine. mpv's default
  ///    `demuxer-lavf-probe-info=auto` issues such probes; a 403 makes mpv
  ///    think the stream is unusable and tears the demuxer down. Forcing `no`
  ///    skips the probe and trusts the first GET.
  /// 2. `cache-secs=60` keeps ~1 minute of forward byte cache, matching the
  ///    demuxer buffer cap above. Keeps the index/window alive across a seek.
  ///
  /// Best-effort: a no-op if `setProperty` isn't available on the platform.
  Future<void> _tuneMpvForNetdisk() async {
    try {
      final dynamic np = _player.platform;
      await np.setProperty('demuxer-lavf-probe-info', 'no');
      await np.setProperty('cache-secs', '60');
    } catch (_) {
      // setProperty not available (e.g. web); safe to ignore.
    }
  }

  @override
  void dispose() {
    _progressTimer?.cancel();
    _hideTimer?.cancel();
    _player.dispose();
    super.dispose();
  }

  // ---------------------------------------------------------------------------
  // Setup
  // ---------------------------------------------------------------------------

  Future<void> _initializeVideo() async {
    try {
      final playInfo = await ApiService.fetchPlayInfo(
          widget.activeUserId, widget.episode.id);
      _playInfo = playInfo;

      if (playInfo.url.isEmpty) {
        throw Exception('后端未能解析出可播放的视频地址');
      }

      final resumeSec = playInfo.resumePositionSeconds ?? 0;
      // Resume whenever there's a meaningful position saved — even for
      // completed episodes. "Completed" only gates rewards/quiz/labels, not
      // playback: if a user watched to 90%, closed, and reopened, they
      // expect to land back at 90%, not at the start.
      final shouldResume = resumeSec > 5;
      if (shouldResume) {
        _pendingResume = Duration(seconds: resumeSec);
      }
      if (playInfo.isCompleted) {
        _hasTriggered80Percent = true; // already finished — don't re-prompt
      }

      // Build a media with per-source HTTP headers (115 netdisk needs Referer).
      // For resume, pass `start` so libmpv opens the demuxer at the resume
      // offset directly — the first rendered frame is already near the target,
      // avoiding the visible 0 → target jump. A watchdog still re-seeks if the
      // CDN connection drops and resets position (see _setupResumeSeek).
      final headers = <String, String>{}..addAll(playInfo.headers);
      final media = Media(
        playInfo.url,
        httpHeaders: headers,
        start: shouldResume ? _pendingResume : null,
      );

      // Resume (断点续播): open with auto-play (default), then a watchdog
      // re-seeks to the resume target whenever position gets reset. On these
      // netdisk streams the CDN connection can drop mid-playback and libmpv
      // re-opens the stream from 0; open(play:false) + seek + play doesn't
      // help because play() itself resets position too. The only reliable
      // approach is to keep re-seeking until playback stabilizes past the
      // target. See _setupResumeSeek for the watchdog.
      if (shouldResume) {
        _resumeTarget = _pendingResume!;
        _player.open(media);
        _setupResumeSeek();
      } else {
        _player.open(media);
      }

      if (mounted) {
        setState(() => _engineReady = true);
      }

      // Begin anti-cheat logging once playback actually starts.
      _startProgressTimer();
      _scheduleAutoHide();
    } catch (e) {
      if (mounted) {
        setState(() => _errorMessage = '视频初始化失败: ${e.toString()}');
      }
    }
  }

  /// Fetch AI content + real attachments in parallel (non-blocking for video).
  Future<void> _loadExtras() async {
    try {
      final results = await Future.wait([
        ApiService.fetchAILesson(widget.activeUserId, widget.episode.id)
            .catchError((_) => null),
        ApiService.fetchAttachments(widget.activeUserId, widget.episode.id)
            .catchError((_) => <Attachment>[]),
      ]);
      if (mounted) {
        setState(() {
          _aiContent = results[0] as AILessonContent?;
          _attachments = results[1] as List<Attachment>;
          _loadingExtras = false;
        });
      }
    } catch (_) {
      if (mounted) setState(() => _loadingExtras = false);
    }
  }

  void _setupKeyListeners() {
    // Trigger the 80% quiz gate based on position progress.
    _player.stream.position.listen((position) {
      final duration = _player.state.duration;
      if (!_hasTriggered80Percent &&
          duration.inSeconds > 0 &&
          position.inSeconds / duration.inSeconds >= 0.8) {
        _hasTriggered80Percent = true;
        _pauseAndTriggerQuiz();
      }
    });
  }

  /// Resume seek helper: after open(play:false), wait for the demuxer to
  /// report a real duration, then seek to the resume target and start play.
  /// The whole sequence runs while playback is paused, so the seek doesn't
  /// race against a VideoOutput rebuild.
  void _setupResumeSeek() {
    final target = _resumeTarget;
    if (target == null) return;
    // Wait for demuxer ready, then seek. After that, run a persistent
    // watchdog: if position snaps back far from the target (which happens
    // when the CDN connection drops and libmpv re-opens the stream from 0),
    // re-seek. We stop only once playback has genuinely progressed past the
    // target (user is watching forward) or after a bounded retry count.
    _player.stream.duration.listen((duration) {
      if (_resumeSeekDone) return;
      if (duration.inSeconds <= 0 || target >= duration) return;
      _resumeSeekDone = true;
      _player.seek(target);
      _lastLoggedPosition = target.inSeconds;
    });

    _player.stream.position.listen((pos) {
      final tgt = target.inSeconds;
      // Once the user has played past the target + a margin, resume is
      // considered successful — stop guarding.
      if (pos.inSeconds > tgt + 10) {
        return;
      }
      // Close to target → fine, nothing to do.
      if ((pos.inSeconds - tgt).abs() < 15) return;
      if (_resumeRetries >= 8) return;
      // Position far from target (typically 0 after a CDN reconnect reset).
      // Re-seek, throttled to ~1/sec, only when demuxer is ready.
      if (_player.state.duration.inSeconds <= 0 || _player.state.buffering) {
        return;
      }
      final now = DateTime.now();
      if (_lastResumeRetry != null &&
          now.difference(_lastResumeRetry!) < const Duration(seconds: 1)) {
        return;
      }
      _lastResumeRetry = now;
      _resumeRetries++;
      _player.seek(target);
    });
  }

  // ---------------------------------------------------------------------------
  // Progress reporting (anti-cheat)
  // ---------------------------------------------------------------------------

  void _startProgressTimer() {
    _progressTimer = Timer.periodic(const Duration(seconds: 5), (_) async {
      if (!_player.state.playing) return;
      final currentPos = _player.state.position.inSeconds;
      final delta = currentPos - _lastLoggedPosition;
      // Only credit forward-watching deltas (skip seeks / pauses). The upper
      // bound is generous (30) vs the 5s cadence so a momentary stall (buffer,
      // GC, resume re-seek) that delays one tick doesn't discard a legit 6-10s
      // delta — a previous 10 cap silently dropped watch time and left the
      // admin "learning time" column stuck at 0. The backend still clamps each
      // report (600s) and only credits monotonic forward progress, so a big
      // accidental jump can't inflate the total.
      if (delta > 0 && delta <= 30) {
        try {
          await ApiService.reportProgress(
            activeUserId: widget.activeUserId,
            episodeId: widget.episode.id,
            positionSeconds: currentPos,
            deltaWatchSeconds: delta,
          );
          _lastLoggedPosition = currentPos;
        } catch (_) {}
      } else if (delta < 0) {
        // Position went backwards (typically a CDN reconnect reset to 0 during
        // resume). Don't anchor the baseline at the low point — that would let
        // the subsequent re-seek forward register a huge false delta. Just skip
        // this tick; the resume-seek watchdog re-establishes the real position.
        // Leave _lastLoggedPosition unchanged so the next forward tick compares
        // against the last genuine forward position.
        return;
      } else {
        // delta == 0 or delta > 30 (a seek / jump): resync the baseline so we
        // don't credit the jump as watch time.
        _lastLoggedPosition = currentPos;
      }
    });
  }

  Future<void> _reportPing({int delta = 1}) async {
    try {
      await ApiService.reportProgress(
        activeUserId: widget.activeUserId,
        episodeId: widget.episode.id,
        positionSeconds: _player.state.position.inSeconds,
        deltaWatchSeconds: delta,
      );
    } catch (_) {}
  }

  // ---------------------------------------------------------------------------
  // Quiz gate
  // ---------------------------------------------------------------------------

  void _pauseAndTriggerQuiz() {
    _player.pause();
    setState(() {
      if (_aiContent != null && _aiContent!.postReviewQuiz.isNotEmpty) {
        _showQuizBlocker = true;
      } else {
        _reportPing();
        _quizFinished = true;
        _showQuizBlocker = true;
      }
    });
  }

  void _onAnswerSelected(int index) {
    if (_selectedAnswerIndex != null) return;
    final quiz = _aiContent!.postReviewQuiz[_currentQuizIndex];
    final isCorrect = index == quiz.answerIndex;
    setState(() {
      _selectedAnswerIndex = index;
      if (isCorrect) _earnedPoints += 10;
    });
  }

  Future<void> _onQuizNext() async {
    if (_aiContent == null) return;
    if (_currentQuizIndex < _aiContent!.postReviewQuiz.length - 1) {
      setState(() {
        _currentQuizIndex++;
        _selectedAnswerIndex = null;
      });
    } else {
      setState(() => _quizFinished = true);
      await _reportPing(); // triggers completion grant server-side
    }
  }

  // ---------------------------------------------------------------------------
  // Subtitle selection
  // ---------------------------------------------------------------------------

  Future<void> _selectSubtitle(int slot) async {
    // slot 0 => off; slot n => _playInfo.subtitles[n-1]
    setState(() => _selectedSubtitle = slot);
    final subs = _playInfo?.subtitles ?? const [];
    if (slot == 0) {
      await _player.setSubtitleTrack(SubtitleTrack.no());
    } else if (slot - 1 < subs.length) {
      final sub = subs[slot - 1];
      final url = ApiService.absoluteUrl(sub.url);
      await _player.setSubtitleTrack(SubtitleTrack.uri(url, title: sub.label));
    }
  }

  // ---------------------------------------------------------------------------
  // Controls auto-hide
  // ---------------------------------------------------------------------------

  void _scheduleAutoHide() {
    _hideTimer?.cancel();
    _hideTimer = Timer(const Duration(seconds: 4), () {
      if (mounted && _player.state.playing && !_showQuizBlocker) {
        setState(() => _controlsVisible = false);
      }
    });
  }

  void _toggleControls() {
    setState(() => _controlsVisible = !_controlsVisible);
    if (_controlsVisible) _scheduleAutoHide();
  }

  // ---------------------------------------------------------------------------
  // Volume / speed / fullscreen / lock
  // ---------------------------------------------------------------------------

  void _setVolume(double v) {
    _player.setVolume(v.clamp(0.0, 100.0));
    setState(() {
      // Unmute if a real volume is chosen while muted.
      _volumeBeforeMute = null;
    });
    _scheduleAutoHide();
  }

  void _toggleMute() {
    if (_volumeBeforeMute == null) {
      setState(() => _volumeBeforeMute = _player.state.volume);
      _player.setVolume(0);
    } else {
      _player.setVolume(_volumeBeforeMute!);
      setState(() => _volumeBeforeMute = null);
    }
    _scheduleAutoHide();
  }

  void _setRate(double r) {
    _player.setRate(r);
    setState(() => _rate = r);
    _scheduleAutoHide();
  }

  void _toggleFullscreen() {
    setState(() => _isFullscreen = !_isFullscreen);
    _scheduleAutoHide();
  }

  void _toggleLock() {
    setState(() {
      _controlsLocked = !_controlsLocked;
      if (_controlsLocked) {
        _controlsVisible = false;
      }
    });
  }

  // ---------------------------------------------------------------------------
  // Gesture-based seeking on the video surface
  // ---------------------------------------------------------------------------

  void _onSeekDragStart() {
    setState(() => _isDraggingSeek = true);
    _dragPosition = _player.state.position;
  }

  void _onSeekDragUpdate(double dx, double videoWidth) {
    // Map horizontal drag to seconds: a full video width ≈ 120s.
    final seconds = (dx / videoWidth) * 120;
    var target = _dragPosition + Duration(seconds: seconds.round());
    final duration = _player.state.duration;
    // Duration has no .clamp(); clamp manually.
    if (target < Duration.zero) target = Duration.zero;
    if (duration.inSeconds > 0 && target > duration) target = duration;
    setState(() => _dragPosition = target);
  }

  Future<void> _onSeekDragEnd() async {
    setState(() => _isDraggingSeek = false);
    await _seekTo(_dragPosition);
  }

  // ---------------------------------------------------------------------------
  // Build
  // ---------------------------------------------------------------------------

  @override
  Widget build(BuildContext context) {
    if (!_engineReady && _errorMessage.isEmpty) {
      return _buildLoadingScreen();
    }
    if (_errorMessage.isNotEmpty) {
      return _buildErrorScreen();
    }

    return Scaffold(
      backgroundColor: Colors.black,
      body: Shortcuts(
        shortcuts: {
          LogicalKeySet(LogicalKeyboardKey.select): const ActivateIntent(),
          LogicalKeySet(LogicalKeyboardKey.enter): const ActivateIntent(),
          LogicalKeySet(LogicalKeyboardKey.space): const ActivateIntent(),
          LogicalKeySet(LogicalKeyboardKey.mediaPlay): const ActivateIntent(),
          LogicalKeySet(LogicalKeyboardKey.mediaPause): const ActivateIntent(),
        },
        child: Focus(
          autofocus: true,
          onKeyEvent: _onRemoteKey,
          child: Stack(
            children: [
              // 70/30 split: video (left) + helper panel (right).
              // In fullscreen the helper panel is collapsed so the video
              // fills the whole screen.
              Row(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Expanded(
                    flex: _isFullscreen ? 1 : 7,
                    child: _buildVideoArea(),
                  ),
                  if (!_isFullscreen) _buildHelperPanel(),
                ],
              ),

              // Buffering spinner centered over the video. We listen to the
              // buffering stream (not state) so the overlay rebuilds promptly,
              // and we only show it while actually playing — a paused player
              // reports buffering too, which would leave a stale spinner after
              // a seek that lands in a buffered region.
              if (!_showQuizBlocker)
                StreamBuilder<bool>(
                  stream: _player.stream.buffering,
                  initialData: _player.state.buffering,
                  builder: (context, snap) {
                    final buffering = snap.data ?? false;
                    if (!buffering || !_player.state.playing) {
                      return const SizedBox.shrink();
                    }
                    return const Center(
                      child: CircularProgressIndicator(color: Colors.white70),
                    );
                  },
                ),

              // Quiz blocker overlay
              if (_showQuizBlocker && _aiContent != null)
                _buildQuizBlockerOverlay(),
            ],
          ),
        ),
      ),
    );
  }

  /// D-pad / keyboard handler. media_kit's own keyboard handling is disabled
  /// because we wrap with our own Focus; here we own play/pause + seek.
  KeyEventResult _onRemoteKey(FocusNode node, KeyEvent event) {
    if (event is! KeyDownEvent) return KeyEventResult.ignored;
    final key = event.logicalKey;

    if (key == LogicalKeyboardKey.space ||
        key == LogicalKeyboardKey.enter ||
        key == LogicalKeyboardKey.select) {
      _togglePlayPause();
      return KeyEventResult.handled;
    } else if (key == LogicalKeyboardKey.arrowLeft) {
      _seekRelative(const Duration(seconds: -10));
      return KeyEventResult.handled;
    } else if (key == LogicalKeyboardKey.arrowRight) {
      _seekRelative(const Duration(seconds: 10));
      return KeyEventResult.handled;
    } else if (key == LogicalKeyboardKey.arrowUp) {
      _player.setVolume((_player.state.volume + 5).clamp(0, 100));
      return KeyEventResult.handled;
    } else if (key == LogicalKeyboardKey.arrowDown) {
      _player.setVolume((_player.state.volume - 5).clamp(0, 100));
      return KeyEventResult.handled;
    } else if (key == LogicalKeyboardKey.escape ||
        key == LogicalKeyboardKey.browserBack) {
      Navigator.maybePop(context);
      return KeyEventResult.handled;
    }
    return KeyEventResult.ignored;
  }

  void _togglePlayPause() {
    if (_player.state.playing) {
      _player.pause();
    } else {
      _player.play();
    }
    _scheduleAutoHide();
  }

  /// Centralized seek wrapper. Clamps to [0, duration] and ignores the request
  /// entirely while the real duration is unknown — a seek past the unknown end
  /// previously sent media_kit into a broken state (jumped back to 0, controls
  /// froze).
  Future<void> _seekTo(Duration target) async {
    final duration = _player.state.duration;
    if (duration.inSeconds > 0) {
      if (target < Duration.zero) target = Duration.zero;
      if (target > duration) target = duration;
    }
    await _player.seek(target);
    _scheduleAutoHide();
  }

  /// Seek by a delta from the *current* player position (not the StreamBuilder
  /// snapshot, which may be stale during rapid taps).
  void _seekRelative(Duration delta) {
    if (_player.state.duration.inSeconds <= 0) return; // metadata not ready
    _seekTo(_player.state.position + delta);
  }

  // ---------------------------------------------------------------------------
  // Video area (left 70%)
  // ---------------------------------------------------------------------------

  Widget _buildVideoArea() {
    return LayoutBuilder(
      builder: (context, constraints) {
        final videoWidth = constraints.maxWidth;
        return Stack(
          fit: StackFit.expand,
          children: [
            // 1. Video surface (no gesture handling of its own — media_kit's
            //    Video widget is used purely for rendering).
            Positioned.fill(
              child: Container(
                color: Colors.black,
                child: Video(
                  controller: _controller,
                  fill: Colors.black,
                  controls: NoVideoControls,
                ),
              ),
            ),

            // 2. Drag-to-seek live position readout.
            if (_isDraggingSeek)
              Center(
                child: Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                  decoration: BoxDecoration(
                    color: Colors.black54,
                    borderRadius: BorderRadius.circular(8),
                  ),
                  child: Text(
                    _formatDuration(_dragPosition),
                    style: const TextStyle(
                        color: Colors.white,
                        fontWeight: FontWeight.bold,
                        fontSize: 16),
                  ),
                ),
              ),

            // 3. Top-level gesture layer. Sits ABOVE the video, BELOW the
            //    controls overlay. Handles tap-to-toggle, double-tap ±10s,
            //    horizontal drag to scrub. Crucially it is NOT a parent of the
            //    controls, so the Slider/buttons get their own gestures; and
            //    because it is above the Video widget, media_kit cannot swallow
            //    the taps that should re-show the controls.
            if (!_controlsLocked && !_showQuizBlocker)
              Positioned.fill(
                child: GestureDetector(
                  behavior: HitTestBehavior.translucent,
                  onTap: _toggleControls,
                  onDoubleTapDown: (details) {
                    final isRight = details.localPosition.dx > videoWidth / 2;
                    _seekRelative(Duration(
                        seconds: isRight ? 10 : -10));
                  },
                  onHorizontalDragStart: (_) => _onSeekDragStart(),
                  onHorizontalDragUpdate: (details) =>
                      _onSeekDragUpdate(details.delta.dx, videoWidth),
                  onHorizontalDragEnd: (_) => _onSeekDragEnd(),
                ),
              ),

            // 4. Lock chip when controls are locked.
            if (_controlsLocked && _controlsVisible)
              Positioned(
                top: 24,
                left: 0,
                right: 0,
                child: Center(
                  child: Container(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 12, vertical: 6),
                    decoration: BoxDecoration(
                      color: Colors.black54,
                      borderRadius: BorderRadius.circular(20),
                    ),
                    child: const Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Icon(Icons.lock_rounded,
                            color: Colors.white, size: 16),
                        SizedBox(width: 6),
                        Text('已锁定控件',
                            style:
                                TextStyle(color: Colors.white, fontSize: 12)),
                      ],
                    ),
                  ),
                ),
              ),

            // 5. Controls overlay (top bar + bottom bar). When hidden, this
            //    subtree is removed so taps fall through to layer 3.
            if (_controlsVisible && !_showQuizBlocker)
              Positioned.fill(
                child: Stack(
                  children: [
                    if (!_controlsLocked)
                      Positioned(
                        top: 0,
                        left: 0,
                        right: 0,
                        child: _buildTopBar(),
                      ),
                    if (!_controlsLocked) _buildPlayerControls(),
                  ],
                ),
              ),
          ],
        );
      },
    );
  }

  Widget _buildTopBar() {
    return Container(
      padding: const EdgeInsets.fromLTRB(20, 16, 20, 32),
      decoration: const BoxDecoration(
        gradient: LinearGradient(
          colors: [Colors.black54, Colors.transparent],
          begin: Alignment.topCenter,
          end: Alignment.bottomCenter,
        ),
      ),
      child: Row(
        children: [
          _iconControl(
            icon: Icons.arrow_back_rounded,
            onTap: () {
              if (_isFullscreen) {
                _toggleFullscreen();
              } else {
                Navigator.pop(context);
              }
            },
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Text(
              widget.episode.title,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                  color: Colors.white,
                  fontWeight: FontWeight.bold,
                  fontSize: 15),
            ),
          ),
          const SizedBox(width: 8),
          _iconControl(
            icon: _controlsLocked
                ? Icons.lock_outline
                : Icons.lock_open_rounded,
            onTap: _toggleLock,
          ),
        ],
      ),
    );
  }

  Widget _buildPlayerControls() {
    return Positioned(
      left: 0,
      right: 0,
      bottom: 0,
      child: Container(
        padding: const EdgeInsets.fromLTRB(24, 32, 24, 20),
        decoration: const BoxDecoration(
          gradient: LinearGradient(
            colors: [Colors.transparent, Colors.black87],
            begin: Alignment.topCenter,
            end: Alignment.bottomCenter,
          ),
        ),
        // IMPORTANT: each StreamBuilder must pass `initialData` from the
        // synchronous `_player.state` snapshot. The controls overlay is
        // unmounted whenever `_controlsVisible` flips to false (so taps fall
        // through to the gesture layer). When it re-mounts, the streams emit
        // no cached value immediately — without `initialData` every snapshot
        // is null and we'd briefly see position=0/duration=0, which disables
        // the Slider (`totalMs > 0` is false) and shows the time as 0:00.
        // That was the real "progress bar breaks after the controls auto-hide"
        // regression.
        child: StreamBuilder<Duration>(
          stream: _player.stream.position,
          initialData: _player.state.position,
          builder: (context, posSnapshot) {
            return StreamBuilder<Duration>(
              stream: _player.stream.duration,
              initialData: _player.state.duration,
              builder: (context, durSnapshot) {
                return StreamBuilder<Duration>(
                  stream: _player.stream.buffer,
                  initialData: _player.state.buffer,
                  builder: (context, bufSnapshot) {
                    final position = posSnapshot.data ?? Duration.zero;
                    final duration = durSnapshot.data ?? Duration.zero;
                    final buffer = bufSnapshot.data ?? Duration.zero;
                    return Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        _buildSeekBar(position, duration, buffer),
                        const SizedBox(height: 10),
                        _buildControlsRow(position, duration),
                      ],
                    );
                  },
                );
              },
            );
          },
        ),
      ),
    );
  }

  Widget _buildSeekBar(Duration position, Duration duration, Duration buffer) {
    final totalMs = duration.inMilliseconds.toDouble();
    final maxMs = totalMs > 0 ? totalMs : 1.0;
    final posMs = position.inMilliseconds.toDouble().clamp(0.0, maxMs);
    final bufMs = buffer.inMilliseconds.toDouble().clamp(0.0, maxMs);

    // While dragging, follow the thumb preview instead of the live position.
    // This is essential: Slider.onChanged fires per pixel of drag, and feeding
    // every move into _player.seek() would tear the libmpv demuxer down on
    // every frame. We only commit a single seek on change-end.
    final displayMs =
        _isDraggingSeek ? _dragPosition.inMilliseconds.toDouble().clamp(0.0, maxMs) : posMs;
    final displayPos = _isDraggingSeek ? _dragPosition : position;

    return Row(
      children: [
        Text(
          _formatDuration(displayPos),
          style: const TextStyle(
              color: Colors.white, fontWeight: FontWeight.bold, fontSize: 12),
        ),
        Expanded(
          child: SliderTheme(
            data: SliderTheme.of(context).copyWith(
              trackHeight: 4,
              // Custom track paints three ranges:
              //   [0 .. position]   → played (primary)
              //   [position .. buf] → buffered (lighter)
              //   [buf .. end]      → unbuffered (faint)
              trackShape: _BufferedSeekBarTrackShape(
                bufferedFraction: totalMs > 0 ? bufMs / totalMs : 0.0,
                bufferedColor: Colors.white38,
              ),
              thumbShape: const RoundSliderThumbShape(enabledThumbRadius: 7),
              overlayShape: const RoundSliderOverlayShape(overlayRadius: 14),
            ),
            child: Slider(
              value: displayMs,
              max: maxMs,
              onChangeStart: totalMs > 0
                  ? (_) {
                      setState(() {
                        _isDraggingSeek = true;
                        _dragPosition = position;
                      });
                    }
                  : null,
              onChanged: totalMs > 0
                  ? (v) {
                      setState(() {
                        _dragPosition = Duration(milliseconds: v.toInt());
                      });
                    }
                  : null,
              onChangeEnd: totalMs > 0
                  ? (v) {
                      _seekTo(Duration(milliseconds: v.toInt()));
                      setState(() => _isDraggingSeek = false);
                    }
                  : null,
              activeColor: AppTheme.primaryColor,
              inactiveColor: Colors.white24,
            ),
          ),
        ),
        Text(
          _formatDuration(duration),
          style: const TextStyle(
              color: Colors.white70, fontWeight: FontWeight.bold, fontSize: 12),
        ),
      ],
    );
  }


  Widget _buildControlsRow(Duration position, Duration duration) {
    final playing = _player.state.playing;
    return Row(
      children: [
        // Play / pause
        _iconControl(
          icon: playing ? Icons.pause_rounded : Icons.play_arrow_rounded,
          onTap: _togglePlayPause,
        ),
        const SizedBox(width: 8),
        // Back 10s (clamped; no-op until duration is known)
        _iconControl(
          icon: Icons.replay_10_rounded,
          onTap: () => _seekRelative(const Duration(seconds: -10)),
        ),
        const SizedBox(width: 8),
        // Forward 10s (clamped; no-op until duration is known)
        _iconControl(
          icon: Icons.forward_10_rounded,
          onTap: () => _seekRelative(const Duration(seconds: 10)),
        ),
        const SizedBox(width: 16),

        // Volume: mute toggle + horizontal slider
        _iconControl(
          icon: (_volumeBeforeMute != null || _player.state.volume == 0)
              ? Icons.volume_off_rounded
              : Icons.volume_up_rounded,
          onTap: _toggleMute,
        ),
        SizedBox(
          width: 100,
          child: SliderTheme(
            data: SliderTheme.of(context).copyWith(
              trackHeight: 3,
              thumbShape:
                  const RoundSliderThumbShape(enabledThumbRadius: 6),
              overlayShape: const RoundSliderOverlayShape(overlayRadius: 12),
            ),
            child: StreamBuilder<double>(
              stream: _player.stream.volume,
              initialData: _player.state.volume,
              builder: (context, snap) {
                final v = snap.data ?? _player.state.volume;
                return Slider(
                  value: v.clamp(0.0, 100.0),
                  min: 0,
                  max: 100,
                  activeColor: Colors.white,
                  inactiveColor: Colors.white24,
                  onChanged: _setVolume,
                );
              },
            ),
          ),
        ),
        const SizedBox(width: 8),

        // Playback speed picker
        _popupControl(
          icon: Icons.speed_rounded,
          label: _rateLabel(_rate),
          items: [0.5, 0.75, 1.0, 1.25, 1.5, 2.0]
              .map((r) => _PopupItem(
                    label: r == 1.0 ? '正常 (1.0x)' : '${r}x',
                    onTap: () => _setRate(r),
                  ))
              .toList(),
        ),
        const SizedBox(width: 8),

        // Subtitle picker
        if ((_playInfo?.subtitles ?? []).isNotEmpty)
          _popupControl(
            icon: Icons.closed_caption_rounded,
            label: _selectedSubtitle == 0
                ? '字幕'
                : _playInfo!.subtitles[_selectedSubtitle - 1].label,
            items: [
              _PopupItem(label: '关闭字幕', onTap: () => _selectSubtitle(0)),
              ..._playInfo!.subtitles.asMap().entries.map((e) => _PopupItem(
                    label: e.value.label,
                    onTap: () => _selectSubtitle(e.key + 1),
                  )),
            ],
          ),

        const Spacer(),

        // Fullscreen toggle (also in top bar, mirrored here for one-thumb reach)
        _iconControl(
          icon: _isFullscreen
              ? Icons.fullscreen_exit_rounded
              : Icons.fullscreen_rounded,
          onTap: _toggleFullscreen,
        ),
      ],
    );
  }

  /// Human-readable label for the speed button, e.g. "倍速" or "1.5x".
  String _rateLabel(double r) {
    if (r == 1.0) return '倍速';
    // Trim trailing zeros: 1.50 -> 1.5, 0.75 -> 0.75.
    var s = r.toStringAsFixed(2);
    s = s.replaceAll(RegExp(r'0+$'), '');
    s = s.replaceAll(RegExp(r'\.$'), '');
    return '${s}x';
  }

  /// A D-pad focusable circular icon button used in the controls bar.
  Widget _iconControl({required IconData icon, required VoidCallback onTap}) {
    return FocusButton(
      onPressed: onTap,
      borderRadius: 24,
      baseColor: Colors.white12,
      borderColor: Colors.transparent,
      padding: const EdgeInsets.all(8),
      child: Icon(icon, color: Colors.white, size: 28),
    );
  }

  /// A focusable icon button that opens a popup menu (subtitle picker).
  Widget _popupControl({
    required IconData icon,
    required String label,
    required List<_PopupItem> items,
  }) {
    // NOTE: do NOT wrap the child in a GestureDetector/FocusButton that
    // absorbs taps — that prevents PopupMenuButton from ever showing the
    // menu (the previous version wrapped the child in a FocusButton with an
    // empty onPressed, which is why the speed button did nothing).
    return PopupMenuButton<int>(
      tooltip: label,
      color: const Color(0xFF1E293B),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
      // Custom child renders our icon+label chip; PopupMenuButton still owns
      // the tap → menu → onSelected flow.
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
        decoration: BoxDecoration(
          color: Colors.white12,
          borderRadius: BorderRadius.circular(24),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, color: Colors.white, size: 20),
            const SizedBox(width: 6),
            Text(label,
                style: const TextStyle(
                    color: Colors.white,
                    fontSize: 12,
                    fontWeight: FontWeight.bold)),
          ],
        ),
      ),
      itemBuilder: (_) => List.generate(items.length, (i) {
        return PopupMenuItem<int>(
          value: i,
          child: Row(
            children: [
              Icon(icon, color: Colors.white70, size: 18),
              const SizedBox(width: 8),
              Text(items[i].label,
                  style: const TextStyle(color: Colors.white)),
            ],
          ),
        );
      }),
      onSelected: (i) => items[i].onTap(),
    );
  }

  // ---------------------------------------------------------------------------
  // Helper panel (right ~30%)
  // ---------------------------------------------------------------------------

  Widget _buildHelperPanel() {
    return Container(
      width: 360,
      decoration: const BoxDecoration(
        color: Colors.white,
        border: Border(left: BorderSide(color: Color(0xFFE2E8F0), width: 2)),
      ),
      child: SingleChildScrollView(
        physics: const BouncingScrollPhysics(),
        padding: const EdgeInsets.all(24),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Title bar
            Row(
              children: [
                Container(
                  padding: const EdgeInsets.all(8),
                  decoration: const BoxDecoration(
                    color: Color(0xFFEFF6FF),
                    shape: BoxShape.circle,
                  ),
                  child: const Icon(Icons.psychology_rounded,
                      color: Color(0xFF2563EB), size: 24),
                ),
                const SizedBox(width: 12),
                const Text('随堂助手',
                    style: TextStyle(
                        fontSize: 20,
                        fontWeight: FontWeight.w900,
                        color: AppTheme.textWhite)),
              ],
            ),
            const SizedBox(height: 24),

            // Episode title context
            Text(widget.episode.title,
                style: const TextStyle(
                    fontWeight: FontWeight.w900,
                    fontSize: 15,
                    color: AppTheme.textWhite)),
            const SizedBox(height: 28),

            // Attachments section
            const Text('附属资料',
                style: TextStyle(
                    fontSize: 12,
                    fontWeight: FontWeight.w900,
                    letterSpacing: 1.5,
                    color: AppTheme.textMuted)),
            const SizedBox(height: 12),
            _buildAttachmentsSection(),
            const SizedBox(height: 28),

            // Pre-adventure tasks
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                const Text('本节探索任务',
                    style: TextStyle(
                        fontSize: 12,
                        fontWeight: FontWeight.w900,
                        letterSpacing: 1.5,
                        color: AppTheme.textMuted)),
                Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                  decoration: BoxDecoration(
                    color: const Color(0xFFEFF6FF),
                    borderRadius: BorderRadius.circular(8),
                  ),
                  child: const Text('带着问题看',
                      style: TextStyle(
                          fontSize: 10,
                          fontWeight: FontWeight.bold,
                          color: Color(0xFF2563EB))),
                ),
              ],
            ),
            const SizedBox(height: 12),
            _buildPreAdventureSection(),
          ],
        ),
      ),
    );
  }

  Widget _buildAttachmentsSection() {
    if (_loadingExtras) {
      return const Padding(
        padding: EdgeInsets.symmetric(vertical: 8),
        child: SizedBox(
          width: 20,
          height: 20,
          child: CircularProgressIndicator(strokeWidth: 2),
        ),
      );
    }
    if (_attachments.isEmpty) {
      return _placeholderTile(
        icon: Icons.picture_as_pdf_outlined,
        title: '暂无配套讲义',
        accent: const Color(0xFFF97316),
      );
    }
    return Column(
      children: _attachments.map((att) {
        final isPdf = att.isPdf;
        final accent =
            isPdf ? const Color(0xFFF97316) : const Color(0xFF8B5CF6);
        return Padding(
          padding: const EdgeInsets.only(bottom: 10),
          child: FocusButton(
            onPressed: () => _openAttachment(att),
            borderRadius: 14,
            baseColor: isPdf
                ? const Color(0xFFFFF7ED)
                : const Color(0xFFF5F3FF),
            borderColor:
                isPdf ? const Color(0xFFFED7AA) : const Color(0xFFDDD6FE),
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
            child: Row(
              children: [
                Icon(isPdf ? Icons.picture_as_pdf_rounded : Icons.attach_file,
                    color: accent, size: 18),
                const SizedBox(width: 10),
                Expanded(
                  child: Text(att.fileName.isEmpty ? '配套资料' : att.fileName,
                      style: TextStyle(
                          color: accent,
                          fontWeight: FontWeight.w800,
                          fontSize: 13),
                      overflow: TextOverflow.ellipsis),
                ),
                const Icon(Icons.chevron_right_rounded,
                    color: Color(0xFF94A3B8), size: 18),
              ],
            ),
          ),
        );
      }).toList(),
    );
  }

  Widget _buildPreAdventureSection() {
    final tasks = widget.preAdventureTasks.isNotEmpty
        ? widget.preAdventureTasks
        : (_aiContent?.preAdventureCards.map((c) => c.prompt).toList() ?? const []);
    if (tasks.isEmpty) {
      return _placeholderTile(
        icon: Icons.casino_outlined,
        title: '本节暂无探索任务',
        accent: const Color(0xFF3B82F6),
      );
    }
    return Column(
      children: List.generate(tasks.length, (i) {
        return Padding(
          padding: const EdgeInsets.only(bottom: 10),
          child: _taskCard(i + 1, tasks[i]),
        );
      }),
    );
  }

  Widget _placeholderTile(
      {required IconData icon,
      required String title,
      required Color accent}) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 12),
      decoration: BoxDecoration(
        color: const Color(0xFFF8FAFC),
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: const Color(0xFFE2E8F0)),
      ),
      child: Row(
        children: [
          Icon(icon, color: accent.withOpacity(0.5), size: 18),
          const SizedBox(width: 10),
          Text(title,
              style: const TextStyle(
                  color: AppTheme.textMuted, fontWeight: FontWeight.bold)),
        ],
      ),
    );
  }

  Widget _taskCard(int index, String text) {
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
            child: Text('$index',
                style: const TextStyle(
                    fontWeight: FontWeight.w900,
                    color: Color(0xFF2563EB),
                    fontSize: 11)),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Text(text,
                style: const TextStyle(
                    color: Color(0xFF475569),
                    fontWeight: FontWeight.bold,
                    fontSize: 13,
                    height: 1.4)),
          ),
        ],
      ),
    );
  }

  // ---------------------------------------------------------------------------
  // Attachment viewer
  // ---------------------------------------------------------------------------

  void _openAttachment(Attachment att) {
    final streamUrl =
        '${AppConfig.baseUrlRef}/api/v1/episodes/${widget.episode.id}/attachments/${att.index}/stream';
    // The stream endpoint requires X-User-ID; we cannot pass headers from
    // url_launcher, but the PDF viewer can be fed the redirected URL by
    // fetching through an HttpClient that injects the header. For PDFs we use
    // SfPdfViewer.network which supports headers; for others we fall back to
    // launching externally.
    if (att.isPdf) {
      _showPdfViewer(att, streamUrl);
    } else {
      _launchExternal(streamUrl);
    }
  }

  void _showPdfViewer(Attachment att, String url) {
    showDialog(
      context: context,
      barrierColor: const Color(0x900F172A),
      builder: (context) {
        return Center(
          child: Container(
            constraints: const BoxConstraints(maxWidth: 900),
            height: MediaQuery.of(context).size.height * 0.85,
            child: GlassPanel(
              borderRadius: 24,
              baseColor: Colors.white,
              borderColor: Colors.white,
              borderWidth: 2,
              padding: const EdgeInsets.all(0),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Container(
                    padding: const EdgeInsets.all(16),
                    decoration: const BoxDecoration(
                      color: Color(0xFFFFF7ED),
                      borderRadius: BorderRadius.only(
                        topLeft: Radius.circular(22),
                        topRight: Radius.circular(22),
                      ),
                    ),
                    child: Row(
                      children: [
                        const Icon(Icons.picture_as_pdf_rounded,
                            color: Color(0xFFF97316)),
                        const SizedBox(width: 12),
                        Expanded(
                          child: Text(att.fileName,
                              style: const TextStyle(
                                  fontWeight: FontWeight.w900,
                                  color: Color(0xFF7C2D12))),
                        ),
                        IconButton(
                          onPressed: () => Navigator.pop(context),
                          icon: const Icon(Icons.close_rounded),
                        ),
                      ],
                    ),
                  ),
                  Expanded(
                    child: PdfViewer(
                      PdfDocumentRefUri(
                        Uri.parse(url),
                        headers: {
                          'X-User-ID': widget.activeUserId.toString(),
                        },
                      ),
                      params: PdfViewerParams(
                        // Keep the viewer ready for streaming range requests
                        // from the Go backend's 302 attachment endpoint.
                        enableTextSelection: true,
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

  Future<void> _launchExternal(String url) async {
    final uri = Uri.parse(url);
    if (await canLaunchUrl(uri)) {
      await launchUrl(uri, mode: LaunchMode.externalApplication);
    } else if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('无法打开此附件，请稍后重试。')),
      );
    }
  }

  // ---------------------------------------------------------------------------
  // Quiz blocker + completion (reused & tightened from previous version)
  // ---------------------------------------------------------------------------

  Widget _buildQuizBlockerOverlay() {
    if (_quizFinished) return _buildQuizFinishedScreen();
    final quiz = _aiContent!.postReviewQuiz[_currentQuizIndex];
    return Positioned.fill(
      child: Container(
        color: const Color(0x900F172A),
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
                  Row(
                    mainAxisAlignment: MainAxisAlignment.spaceBetween,
                    children: [
                      Container(
                        padding: const EdgeInsets.symmetric(
                            horizontal: 12, vertical: 6),
                        decoration: BoxDecoration(
                          color: AppTheme.accentGreen.withOpacity(0.15),
                          borderRadius: BorderRadius.circular(12),
                        ),
                        child: Row(
                          children: const [
                            Icon(Icons.emoji_events_rounded,
                                color: AppTheme.accentGreen, size: 18),
                            SizedBox(width: 8),
                            Text('课后小挑战',
                                style: TextStyle(
                                    color: AppTheme.accentGreen,
                                    fontWeight: FontWeight.bold)),
                          ],
                        ),
                      ),
                      Text(
                        '问题 ${_currentQuizIndex + 1} / ${_aiContent!.postReviewQuiz.length}',
                        style: const TextStyle(
                            color: AppTheme.textMuted,
                            fontWeight: FontWeight.bold),
                      ),
                    ],
                  ),
                  const SizedBox(height: 24),
                  Text(quiz.question,
                      style: const TextStyle(
                          fontSize: 20,
                          fontWeight: FontWeight.w900,
                          height: 1.4,
                          color: AppTheme.textWhite)),
                  const SizedBox(height: 24),
                  Column(
                    children: List.generate(quiz.options.length, (index) {
                      final optionText = quiz.options[index];
                      final isSelected = _selectedAnswerIndex == index;
                      final isCorrect = quiz.answerIndex == index;
                      Color bg = Colors.white;
                      Color shadow = const Color(0xFFE2E8F0);
                      Border customBorder = Border.all(
                          color: const Color(0xFFF1F5F9), width: 2.0);
                      if (_selectedAnswerIndex != null) {
                        if (isCorrect) {
                          bg = const Color(0xFFECFDF5);
                          shadow = const Color(0xFFA7F3D0);
                          customBorder = Border.all(
                              color: AppTheme.accentGreen, width: 2.0);
                        } else if (isSelected) {
                          bg = const Color(0xFFFEF2F2);
                          shadow = const Color(0xFFFCA5A5);
                          customBorder = Border.all(
                              color: Colors.redAccent, width: 2.0);
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
                                  color: isSelected
                                      ? AppTheme.primaryColor
                                      : const Color(0xFFF1F5F9),
                                ),
                                child: Text(
                                  String.fromCharCode(65 + index),
                                  style: TextStyle(
                                      fontWeight: FontWeight.w900,
                                      color: isSelected
                                          ? Colors.white
                                          : AppTheme.textWhite),
                                ),
                              ),
                              const SizedBox(width: 16),
                              Expanded(
                                child: Text(optionText,
                                    style: const TextStyle(
                                        fontSize: 15,
                                        color: AppTheme.textWhite,
                                        fontWeight: FontWeight.bold)),
                              ),
                              if (_selectedAnswerIndex != null && isCorrect)
                                const Icon(Icons.check_circle_rounded,
                                    color: AppTheme.accentGreen),
                              if (_selectedAnswerIndex != null &&
                                  isSelected &&
                                  !isCorrect)
                                const Icon(Icons.cancel_rounded,
                                    color: Colors.redAccent),
                            ],
                          ),
                        ),
                      );
                    }),
                  ),
                  const SizedBox(height: 24),
                  if (_selectedAnswerIndex != null)
                    Align(
                      alignment: Alignment.centerRight,
                      child: Button3D.blue(
                        onPressed: _onQuizNext,
                        padding: const EdgeInsets.symmetric(
                            horizontal: 32, vertical: 12),
                        child: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: const [
                            Text('下一题',
                                style: TextStyle(
                                    fontWeight: FontWeight.w900,
                                    fontSize: 16,
                                    color: Colors.white)),
                            SizedBox(width: 8),
                            Icon(Icons.arrow_forward_rounded,
                                size: 20, color: Colors.white),
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
                  const Icon(Icons.stars_rounded,
                      color: AppTheme.accentOrange, size: 80),
                  const SizedBox(height: 24),
                  const Text('恭喜你通关成功！',
                      style: TextStyle(
                          fontSize: 28,
                          fontWeight: FontWeight.w900,
                          color: AppTheme.accentOrange)),
                  const SizedBox(height: 12),
                  Text('您已看完了课时：${widget.episode.title}',
                      textAlign: TextAlign.center,
                      style: const TextStyle(
                          color: AppTheme.textMuted,
                          fontSize: 16,
                          fontWeight: FontWeight.bold)),
                  const SizedBox(height: 32),
                  Container(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 24, vertical: 16),
                    decoration: BoxDecoration(
                      color: const Color(0xFFF8FAFC),
                      borderRadius: BorderRadius.circular(20),
                      border: Border.all(color: const Color(0xFFE2E8F0)),
                    ),
                    child: Row(
                      mainAxisAlignment: MainAxisAlignment.center,
                      children: [
                        const Text('本次学习奖励积分：',
                            style: TextStyle(
                                fontSize: 16,
                                fontWeight: FontWeight.bold,
                                color: AppTheme.textWhite)),
                        Text(' +$_earnedPoints 星币',
                            style: const TextStyle(
                                fontSize: 20,
                                fontWeight: FontWeight.w900,
                                color: AppTheme.accentGreen)),
                      ],
                    ),
                  ),
                  const SizedBox(height: 40),
                  Button3D.blue(
                    onPressed: () => Navigator.pop(context),
                    padding: const EdgeInsets.symmetric(
                        horizontal: 48, vertical: 16),
                    child: const Text('好的，关闭播放',
                        style: TextStyle(
                            fontSize: 18,
                            fontWeight: FontWeight.w900,
                            color: Colors.white)),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  // ---------------------------------------------------------------------------
  // Fullscreen helpers
  // ---------------------------------------------------------------------------

  Widget _buildLoadingScreen() {
    return const Scaffold(
      backgroundColor: Colors.black,
      body: Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            CircularProgressIndicator(color: AppTheme.primaryColor),
            SizedBox(height: 16),
            Text('正在召唤播放内核…',
                style: TextStyle(color: Colors.white70, fontWeight: FontWeight.bold)),
          ],
        ),
      ),
    );
  }

  Widget _buildErrorScreen() {
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
              Text(_errorMessage,
                  textAlign: TextAlign.center,
                  style: const TextStyle(fontSize: 16, color: Colors.redAccent)),
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

  String _formatDuration(Duration d) {
    String two(int n) => n.toString().padLeft(2, '0');
    final h = d.inHours;
    if (h > 0) {
      return '$h:${two(d.inMinutes.remainder(60))}:${two(d.inSeconds.remainder(60))}';
    }
    return '${two(d.inMinutes)}:${two(d.inSeconds.remainder(60))}';
  }
}

/// Tiny model for popup menu entries inside the controls bar.
class _PopupItem {
  final String label;
  final VoidCallback onTap;
  const _PopupItem({required this.label, required this.onTap});
}

/// A [SliderTrackShape] that paints a three-segment seek bar:
///   played (activeColor) | buffered (bufferedColor) | unbuffered (trackColor).
///
/// Standard Material Slider only shows played vs. unplayed; for a video player
/// we also want to show how far ahead the demuxer has buffered, so the user
/// knows whether a seek target is ready to play instantly.
class _BufferedSeekBarTrackShape extends RoundedRectSliderTrackShape {
  _BufferedSeekBarTrackShape({
    required this.bufferedFraction,
    required this.bufferedColor,
  });

  /// Fraction of total duration already buffered, in [0, 1].
  final double bufferedFraction;
  final Color bufferedColor;

  @override
  void paint(
    PaintingContext context,
    Offset offset, {
    required RenderBox parentBox,
    required SliderThemeData sliderTheme,
    required Animation<double> enableAnimation,
    required TextDirection textDirection,
    required Offset thumbCenter,
    Offset? secondaryOffset,
    bool isDiscrete = false,
    bool isEnabled = false,
    double additionalActiveTrackHeight = 0,
  }) {
    if (sliderTheme.trackHeight == null) return;
    final trackHeight = sliderTheme.trackHeight!;
    final radius = Radius.circular(trackHeight / 2);

    // Compute the track rect manually. The Slider leaves horizontal padding
    // for the thumb; we mirror the default Material layout (thumb radius
    // ≈ trackHeight to keep things simple).
    final thumbGap = trackHeight;
    final trackLeft = offset.dx + thumbGap;
    final trackRight = offset.dx + parentBox.size.width - thumbGap;
    final trackTop = offset.dy + (parentBox.size.height - trackHeight) / 2;
    final trackRect = Rect.fromLTRB(
        trackLeft, trackTop, trackRight, trackTop + trackHeight);

    // Layer 1 (bottom): full base track = unbuffered segment.
    context.canvas.drawRRect(
      RRect.fromRectAndCorners(
        Rect.fromLTRB(
            trackRect.left, trackRect.top, trackRect.right, trackRect.bottom),
        topLeft: radius,
        topRight: radius,
        bottomLeft: radius,
        bottomRight: radius,
      ),
      Paint()..color = sliderTheme.inactiveTrackColor ?? Colors.white12,
    );

    // Layer 2: buffered segment [0 .. bufferedFraction].
    final bufferEnd =
        trackRect.left + trackRect.width * bufferedFraction.clamp(0.0, 1.0);
    if (bufferEnd > trackRect.left) {
      context.canvas.drawRRect(
        RRect.fromRectAndCorners(
          Rect.fromLTRB(
              trackRect.left, trackRect.top, bufferEnd, trackRect.bottom),
          topLeft: radius,
          topRight: radius,
          bottomLeft: radius,
          bottomRight: radius,
        ),
        Paint()..color = bufferedColor,
      );
    }

    // Layer 3 (top): played segment [0 .. thumbCenter] in active color.
    context.canvas.drawRRect(
      RRect.fromRectAndCorners(
        Rect.fromLTRB(
            trackRect.left, trackRect.top, thumbCenter.dx, trackRect.bottom),
        topLeft: radius,
        topRight: radius,
        bottomLeft: radius,
        bottomRight: radius,
      ),
      Paint()..color = sliderTheme.activeTrackColor ?? AppTheme.primaryColor,
    );
  }
}


