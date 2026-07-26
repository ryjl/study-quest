import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';
import 'package:url_launcher/url_launcher.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:screen_brightness/screen_brightness.dart';
import '../../config.dart';
import '../../model/course.dart';
import '../../model/quiz.dart';
import '../../service/api_service.dart';
import '../../service/track_selection_controller.dart';
import '../../service/ui_prefs.dart';
import '../../service/tv_mode.dart';
import '../ai/ai_availability.dart';
import 'ai_study_screen.dart';
import '../../theme.dart';
import '../widget/button_3d.dart';
import '../widget/buffered_seek_bar.dart';
import '../widget/focus_button.dart';
import '../widget/helper_panel.dart';
import '../widget/inline_chip_menu.dart';
import '../widget/pdf_viewer_dialog.dart';

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
///   4. Reports progress every 5s (anti-cheat). Quiz is entered manually via
///      the AI icon / helper panel / course detail button, not by playback
///      progress.
class PlayerScreen extends StatefulWidget {
  final int activeUserId;
  final Episode episode;

  /// Optional real pre-adventure prompts (AI-generated). When empty the panel
  /// falls back to placeholder copy.
  final List<String> preAdventureTasks;

  /// 当为 true 时,helper panel 的 AI 学习入口和顶栏 AI 图标都不渲染。
  /// 用于从 AI 学习页"跳转视频"push 出来的播放器:这个播放器是 AI 页的子页,
  /// 它再进 AI 页就会形成 AI 页→播放器→AI 页→… 的无限栈。禁掉入口从根上断环。
  final bool disableAiTab;

  /// 可选的初始播放位置(秒级,但用 Duration 表达)。非 null 时,初始化阶段把
  /// 它喂给现有的 _resumeTarget/_pendingResume 机制——播放器打开后会 seek 到这里。
  /// 典型来源:AI 学习页"跳转视频 12:38"按钮传进来。
  final Duration? initialPosition;

  const PlayerScreen({
    Key? key,
    required this.activeUserId,
    required this.episode,
    this.preAdventureTasks = const [],
    this.disableAiTab = false,
    this.initialPosition,
  }) : super(key: key);

  @override
  State<PlayerScreen> createState() => _PlayerScreenState();
}

class _PlayerScreenState extends State<PlayerScreen> {
  // media_kit core
  late final Player _player;
  late VideoController _controller;
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
  // Phase 2:课前探险问题数据源切到 /ai-summary 的 pre_adventure,"带着问题看"
  // 也读 _summary.pre_adventure。老管线 /ai-content 已在 Phase 5 删除。
  EpisodeSummary? _summary;
  bool _loadingExtras = true;

  // Subtitle selection (0 = off, otherwise 1-based into subtitles list)
  int _selectedSubtitle = 0;

  // Anti-cheat progress logging
  Timer? _progressTimer;
  int _lastLoggedPosition = 0;
  // Resume bookkeeping — the seek must wait until media_kit reports a real
  // duration, otherwise the seek silently no-ops.

  // Auto-hide controls
  bool _controlsVisible = true;
  Timer? _hideTimer;

  // Fullscreen + extra controls state
  bool _isFullscreen = false;
  bool _controlsLocked = false;
  double _rate = 1.0;
  double _volumeBeforeMute = 50.0;
  String _activeMenu = '';
  // native(libmpv 内置)字幕轨 id 集合。用 Set 去重 —— 修复字幕按钮重复 bug(需求 #4):
  // 之前用 List,tracks 事件多次触发时会重复 add 同一个 id,导致字幕菜单里出现多个
  // 同名「中文」按钮,点一个又触发新事件再 add,越点越多。
  final Set<String> _nativeSubtitleIds = {};
  bool _nativeSubtitlesCaptured = false;
  // Drag-to-seek transient state. While the user is dragging the seek bar we
  // only update a local preview position (no seek); a single seek is committed
  // on change-end. Without this, dragging fires dozens of seek() calls and
  // tears the libmpv demuxer down on every frame.
  bool _isDraggingSeek = false;
  Duration _dragPosition = Duration.zero;

  // Helper panel layout state
  bool _helperPanelInitialized = false;
  bool _showHelperPanel = false;

  // Gestures Overlay indicators
  bool _showVolumeIndicator = false;
  double _volumeIndicatorVal = 0.0;
  bool _showBrightnessIndicator = false;
  double _brightnessIndicatorVal = 0.0;
  bool _showFastForwardIndicator = false;
  String _fastForwardText = '';
  Timer? _indicatorTimer;

  @override
  void initState() {
    super.initState();
    // Force landscape: video is widescreen content and the 70/30 split layout
    // (video + helper panel) is designed for landscape. Restored to all
    // orientations on dispose so the rest of the app can rotate freely.
    SystemChrome.setPreferredOrientations([
      DeviceOrientation.landscapeLeft,
      DeviceOrientation.landscapeRight,
    ]);
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

    _initializeVideo();
    _loadExtras();
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
    // Restore all orientations so the rest of the app can rotate freely.
    SystemChrome.setPreferredOrientations([
      DeviceOrientation.landscapeLeft,
      DeviceOrientation.landscapeRight,
      DeviceOrientation.portraitUp,
      DeviceOrientation.portraitDown,
    ]);
    super.dispose();
  }

  // ---------------------------------------------------------------------------
  // Setup
  // ---------------------------------------------------------------------------

  Future<void> _initializeVideo() async {
    _nativeSubtitleIds.clear();
    _nativeSubtitlesCaptured = false;
    try {
      final prefs = await SharedPreferences.getInstance();
      final enableHw = prefs.getBool('enable_hw_acceleration') ?? false;
      _controller = VideoController(_player,
          configuration: VideoControllerConfiguration(
            enableHardwareAcceleration: enableHw,
          ));

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
      // initialPosition(AI 学习页"跳转视频"传进来的目标时间戳)优先级最高:
      // 它是用户显式想看的位置,覆盖断点续播。复用同一套 _pendingResume /
      // _resumeTarget / watchdog 机制,这样 CDN 掉线重连把位置重置回 0 时也会被
      // 自动 seek 回跳转点(行为和断点续播一致)。
      final jumpTo = widget.initialPosition;
      final hasJump = jumpTo != null && jumpTo.inSeconds > 0;
      if (hasJump) {
        _pendingResume = jumpTo;
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
        start: (shouldResume || hasJump) ? _pendingResume : null,
      );

      // Resume / 跳转目标:open with auto-play(默认),然后用 watchdog 在位置被
      // 重置时 re-seek 回目标。netdisk 流的 CDN 连接可能中途断开,libmpv 会从 0
      // 重开流;open(play:false)+seek+play 也不顶用(play() 自己会重置位置)。
      // 唯一靠谱的办法是反复 seek,直到播放稳定越过目标位置。详见 _setupResumeSeek。
      if (shouldResume || hasJump) {
        _resumeTarget = _pendingResume!;
        _player.open(media);
        _setupResumeSeek();
      } else {
        _player.open(media);
      }

      _player.stream.tracks.listen((tracks) {
        if (mounted) {
          if (!_nativeSubtitlesCaptured && tracks.subtitle.isNotEmpty) {
            _nativeSubtitleIds.clear();
            for (var t in tracks.subtitle) {
              if (t.id != 'no' && t.id != 'auto') {
                _nativeSubtitleIds.add(t.id);
              }
            }
            if (_player.state.duration > Duration.zero) {
              _nativeSubtitlesCaptured = true;
            }
          }
          final options = _getSubtitleOptions();
          if (_selectedSubtitle == 0) {
            _autoSelectDefaultSubtitle(options);
          }
        }
      });

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

  /// Fetch real attachments + summary in parallel (non-blocking).
  Future<void> _loadExtras() async {
    try {
      final results = await Future.wait([
        ApiService.fetchAttachments(widget.activeUserId, widget.episode.id)
            .catchError((_) => <Attachment>[]),
        // summary.pre_adventure 是课前探险问题的数据源。
        // 404(无 summary / AI 未开)返回 null,这里再 catchError 兜底容错。
        ApiService.fetchEpisodeSummary(widget.activeUserId, widget.episode.id)
            .catchError((_) => null),
      ]);
      if (mounted) {
        setState(() {
          _attachments = results[0] as List<Attachment>;
          _summary = results[1] as EpisodeSummary?;
          _loadingExtras = false;
        });
      }
    } catch (_) {
      if (mounted) setState(() => _loadingExtras = false);
    }
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

  // ---------------------------------------------------------------------------
  // Controls auto-hide
  // ---------------------------------------------------------------------------

  void _scheduleAutoHide() {
    _hideTimer?.cancel();
    _hideTimer = Timer(const Duration(seconds: 4), () {
      if (mounted && _player.state.playing) {
        setState(() {
          _controlsVisible = false;
          _activeMenu = '';
        });
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

    if (!_helperPanelInitialized) {
      // TV 模式下强制展开 helper panel(需求 #9)—— TV 屏幕大、交互靠 D-pad,
      // 隐藏式 chevron 入口反而难发现,直接常驻右侧随堂助手更顺手。
      _showHelperPanel =
          TvMode.instance.isActive || MediaQuery.of(context).size.width >= 900;
      _isFullscreen = !_showHelperPanel;
      _helperPanelInitialized = true;
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
              Row(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Expanded(
                    child: _buildVideoArea(),
                  ),
                  if (!_isFullscreen && _showHelperPanel)
                    SizedBox(
                      width: MediaQuery.of(context).size.width >= 900 ? 360 : 300,
                      child: _buildHelperPanel(),
                    ),
                ],
              ),

              // Buffering spinner centered over the video. We listen to the
              // buffering stream (not state) so the overlay rebuilds promptly,
              // and we only show it while actually playing — a paused player
              // reports buffering too, which would leave a stale spinner after
              // a seek that lands in a buffered region.
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
      // TV 模式下遥控器左右键 seek 步长加大到 30s(需求 #9)—— 10s 在 TV 上太小,
      // 长视频快进/后退体验差。触屏双击手势保持 ±10s 不变(那是近距离精确跳)。
      final step = TvMode.instance.isActive ? 30 : 10;
      _seekRelative(Duration(seconds: -step));
      return KeyEventResult.handled;
    } else if (key == LogicalKeyboardKey.arrowRight) {
      final step = TvMode.instance.isActive ? 30 : 10;
      _seekRelative(Duration(seconds: step));
      return KeyEventResult.handled;
    } else if (key == LogicalKeyboardKey.arrowUp) {
      // 非 TV:上下键调音量(PAD/手机无独立音量键的便捷操作)。
      // TV:不处理 —— 交给焦点遍历系统,让 D-pad 上下能在视频区和 helper panel
      // 之间移动焦点(TV 遥控器通常有独立音量键,不需要 App 内再占用上下键)。
      if (!TvMode.instance.isActive) {
        _player.setVolume((_player.state.volume + 5).clamp(0, 100));
        return KeyEventResult.handled;
      }
      return KeyEventResult.ignored;
    } else if (key == LogicalKeyboardKey.arrowDown) {
      if (!TvMode.instance.isActive) {
        _player.setVolume((_player.state.volume - 5).clamp(0, 100));
        return KeyEventResult.handled;
      }
      return KeyEventResult.ignored;
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

  // Gesture Helper states
  double _dragVolumeStart = 0.0;
  double _dragBrightnessStart = 0.5;
  double _dragStartOffset = 0.0;
  double _rateBeforeLongPress = 1.0;

  Future<void> _setBrightness(double val) async {
    try {
      await ScreenBrightness().setScreenBrightness(val.clamp(0.0, 1.0));
    } catch (_) {}
  }
  Future<double> _getBrightness() async {
    try {
      return await ScreenBrightness().current;
    } catch (_) {
      return 0.5;
    }
  }

  void _showVolumeOverlay(double val) {
    _indicatorTimer?.cancel();
    setState(() {
      _showVolumeIndicator = true;
      _volumeIndicatorVal = val;
      _showBrightnessIndicator = false;
      _showFastForwardIndicator = false;
    });
    _indicatorTimer = Timer(const Duration(milliseconds: 1000), () {
      if (mounted) setState(() => _showVolumeIndicator = false);
    });
  }

  void _showBrightnessOverlay(double val) {
    _indicatorTimer?.cancel();
    setState(() {
      _showBrightnessIndicator = true;
      _brightnessIndicatorVal = val;
      _showVolumeIndicator = false;
      _showFastForwardIndicator = false;
    });
    _indicatorTimer = Timer(const Duration(milliseconds: 1000), () {
      if (mounted) setState(() => _showBrightnessIndicator = false);
    });
  }

  void _showFFRewindIndicator(String text) {
    _indicatorTimer?.cancel();
    setState(() {
      _showFastForwardIndicator = true;
      _fastForwardText = text;
      _showVolumeIndicator = false;
      _showBrightnessIndicator = false;
    });
    _indicatorTimer = Timer(const Duration(milliseconds: 1000), () {
      if (mounted) setState(() => _showFastForwardIndicator = false);
    });
  }

  void _showFastForwardOverlay(String text) {
    _indicatorTimer?.cancel();
    setState(() {
      _showFastForwardIndicator = true;
      _fastForwardText = text;
      _showVolumeIndicator = false;
      _showBrightnessIndicator = false;
    });
  }

  void _hideFFIndicator() {
    setState(() => _showFastForwardIndicator = false);
  }

  Widget _buildVideoArea() {
    return LayoutBuilder(
      builder: (context, constraints) {
        final videoWidth = constraints.maxWidth;
        return Stack(
          fit: StackFit.expand,
          children: [
            // 1. Video surface
            Positioned.fill(
              child: Container(
                color: Colors.black,
                child: Video(
                  controller: _controller,
                  fill: Colors.black,
                  controls: NoVideoControls,
                  subtitleViewConfiguration: SubtitleViewConfiguration(
                    style: TextStyle(
                      // 字号直接读全局 UiPrefs —— 用户在字幕菜单里改档位会
                      // setSubtitleSizeIndex 写回并 setState,这里随之重建拿到新值。
                      fontSize: UiPrefs.instance.subtitleSize,
                      color: Colors.white,
                      fontWeight: FontWeight.bold,
                      backgroundColor: Colors.black38,
                    ),
                    padding: EdgeInsets.only(
                      bottom: _controlsVisible ? 100.0 : 24.0,
                    ),
                  ),
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

            // 3. Top-level gesture layer
            if (!_controlsLocked)
              Positioned.fill(
                child: GestureDetector(
                  behavior: HitTestBehavior.translucent,
                  onTap: _toggleControls,
                  onDoubleTapDown: (details) {
                    final dx = details.localPosition.dx;
                    if (dx < videoWidth * 0.35) {
                      _seekRelative(const Duration(seconds: -10));
                      _showFFRewindIndicator('-10s');
                    } else if (dx > videoWidth * 0.65) {
                      _seekRelative(const Duration(seconds: 10));
                      _showFFRewindIndicator('+10s');
                    } else {
                      _togglePlayPause();
                    }
                  },
                  onHorizontalDragStart: (_) => _onSeekDragStart(),
                  onHorizontalDragUpdate: (details) =>
                      _onSeekDragUpdate(details.delta.dx, videoWidth),
                  onHorizontalDragEnd: (_) => _onSeekDragEnd(),
                  onVerticalDragStart: (details) {
                    final isRight = details.localPosition.dx > videoWidth / 2;
                    _dragStartOffset = details.localPosition.dy;
                    if (isRight) {
                      _dragVolumeStart = _player.state.volume;
                    } else {
                      _getBrightness().then((b) => _dragBrightnessStart = b);
                    }
                  },
                  onVerticalDragUpdate: (details) {
                    final isRight = details.localPosition.dx > videoWidth / 2;
                    final dy = _dragStartOffset - details.localPosition.dy;
                    final pct = dy / 200.0; // 200px drag represents 100% change
                    if (isRight) {
                      final newVol = (_dragVolumeStart + pct * 100).clamp(0.0, 100.0);
                      _setVolume(newVol);
                      _showVolumeOverlay(newVol);
                    } else {
                      final newBright = (_dragBrightnessStart + pct).clamp(0.0, 1.0);
                      _setBrightness(newBright);
                      _showBrightnessOverlay(newBright);
                    }
                  },
                  onLongPressStart: (_) {
                    _rateBeforeLongPress = _rate;
                    _setRate(2.0);
                    _showFastForwardOverlay("2.0x 倍速播放中");
                  },
                  onLongPressEnd: (_) {
                    _setRate(_rateBeforeLongPress);
                    _hideFFIndicator();
                  },
                ),
              ),

            // Volume Overlay Indicator
            if (_showVolumeIndicator)
              Center(
                child: Container(
                  padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
                  decoration: BoxDecoration(
                    color: Colors.black87,
                    borderRadius: BorderRadius.circular(12),
                  ),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      const Icon(Icons.volume_up_rounded, color: Colors.white, size: 24),
                      const SizedBox(width: 8),
                      Text(
                        '音量: ${_volumeIndicatorVal.round()}%',
                        style: const TextStyle(color: Colors.white, fontWeight: FontWeight.bold, fontSize: 16),
                      ),
                    ],
                  ),
                ),
              ),

            // Brightness Overlay Indicator
            if (_showBrightnessIndicator)
              Center(
                child: Container(
                  padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
                  decoration: BoxDecoration(
                    color: Colors.black87,
                    borderRadius: BorderRadius.circular(12),
                  ),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      const Icon(Icons.brightness_5_rounded, color: Colors.white, size: 24),
                      const SizedBox(width: 8),
                      Text(
                        '亮度: ${(_brightnessIndicatorVal * 100).round()}%',
                        style: const TextStyle(color: Colors.white, fontWeight: FontWeight.bold, fontSize: 16),
                      ),
                    ],
                  ),
                ),
              ),

            // Fast Forward / Seek Overlay Indicator
            if (_showFastForwardIndicator)
              Center(
                child: Container(
                  padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
                  decoration: BoxDecoration(
                    color: Colors.black87,
                    borderRadius: BorderRadius.circular(12),
                  ),
                  child: Text(
                    _fastForwardText,
                    style: const TextStyle(color: Colors.white, fontWeight: FontWeight.bold, fontSize: 16),
                  ),
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

            // Chevron tab to open helper panel when it is closed
            if (!_isFullscreen && !_showHelperPanel)
              Positioned(
                right: 0,
                top: 0,
                bottom: 0,
                child: Center(
                  child: GestureDetector(
                    onTap: () {
                      setState(() {
                        _showHelperPanel = true;
                        _isFullscreen = false;
                      });
                    },
                    child: Container(
                      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 16),
                      decoration: const BoxDecoration(
                        color: Colors.black54,
                        borderRadius: BorderRadius.only(
                          topLeft: Radius.circular(16),
                          bottomLeft: Radius.circular(16),
                        ),
                      ),
                      child: const Icon(Icons.chevron_left_rounded, color: Colors.white, size: 24),
                    ),
                  ),
                ),
              ),

            // 5. Controls overlay (top bar + bottom bar). When hidden, this
            //    subtree is removed so taps fall through to layer 3.
            if (_controlsVisible)
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
              Navigator.pop(context);
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
          // AI study entry — opens the AI study page (summary + practice). On
          // return, if the user tapped a "[跳转 12:38]" link, pop receives a
          // JumpRequest and we seek the player there.
          // Phase 2:三态 gating 与课程详情页一致(走同一 helper)。不可用时图标
          // 置灰(active=false)、点击弹 SnackBar 提示原因,不进入 AiStudyScreen。
          // disableAiTab(AI 跳转 push 出来的播放器):整个入口不渲染,防栈无限加深。
          if (!widget.disableAiTab)
            Builder(builder: (iconCtx) {
              final availability =
                  AiAvailabilityHelper.fromEpisode(widget.episode);
              final enabled = availability == AiAvailability.enabled;
              return _iconControl(
                icon: Icons.auto_awesome_rounded,
                active: enabled,
                onTap: () async {
                  if (!enabled) {
                    ScaffoldMessenger.of(iconCtx).showSnackBar(
                      SnackBar(
                        content: Text(
                            AiAvailabilityHelper.tooltipFor(availability)!),
                        duration: const Duration(seconds: 2),
                      ),
                    );
                    return;
                  }
                  // 进 AI 学习页前暂停视频,避免在后台继续播放(含音频)。
                  // 返回时不自动 resume——用户可能只想看完解析,让其手动点播放更可控。
                  _player.pause();
                  final result = await Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (context) => AiStudyScreen(
                        activeUserId: widget.activeUserId,
                        episode: widget.episode,
                      ),
                    ),
                  );
                  if (result is JumpRequest && mounted) {
                    _seekTo(result.target);
                  }
                },
              );
            }),
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
                        if (_activeMenu == 'speed') _buildSpeedInlineMenu(),
                        if (_activeMenu == 'subtitle') _buildSubtitleInlineMenu(),
                        if (_activeMenu == 'audio') _buildAudioInlineMenu(),
                        const SizedBox(height: 8),
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
              trackShape: BufferedSeekBarTrackShape(
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


  List<Map<String, dynamic>> _getSubtitleOptions() {
    return TrackSelectionController.subtitleOptions(
      player: _player,
      nativeSubtitleIds: _nativeSubtitleIds,
      backendSubtitles: _playInfo?.subtitles ?? const [],
    );
  }

  void _autoSelectDefaultSubtitle(List<Map<String, dynamic>> options) {
    final currentTrack = _player.state.track.subtitle;
    if (currentTrack.id != 'no') {
      final idx = options.indexWhere((opt) => opt['type'] == 'native' && opt['track'].id == currentTrack.id);
      if (idx != -1) {
        setState(() => _selectedSubtitle = idx);
        return;
      }
    }

    final firstNativeIdx = options.indexWhere((opt) => opt['type'] == 'native');
    if (firstNativeIdx != -1) {
      _applySubtitleOption(options[firstNativeIdx], firstNativeIdx);
      return;
    }

    final firstBackendIdx = options.indexWhere((opt) => opt['type'] == 'backend');
    if (firstBackendIdx != -1) {
      _applySubtitleOption(options[firstBackendIdx], firstBackendIdx);
      return;
    }

    setState(() => _selectedSubtitle = 0);
  }

  Future<void> _applySubtitleOption(Map<String, dynamic> opt, int index) async {
    setState(() => _selectedSubtitle = index);
    final type = opt['type'];
    if (type == 'off') {
      await _player.setSubtitleTrack(SubtitleTrack.no());
    } else if (type == 'native') {
      await _player.setSubtitleTrack(opt['track'] as SubtitleTrack);
    } else if (type == 'backend') {
      final sub = opt['track'] as EpisodeSubtitle;
      final url = TrackSelectionController.backendSubtitleUrl(sub);
      await _player.setSubtitleTrack(SubtitleTrack.uri(url, title: sub.label));
    }
  }

  List<Map<String, dynamic>> _getAudioOptions() {
    return TrackSelectionController.audioOptions(_player);
  }

  Future<void> _applyAudioOption(Map<String, dynamic> opt) async {
    await _player.setAudioTrack(opt['track'] as AudioTrack);
    setState(() {});
  }

  Widget _buildSpeedInlineMenu() {
    return InlineChipMenu(
      title: '播放速度：',
      items: [0.5, 0.75, 1.0, 1.25, 1.5, 2.0]
          .map((r) => InlineChipItem(
                label: '${r}x',
                selected: _rate == r,
                onTap: () {
                  _setRate(r);
                  _scheduleAutoHide();
                },
              ))
          .toList(),
    );
  }

  Widget _buildSubtitleInlineMenu() {
    final subtitleOptions = _getSubtitleOptions();
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        InlineChipMenu(
          title: '字幕选择：',
          items: subtitleOptions.asMap().entries.map((entry) {
            final idx = entry.key;
            final opt = entry.value;
            return InlineChipItem(
              label: opt['label'],
              selected: _selectedSubtitle == idx,
              onTap: () async {
                await _applySubtitleOption(opt, idx);
                setState(() {});
                _scheduleAutoHide();
              },
            );
          }).toList(),
        ),
        const SizedBox(height: 10),
        // 字号档位接全局 UiPrefs(需求 #5 + #7):四档「小/中/大/超大」,
        // 选中态读 UiPrefs.subtitleSizeIndex,点击写回 SharedPreferences,
        // 下次进播放器沿用上次选择。改档位后 setState 重建,上面的
        // SubtitleViewConfiguration 会读最新的 UiPrefs.subtitleSize 生效。
        InlineChipMenu(
          title: '字幕大小：',
          items: UiPrefs.subtitleSizeLabels.asMap().entries.map((entry) {
            final index = entry.key;
            final label = entry.value;
            return InlineChipItem(
              label: label,
              selected: UiPrefs.instance.subtitleSizeIndex == index,
              onTap: () async {
                await UiPrefs.instance.setSubtitleSizeIndex(index);
                setState(() {});
                _scheduleAutoHide();
              },
            );
          }).toList(),
        ),
      ],
    );
  }

  Widget _buildAudioInlineMenu() {
    final audioOptions = _getAudioOptions();
    final currentAudio = _player.state.track.audio;
    if (audioOptions.isEmpty) {
      return InlineChipMenu(
        title: '音轨选择：',
        items: const [],
      );
    }
    return InlineChipMenu(
      title: '音轨选择：',
      items: audioOptions.map((opt) {
        final track = opt['track'] as AudioTrack;
        return InlineChipItem(
          label: opt['label'],
          selected: currentAudio.id == track.id,
          onTap: () async {
            await _applyAudioOption(opt);
            setState(() {});
            _scheduleAutoHide();
          },
        );
      }).toList(),
    );
  }

  void _toggleMute() {
    final currentVol = _player.state.volume;
    if (currentVol > 0.0) {
      _volumeBeforeMute = currentVol;
      _setVolume(0.0);
    } else {
      _setVolume(_volumeBeforeMute);
    }
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
        const Spacer(),
        // Volume controls: Icon + Slider
        StreamBuilder<double>(
          stream: _player.stream.volume,
          initialData: _player.state.volume,
          builder: (context, snap) {
            final v = snap.data ?? _player.state.volume;
            final isMuted = v == 0.0;
            return Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                GestureDetector(
                  onTap: _toggleMute,
                  child: Icon(
                    isMuted ? Icons.volume_off_rounded : Icons.volume_up_rounded,
                    color: Colors.white,
                    size: 24,
                  ),
                ),
                const SizedBox(width: 4),
                SizedBox(
                  width: 70,
                  child: SliderTheme(
                    data: SliderTheme.of(context).copyWith(
                      trackHeight: 3,
                      activeTrackColor: AppTheme.primaryColor,
                      inactiveTrackColor: Colors.white24,
                      thumbColor: Colors.white,
                      thumbShape: const RoundSliderThumbShape(enabledThumbRadius: 5),
                      overlayShape: const RoundSliderOverlayShape(overlayRadius: 10),
                      trackShape: const RectangularSliderTrackShape(),
                    ),
                    child: Slider(
                      value: v.clamp(0.0, 100.0),
                      min: 0,
                      max: 100,
                      onChanged: (val) {
                        _setVolume(val);
                      },
                    ),
                  ),
                ),
              ],
            );
          },
        ),
        const SizedBox(width: 12),
        // Playback Speed
        FocusButton(
          onPressed: () {
            setState(() {
              _activeMenu = _activeMenu == 'speed' ? '' : 'speed';
            });
            _scheduleAutoHide();
          },
          borderRadius: 20,
          baseColor: _activeMenu == 'speed' ? AppTheme.primaryColor : Colors.white12,
          borderColor: Colors.transparent,
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          child: Text(
            '${_rate}x',
            style: const TextStyle(color: Colors.white, fontWeight: FontWeight.bold, fontSize: 13),
          ),
        ),
        const SizedBox(width: 12),
        // Subtitles
        _iconControl(
          icon: Icons.subtitles_rounded,
          onTap: () {
            setState(() {
              _activeMenu = _activeMenu == 'subtitle' ? '' : 'subtitle';
            });
            _scheduleAutoHide();
          },
          active: _activeMenu == 'subtitle',
        ),
        const SizedBox(width: 12),
        // Audio Tracks
        if (_getAudioOptions().isNotEmpty) ...[
          _iconControl(
            icon: Icons.audiotrack_rounded,
            onTap: () {
              setState(() {
                _activeMenu = _activeMenu == 'audio' ? '' : 'audio';
              });
              _scheduleAutoHide();
            },
            active: _activeMenu == 'audio',
          ),
          const SizedBox(width: 12),
        ],
        // Fullscreen toggle
        _iconControl(
          icon: _isFullscreen
              ? Icons.fullscreen_exit_rounded
              : Icons.fullscreen_rounded,
          onTap: _toggleFullscreen,
        ),
      ],
    );
  }

  /// A D-pad focusable circular icon button used in the controls bar.
  Widget _iconControl({
    required IconData icon,
    required VoidCallback onTap,
    bool active = false,
  }) {
    return FocusButton(
      onPressed: onTap,
      borderRadius: 24,
      baseColor: active ? AppTheme.primaryColor : Colors.white12,
      borderColor: Colors.transparent,
      padding: const EdgeInsets.all(8),
      child: Icon(icon, color: Colors.white, size: 28),
    );
  }

  // ---------------------------------------------------------------------------
  // Helper panel (right ~30%)
  // ---------------------------------------------------------------------------

  Widget _buildHelperPanel() {
    return HelperPanel(
      episode: widget.episode,
      attachments: _attachments,
      loadingExtras: _loadingExtras,
      summary: _summary,
      preAdventureTasks: widget.preAdventureTasks,
      disableAiTab: widget.disableAiTab,
      tvModeActive: TvMode.instance.isActive,
      onClosePanel: () {
        setState(() {
          _showHelperPanel = false;
          _isFullscreen = true;
        });
      },
      onOpenAttachment: (att) => _openAttachment(att),
      onEnterAiStudy: () async {
        // 进 AI 学习页前暂停视频,避免在后台继续播放(含音频)。
        // 与顶栏 AI 入口行为一致 —— 之前 helper panel 这个常驻入口漏了 pause,
        // 导致从卡片进 AI 页后视频还在后台放(需求 #1)。
        _player.pause();
        await Navigator.of(context).push(
          MaterialPageRoute(
            builder: (context) => AiStudyScreen(
              activeUserId: widget.activeUserId,
              episode: widget.episode,
            ),
          ),
        );
      },
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
      PdfViewerDialog.show(context, att, streamUrl);
    } else {
      _launchExternal(streamUrl);
    }
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

