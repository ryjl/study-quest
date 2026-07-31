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
import 'ai_study_screen.dart';
import '../../theme.dart';
import '../widget/button_3d.dart';
import '../widget/buffered_seek_bar.dart';
import '../widget/focus_button.dart';
import '../widget/helper_panel.dart';
import '../widget/pdf_viewer_dialog.dart';
import '../widget/player_menu.dart';

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
///
/// **焦点系统**:声明式,完全依赖 Flutter framework。方向键 ◄▲▼► 由
/// MaterialApp 顶层默认 Shortcuts 绑定到 [DirectionalFocusIntent] →
/// [FocusNode.focusInDirection] → framework 的 2D 几何算法自动找空间最近的
/// 可聚焦节点。本屏只在三处做最小拦截(详见 [_onWakeControls] / seek bar
/// 的 onKeyEvent / [dpadEscapeFocusNode] 用于 TextField)。菜单用标准
/// [MenuAnchor] + [RadioMenuButton],焦点隔离 + 几何导航由 framework 保证。
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

  // 长按 ◄► 的加速状态:连续 KeyRepeatEvent 累计次数越多,单次 seek 步长越大
  // (越按越快)。KeyDown(首次按下)重置为 0,KeyUp(松开)重置为 0。
  int _seekRepeatCount = 0;

  // Fullscreen + extra controls state
  // (注:_isFullscreen 现在是 helper panel 显隐的唯一事实源,定义在下面带完整
  // 注释,这里不再重复声明。)
  double _rate = 1.0;
  double _volumeBeforeMute = 50.0;
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
  //
  // 统一模型:helper panel 的显隐只由 _isFullscreen 决定(单一事实源)。
  //   _isFullscreen = false(默认) → 显示右侧 helper panel
  //   _isFullscreen = true        → 全屏沉浸,无 panel
  // 控制行的全屏按钮是唯一开关。砍掉了:右侧 chevron 开关、panel 右上角关闭
  // 按钮、初始化时"TV/宽屏自动展开"的特殊逻辑 —— 三端行为统一,减少心智负担。
  // (历史:曾有 _showHelperPanel + _isFullscreen 两个布尔互相反向,代码里到处
  //  `if (!_isFullscreen && _showHelperPanel)` 双重判断,冗余且易错。)
  bool _isFullscreen = false;

  // 焦点系统的命名 FocusNode,用于 TV 下显式 ▲▼ 跳转(参考 YouTube/爱奇艺 TV)。
  // 不再用旧的分发表 + 几何算法(几何算法在 video+panel 同 scope 时会把 panel
  // 的 FocusButton 当候选,跳错)。改成显式:
  //   seek bar ▲ → 顶栏返回按钮,▼ → 控制行播放按钮
  //   ◄→ 在控制行/顶栏按钮间走几何算法(同区,候选明确)
  //   ► 到控制行右边界 → 顶层 FocusScope(parentScope)跨进 helper panel
  late final FocusNode _seekBarFocus = FocusNode(debugLabel: 'seekBar');
  late final FocusNode _backFocus = FocusNode(debugLabel: 'back');
  late final FocusNode _playPauseFocus = FocusNode(debugLabel: 'playPause');
  // seek bar 聚焦态:聚焦时 thumb 变大 + track/文字高亮,TV 用户能看出焦点在哪。
  // FocusButton 自带发光环,seek bar 是裸 Focus+Slider,得手动 listen focus 改样式。
  bool _seekBarFocused = false;

  // Gestures Overlay indicators
  bool _showVolumeIndicator = false;
  double _volumeIndicatorVal = 0.0;
  bool _showBrightnessIndicator = false;
  double _brightnessIndicatorVal = 0.0;
  // 用户是否在播放器内改过亮度(用于 dispose 时决定是否还原系统亮度)。
  // 不还原会导致退出后整台设备亮度保持低值,直到重启或手动去系统设置调。
  bool _brightnessChanged = false;
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
    _seekBarFocus.dispose();
    _backFocus.dispose();
    _playPauseFocus.dispose();
    // Restore all orientations so the rest of the app can rotate freely.
    SystemChrome.setPreferredOrientations([
      DeviceOrientation.landscapeLeft,
      DeviceOrientation.landscapeRight,
      DeviceOrientation.portraitUp,
      DeviceOrientation.portraitDown,
    ]);
    // 还原系统亮度:用户在播放器内调过低亮度后,若不还原,退出后整台设备会一直
    // 保持低亮度(直到重启)。只在确实改过亮度时 reset(避免无谓的系统调用)。
    // dispose 是同步的,fire-and-forget 即可;reset 失败忽略(设备/权限问题)。
    if (_brightnessChanged) {
      ScreenBrightness().resetScreenBrightness().catchError((_) {});
    }
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

      // 监听播放中途错误(CDN 断流、解码失败等)。初始化阶段的错误由上面的
      // try/catch 覆盖;这里捕获的是播放已就绪后发生的断流——原版不监听,用户只会
      // 看到画面冻住无提示无重试。只在引擎已就绪 + 已拿到时长后显示错误页,避免
      // 把初始化时的非致命 error(某些源的编解码警告)误判成致命错误。
      _player.stream.error.listen((msg) {
        if (!mounted || msg.isEmpty) return;
        if (_engineReady && _player.state.duration > Duration.zero) {
          setState(() => _errorMessage = '播放出错,可能是网络中断:$msg');
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
    // 初始化阶段:等 demuxer 报出真实 duration 后,seek 到 resume 点一次。
    //
    // 不再做 CDN 掉线归 0 的持续看门狗 —— 那套逻辑(监听 position 突变回拉)
    // 分不清"CDN 掉线归 0"和"视频自然播完重播/用户主动操作",反复误伤(把
    // 用户回退或重播强行拉回旧位置)。CDN 真掉线归 0 的情况,用户拖一下进度条
    // 即可恢复,代价远小于一个持续误伤的自动防御。
    _player.stream.duration.listen((duration) {
      if (_resumeSeekDone) return;
      if (duration.inSeconds <= 0 || target >= duration) return;
      _resumeSeekDone = true;
      _player.seek(target);
      _lastLoggedPosition = target.inSeconds;
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
      // 真实播放产生的正向 delta 才计入观看时长。delta ≤ 0(暂停/回退/CDN 重连
      // 归 0)或过大(seek 跳跃)都不算观看时长 —— 但 position 仍然上报,让续播
      // 点跟随实际位置(旧逻辑在 delta<0 时直接 return 跳过上报,导致用户回退
      // 后的进度不保存,下次进来 resume 回旧点)。
      final watchDelta = (delta > 0 && delta <= 30) ? delta : 0;
      try {
        await ApiService.reportProgress(
          activeUserId: widget.activeUserId,
          episodeId: widget.episode.id,
          positionSeconds: currentPos,
          deltaWatchSeconds: watchDelta,
        );
      } catch (_) {}
      // baseline 总是同步到当前位置:无论前进/回退/seek,下次 tick 都从这比。
      _lastLoggedPosition = currentPos;
    });
  }

  // ---------------------------------------------------------------------------
  // Controls auto-hide
  // ---------------------------------------------------------------------------

  void _scheduleAutoHide() {
    _hideTimer?.cancel();
    // 非 TV(触屏)总是 auto-hide;TV 下仅全屏时 auto-hide —— 非全屏有 helper
    // panel 需要常驻操作,全屏沉浸时控件过一会要消失避免挡内容。
    // (控件层现在常驻不卸载,隐藏只影响视觉/触摸,不会让 _seekBarFocus 离开
    // 焦点树,所以 TV auto-hide 不再导致焦点丢失。)
    final shouldHide = !TvMode.instance.isActive || _isFullscreen;
    if (!shouldHide) return;
    _hideTimer = Timer(const Duration(seconds: 4), () {
      if (mounted && _player.state.playing) {
        setState(() {
          _controlsVisible = false;
        });
      }
    });
  }

  void _toggleControls() {
    setState(() => _controlsVisible = !_controlsVisible);
    if (_controlsVisible) {
      _scheduleAutoHide();
      // TV 唤出控件后,焦点落回 seek bar —— 控件隐藏时焦点在全屏 Focus 节点上
      // (canRequestFocus: !_controlsVisible),唤出后要回到 seek bar 才能用
      // D-pad 操作进度条/控制行。延迟到下一帧,等控件重新可交互。
      if (TvMode.instance.isActive) {
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted && _seekBarFocus.canRequestFocus) {
            _seekBarFocus.requestFocus();
          }
        });
      }
    }
  }

  // ---------------------------------------------------------------------------
  // Volume / speed / fullscreen
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

  // 一次性闸门:TV 模式下默认焦点落 seek bar 的初始化只跑一次。
  // 替代了原来的 _helperPanelInitialized(它管的事已经下放到 _isFullscreen 字段初值)。
  bool _focusInitialized = false;

  @override
  Widget build(BuildContext context) {
    if (!_engineReady && _errorMessage.isEmpty) {
      return _buildLoadingScreen();
    }
    if (_errorMessage.isNotEmpty) {
      return _buildErrorScreen();
    }

    // TV 模式下首次进入:焦点落 seek bar —— 最常用的 ◄► seek 直接可用,而不是
    // 靠 autofocus 飘到不确定的子节点。postFrame 等控件行渲染完再 requestFocus。
    if (!_focusInitialized) {
      _focusInitialized = true;
      if (TvMode.instance.isActive) {
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted) _seekBarFocus.requestFocus();
        });
      }
    }

    return PopScope(
      // canPop: false 拦截所有 pop 来源(Android 系统 back、browserBack、ESC 被系统
      // 当 back、Navigator.maybePop),统一在 onPopInvokedWithResult 里做分层:
      // 有菜单(Dialog)→ 关菜单;无菜单 → 退出页面。
      // 不用 canPop: true —— 那样系统 back 会直接 pop,绕过分层(ESC/browserBack
      // 在 mumu/TV 上走系统 back 通道,不进 Shortcuts 的 KeyEvent)。
      canPop: false,
      onPopInvokedWithResult: (didPop, _) {
        if (didPop) return;
        _handlePop();
      },
      child: Scaffold(
      backgroundColor: Colors.black,
      body: Shortcuts(
        shortcuts: const {
          // 激活键 → 播放/暂停。不绑方向键!方向键留给 MaterialApp 顶层默认
          // 绑定的 DirectionalFocusIntent → focusInDirection 2D 几何算法,
          // 在所有可聚焦节点间按空间最近原则跳转。我若在这里绑方向键,会覆盖
          // framework 默认行为,破坏几何导航。
          SingleActivator(LogicalKeyboardKey.select): ActivateIntent(),
          SingleActivator(LogicalKeyboardKey.enter): ActivateIntent(),
          SingleActivator(LogicalKeyboardKey.space): ActivateIntent(),
          SingleActivator(LogicalKeyboardKey.mediaPlay): ActivateIntent(),
          SingleActivator(LogicalKeyboardKey.mediaPause): ActivateIntent(),
          // escape → 控件显隐分层。**绑到自定义 _ToggleControlsIntent,不用框架 DismissIntent**
          // —— DismissIntent 会被 MaterialPageRoute 的 _DismissModalAction 抢走(它的 invoke 是
          // maybePop,会退出播放页),见 _ToggleControlsIntent 注释。有菜单时 Dialog 路由自己
          // 的 DismissIntent 关菜单,不冲突(菜单在更高层路由)。
          // browserBack/goBack(系统返回键)→ 退出页面分层,走 BACK 通道(PopScope)。
          SingleActivator(LogicalKeyboardKey.escape): const _ToggleControlsIntent(),
          SingleActivator(LogicalKeyboardKey.browserBack): const _PopPlayerIntent(),
          SingleActivator(LogicalKeyboardKey.goBack): const _PopPlayerIntent(),
        },
        child: Actions(
          actions: {
            ActivateIntent: CallbackAction<ActivateIntent>(
              onInvoke: (_) => _togglePlayPause(),
            ),
            _ToggleControlsIntent: CallbackAction<_ToggleControlsIntent>(
              onInvoke: (_) => _handleDismiss(),
            ),
            _PopPlayerIntent: CallbackAction<_PopPlayerIntent>(
              onInvoke: (_) => _handlePop(),
            ),
          },
          child: Focus(
            // autofocus 让这个 Focus 节点接管键入口,_onWakeControls 才能在
            // 控件隐藏态(子树已卸载,seek bar 的 onKeyEvent 收不到键)唤出控件。
            autofocus: true,
            // 控件可见时,这个全屏 Focus 不参与焦点遍历(canRequestFocus=false):
            // 否则它覆盖全屏(0,0)~(w,h),在 D-pad 几何导航里会被当作"正对方向
            // 的大邻居"选中,把焦点从控制行按钮吸走 → 看似丢焦。
            // 控件隐藏时设回 true:此时控制行子树已卸载,需要这个节点自己持有
            // 焦点,_onWakeControls 才能在隐藏态接到方向键唤出控件。
            canRequestFocus: !_controlsVisible,
            onKeyEvent: _onWakeControls,
            child: FocusTraversalGroup(
              child: Stack(
                children: [
                  Row(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Expanded(
                        child: _buildVideoArea(),
                      ),
                      // panel 显隐只由 _isFullscreen 决定(单一事实源)。
                      // 宽度按屏幕宽自适应:宽屏 360,窄屏(手机横屏)300。
                      //
                      // **TV 端 panel 只读**:用 ExcludeFocus 把 panel 从焦点树
                      // 移除,D-pad 焦点进不去也出不来,避免"进了 panel 回不来"的
                      // 死锁。TV 用户需要 AI 学习/附件时,在 PAD/手机端操作(panel
                      // 在非 TV 下正常可聚焦)。panel 内部仍用 FocusScope(供非 TV
                      // 用),ExcludeFocus 在 TV 下覆盖该 scope。
                      if (!_isFullscreen)
                        SizedBox(
                          width: MediaQuery.of(context).size.width >= 900 ? 360 : 300,
                          child: ExcludeFocus(
                            excluding: TvMode.instance.isActive,
                            child: _buildHelperPanel(),
                          ),
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
                      // 裸 spinner 在网络差时让 K12 学生困惑(以为卡死)。加"缓冲中…"
                      // 文字明确状态;网络慢时这比一个无声转圈更安心。
                      return Center(
                        child: Column(
                          mainAxisSize: MainAxisSize.min,
                          children: const [
                            CircularProgressIndicator(color: Colors.white70),
                            SizedBox(height: 12),
                            Text('缓冲中…',
                                style: TextStyle(
                                    color: Colors.white70, fontSize: 13)),
                          ],
                        ),
                      );
                    },
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
      ),
    );
  }

  /// 控件隐藏态的"唤出兜底"。取代旧的 _onRemoteKey(那个拦截全部方向键并转成
  /// 线性 nextFocus,破坏了 framework 的 2D 几何导航)。
  ///
  /// 这里只在 **TV + 控件隐藏** 这一种情况下拦键:任意方向键/激活键唤出控件 +
  /// 吞键(避免唤出那次按键同时触发 seek 或几何跳转)。其余情况一律 return
  /// [KeyEventResult.ignored],方向键自然流给 framework 跑几何算法。
  ///
  /// 为什么需要这个兜底:控件隐藏时焦点在这个全屏 Focus 上(见它的
  /// canRequestFocus: !_controlsVisible),控件子树虽常驻但此刻不持焦,seek bar
  /// 的 onKeyEvent 收不到键。这个顶层 Focus 是隐藏态唯一接键的位置,用来唤出控件。
  KeyEventResult _onWakeControls(FocusNode node, KeyEvent event) {
    if (event is! KeyDownEvent) return KeyEventResult.ignored;
    if (!TvMode.instance.isActive) return KeyEventResult.ignored;
    if (_controlsVisible) return KeyEventResult.ignored;
    final k = event.logicalKey;
    final isWake = k == LogicalKeyboardKey.arrowLeft ||
        k == LogicalKeyboardKey.arrowRight ||
        k == LogicalKeyboardKey.arrowUp ||
        k == LogicalKeyboardKey.arrowDown ||
        k == LogicalKeyboardKey.enter ||
        k == LogicalKeyboardKey.select ||
        k == LogicalKeyboardKey.space;
    if (isWake) {
      _toggleControls();
      return KeyEventResult.handled;
    }
    // escape/browserBack/goBack 由顶层 DismissIntent 处理,不在这里拦。
    return KeyEventResult.ignored;
  }

 /// escape 键分层(控件显隐)。菜单(Dialog)打开时,Dialog 路由自带
  /// DismissIntent 关闭,不会走到这里。ESC 与系统返回键统一走 [_applyEscLayering]
  /// 分层(见下,mumu/TV 上 ESC 常被系统当 back,两者行为需一致)。
  void _handleDismiss() {
    _applyEscLayering();
  }

  /// 系统返回键(browserBack)/ESC 分层。由 PopScope 拦截所有 pop 来源后调用。
  /// 菜单(Dialog)打开时,Dialog 路由在栈顶,系统 back 先 pop Dialog(关菜单),
  /// 不会到这里。ESC 与系统返回键统一走 [_applyEscLayering] 分层。
  void _handlePop() {
    _applyEscLayering();
  }

  /// ESC / 系统返回键的统一分层(YouTube/B站 式):
  ///   - 全屏 + 控件隐藏 → **唤出控件**(不退出):全屏沉浸态按 ESC 不该粗暴退出,
  ///     先给用户操作控件的机会。
  ///   - 全屏 + 控件可见 → **退出全屏**(回带 AI 侧边栏的非全屏态):退出全屏而非
  ///     直接退出页面,避免误触。
  ///   - 非全屏 + 控件可见 → 关控件。
  ///   - 非全屏 + 控件隐藏 → 退出页面(真正 pop)。
  /// 菜单(Dialog)打开时不走这里 —— 系统返回键先 pop Dialog 关菜单,ESC 的
  /// DismissIntent 由 Dialog 路由截获关菜单。
  void _applyEscLayering() {
    if (_isFullscreen) {
      if (!_controlsVisible) {
        // 全屏隐藏 → 唤出控件。
        _toggleControls();
        return;
      }
      // 全屏可见 → 退出全屏(控件保持可见,非全屏态控件常驻不 auto-hide)。
      setState(() {
        _isFullscreen = false;
        _controlsVisible = true;
      });
      _scheduleAutoHide();
      return;
    }
    if (_controlsVisible) {
      // 非全屏可见 → 关控件。
      setState(() => _controlsVisible = false);
      return;
    }
    // 非全屏隐藏 → 退出页面。
    Navigator.of(context).pop();
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
    // 用户主动 seek 后,同步进度上报基准点,让续播点尽快跟随用户实际位置
    // (否则定时器下次 tick 的 delta 会偏大,且 seek 期间的位置变化不计入时长)。
    _lastLoggedPosition = target.inSeconds;
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
      _brightnessChanged = true;
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

            // 4. Controls overlay (top bar + bottom bar).
            //
            // 控件层**常驻**(不卸载),用 IgnorePointer + AnimatedOpacity 控制
            // 显隐。关键:不能用 `if (_controlsVisible)` 卸载 —— 卸载会让 seek bar
            // 的 _seekBarFocus 离开焦点树,TV 几何导航丢焦。常驻则 FocusNode 永在
            // 树里,隐藏只影响视觉与触摸命中:
            //  - IgnorePointer(ignoring: !_controlsVisible):隐藏时不拦触摸,
            //    让点击穿透到 layer 3 的 GestureDetector(onTap: _toggleControls),
            //    保持触屏"点一下显/隐"的行为(等价于旧的"卸载后穿透")。
            //  - AnimatedOpacity:视觉淡入淡出,TV 下隐藏后不挡视频内容。
            Positioned.fill(
              child: IgnorePointer(
                ignoring: !_controlsVisible,
                child: AnimatedOpacity(
                  opacity: _controlsVisible ? 1.0 : 0.0,
                  duration: const Duration(milliseconds: 200),
                  child: Stack(
                    children: [
                      Positioned(
                        top: 0,
                        left: 0,
                        right: 0,
                        child: _buildTopBar(),
                      ),
                      _buildPlayerControls(),
                    ],
                  ),
                ),
              ),
            ),
          ],
        );
      },
    );
  }

  Widget _buildTopBar() {
    // 去掉了外层 Focus(focusNode: _topBarFocus, ...) 锚点 —— 方向键现在完全交给
    // framework 几何算法,顶栏的返回按钮靠 FocusButton 自带的可聚焦性参与遍历。
    // 同时砍掉了锁按钮(TV+PAD 都删,连同 _controlsLocked/_toggleLock 状态)。
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
            focusNode: _backFocus,
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
          // 顶栏 AI 按钮已移除:AI 学习的唯一入口统一到 helper panel 的 AI 卡。
          // 减少入口冗余(原本顶栏 + helper panel 两处),三端行为一致。
          // JumpRequest(从 AI 页"跳转 12:38"回来 seek)的处理在 _enterAiStudy。
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

    // seek bar 是焦点系统的"主落点":TV 进屏 autofocus 到这,◄► 做 seek ±30s
    // (唯一手写方向键语义,因为 seek 不是焦点遍历语义,framework 的几何算法
    // 帮不上)。其它方向键:
    //  - TV ▲▼ → return ignored,交给 framework 几何算法跳顶栏/控制行
    //  - 非 TV ◄► → seek ±10s(旧行为),▲▼ → 调音量(旧行为)
    // descendantsAreFocusable: TV 下 false —— Slider 自己聚焦后会用 ◄► 做单帧
    // 拖动,粒度太细不适合遥控;触屏拖动不依赖焦点,仍正常工作。
    return FocusTraversalGroup(
      child: Focus(
        focusNode: _seekBarFocus,
        autofocus: TvMode.instance.isActive,
        descendantsAreFocusable: !TvMode.instance.isActive,
        onFocusChange: (focused) => setState(() => _seekBarFocused = focused),
        onKeyEvent: (node, event) {
          final tv = TvMode.instance.isActive;
          // KeyUp(松开方向键):重置加速计数。无论是不是方向键的 KeyUp 都重置
          // 无妨 —— 只有方向键在累加 _seekRepeatCount。
          if (event is KeyUpEvent) {
            _seekRepeatCount = 0;
            return KeyEventResult.ignored;
          }
          // 同时接受 KeyDownEvent 和 KeyRepeatEvent:后者是长按时的 auto-repeat
          // 事件。只接 KeyDownEvent 会导致长按 ◄► 只 seek 一次(第一次按下),
          // 后续 repeat 被拦掉 —— 必须 || KeyRepeatEvent 才能长按连续快退/快进。
          if (event is! KeyDownEvent && event is! KeyRepeatEvent) {
            return KeyEventResult.ignored;
          }
          // 长按加速:首次按下(KeyDown)重置计数;连续 repeat 时计数递增,步长
          // 随之加大(越按越快)。基础步长 TV 30s / 非 TV 10s,每累计 3 次 repeat
          // 步长 +基础值,封顶避免一次跳太远。
          if (event is KeyDownEvent) {
            _seekRepeatCount = 0;
          }
          final isSeekKey = event.logicalKey == LogicalKeyboardKey.arrowLeft ||
              event.logicalKey == LogicalKeyboardKey.arrowRight;
          if (isSeekKey) {
            final base = tv ? 30 : 10;
            // 前 3 次按基础步长;之后每 3 次步长翻一档:base → 2base → 3base → ...
            // 封顶 4 档,避免长按一下窜到片尾。
            final tier = (_seekRepeatCount ~/ 3).clamp(0, 3);
            final step = base * (1 + tier);
            if (event is KeyRepeatEvent) _seekRepeatCount++;
            final delta = event.logicalKey == LogicalKeyboardKey.arrowLeft
                ? -step
                : step;
            return _seekAndHandle(Duration(seconds: delta));
          }
          // ▲▼ 不参与加速,走原逻辑。
          return switch (event.logicalKey) {
            LogicalKeyboardKey.arrowUp => _seekArrowUp(tv),
            LogicalKeyboardKey.arrowDown => _seekArrowDown(tv),
            _ => KeyEventResult.ignored,
          };
        },
        child: Row(
          children: [
            Text(
              _formatDuration(displayPos),
              style: TextStyle(
                color: Colors.white,
                fontWeight: FontWeight.bold,
                fontSize: 12,
                // 聚焦时时间文字加蓝色阴影,辅助识别焦点位置。
                shadows: _seekBarFocused
                    ? const [
                        Shadow(color: AppTheme.primaryColor, blurRadius: 6)
                      ]
                    : null,
              ),
            ),
            Expanded(
              child: SliderTheme(
                data: SliderTheme.of(context).copyWith(
                  // 聚焦时 track 加粗 + thumb 变大,TV 用户远距离能看出焦点。
                  trackHeight: _seekBarFocused ? 6 : 4,
                  // Custom track paints three ranges:
                  //   [0 .. position]   → played (primary)
                  //   [position .. buf] → buffered (lighter)
                  //   [buf .. end]      → unbuffered (faint)
                  trackShape: BufferedSeekBarTrackShape(
                    bufferedFraction: totalMs > 0 ? bufMs / totalMs : 0.0,
                    bufferedColor: Colors.white38,
                  ),
                  thumbShape: RoundSliderThumbShape(
                      // 非聚焦态 thumb 适度加大(7→8),K12 低龄用户手指拖动精度差,
                      // 原 7px 在手机横屏上容易戳偏。
                      enabledThumbRadius: _seekBarFocused ? 10 : 8),
                  overlayShape: RoundSliderOverlayShape(
                      overlayRadius: _seekBarFocused ? 18 : 20),
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
        ),
      ),
    );
  }

  /// seek bar onKeyEvent 的 helper:执行 seek + 返 handled。
  KeyEventResult _seekAndHandle(Duration delta) {
    _seekRelative(delta);
    return KeyEventResult.handled;
  }

  /// seek bar onKeyEvent 的 helper(非 TV):调音量 + 返 handled。
  KeyEventResult _volumeAndHandle(double delta) {
    _setVolume((_player.state.volume + delta).clamp(0.0, 100.0));
    return KeyEventResult.handled;
  }

  /// seek bar ▲ 的 helper:TV 显式跳顶栏返回按钮(避免几何算法跳错到 panel);
  /// 非 TV 调音量。
  KeyEventResult _seekArrowUp(bool tv) {
    if (tv) {
      _backFocus.requestFocus();
      return KeyEventResult.handled;
    }
    return _volumeAndHandle(5);
  }

  /// seek bar ▼ 的 helper:TV 显式跳控制行播放按钮;非 TV 调音量。
  KeyEventResult _seekArrowDown(bool tv) {
    if (tv) {
      _playPauseFocus.requestFocus();
      return KeyEventResult.handled;
    }
    return _volumeAndHandle(-5);
  }


  List<Map<String, dynamic>> _getSubtitleOptions() {
    return TrackSelectionController.subtitleOptions(
      player: _player,
      nativeSubtitleIds: _nativeSubtitleIds,
      backendSubtitles: _playInfo?.subtitles ?? const [],
    );
  }

  void _autoSelectDefaultSubtitle(List<Map<String, dynamic>> options) {
    // 优先级:backend(LLM polish 后的优质字幕) > native(容器内嵌兜底) > off。
    // 历史问题:之前 native 优先,默认就选了硬编码轨,白白丢掉了 polish 成果;
    // 而且菜单同时出现"中文"和"中文(校对版)"时,默认走 native 会让"校对版"
    // 那条点击无字幕的 bug 更明显。改成 backend 优先后两者都顺带修了。

    // 1) 若播放器已挂着某条轨,且它在选项列表里,先沿用(用户/续播已选)。
    final currentTrack = _player.state.track.subtitle;
    if (currentTrack.id != 'no') {
      final idx = options.indexWhere((opt) {
        if (opt['type'] != 'native') return false;
        final track = opt['track'] as SubtitleTrack;
        return track.id == currentTrack.id;
      });
      if (idx != -1) {
        setState(() => _selectedSubtitle = idx);
        return;
      }
    }

    // 2) 否则按优先级表挑默认项 —— 表驱动,加新来源只动这张表。
    for (final preferredType in const ['backend', 'native']) {
      final idx = options.indexWhere((opt) => opt['type'] == preferredType);
      if (idx != -1) {
        _applySubtitleOption(options[idx], idx);
        return;
      }
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
    // 去掉了外层 Focus(focusNode: _controlsRowFocus, ...) 锚点 + _activeMenu
    // 状态机。三个设置菜单改用 PlayerSettingsMenu(MenuAnchor + RadioMenuButton),
    // 焦点隔离 + 几何导航 + escape 关菜单都由 framework 保证。锁按钮已删。
    final playing = _player.state.playing;
    final audioOptions = _getAudioOptions();
    final subtitleOptions = _getSubtitleOptions();
    return Row(
      children: [
        // Play / pause
        _iconControl(
          icon: playing ? Icons.pause_rounded : Icons.play_arrow_rounded,
          focusNode: _playPauseFocus,
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
            final tv = TvMode.instance.isActive;
            return Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                // 音量图标:FocusButton,让 D-pad 可聚焦、Enter 触发静音切换。
                _iconControl(
                  icon: isMuted
                      ? Icons.volume_off_rounded
                      : Icons.volume_up_rounded,
                  onTap: _toggleMute,
                ),
                // 音量 Slider:TV 下不渲染。原因 —— Slider 聚焦后会吃掉 ◄►
                // 做单步 ±1% 调值(Flutter Slider 默认行为),焦点陷阱。
                // TV 有硬件音量键 + 上面的静音图标,够用。
                // PAD/手机保留 Slider(触屏拖动,不依赖焦点)。
                if (!tv) ...[
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
              ],
            );
          },
        ),
        const SizedBox(width: 12),
        // Playback Speed —— MenuAnchor + RadioMenuButton(单选,framework 焦点隔离)
        PlayerSettingsMenu<double>(
          icon: Icons.speed_rounded,
          selectedValue: _rate,
          // 倍速≠1.0 时图标高亮,让学生一眼看出当前在加速播放。
          active: _rate != 1.0,
          options: [0.5, 0.75, 1.0, 1.25, 1.5, 2.0]
              .map((r) => PlayerMenuOption(value: r, label: '${r}x'))
              .toList(),
          onSelected: (r) async => _setRate(r),
        ),
        const SizedBox(width: 12),
        // Subtitles —— 字幕选择 + 字幕大小(用两个独立菜单,避免 SubmenuButton
        // 在 mumu 上的样式不确定性)。subtitleOptions 自带「关闭字幕」(type='off')
        // 作为第 0 项,所以菜单直接用它,value = 列表索引,_selectedSubtitle 同语义。
        // (历史:曾手动加 PlayerMenuOption(value:0,'关闭') + 列表偏移 +1,跟
        //  subtitleOptions 自带的「关闭字幕」重复成两个关闭项。)
        PlayerSettingsMenu<int>(
          icon: Icons.subtitles_rounded,
          selectedValue: _selectedSubtitle,
          // 字幕已开启(非「关闭」)时图标高亮,一眼看出字幕状态。
          active: _selectedSubtitle != 0,
          menuTitle: '字幕选择',
          options: subtitleOptions.asMap().entries.map((entry) {
            final opt = entry.value;
            return PlayerMenuOption(
                value: entry.key, label: opt['label'] as String);
          }).toList(),
          onSelected: (idx) async {
            final opt = subtitleOptions[idx];
            await _applySubtitleOption(opt, idx);
            setState(() {});
            _scheduleAutoHide();
          },
        ),
        const SizedBox(width: 12),
        // 字幕大小档位(独立菜单):接全局 UiPrefs,四档「小/中/大/超大」。
        PlayerSettingsMenu<int>(
          icon: Icons.format_size_rounded,
          selectedValue: UiPrefs.instance.subtitleSizeIndex,
          // 字幕字号≠默认「中」(档位 1)时高亮。
          active: UiPrefs.instance.subtitleSizeIndex != 1,
          menuTitle: '字幕大小',
          options: UiPrefs.subtitleSizeLabels
              .asMap()
              .entries
              .map((entry) => PlayerMenuOption(
                  value: entry.key, label: entry.value))
              .toList(),
          onSelected: (index) async {
            await UiPrefs.instance.setSubtitleSizeIndex(index);
            setState(() {});
            _scheduleAutoHide();
          },
        ),
        const SizedBox(width: 12),
        // Audio Tracks —— 条件渲染:有多条音轨才显示。
        if (audioOptions.length > 1) ...[
          PlayerSettingsMenu<String>(
            icon: Icons.audiotrack_rounded,
            selectedValue: (_player.state.track.audio.id == 'no' ||
                    _player.state.track.audio.id == 'auto')
                ? null
                : _player.state.track.audio.id,
            // 选了非默认(非 auto/no)音轨时高亮。
            active: _player.state.track.audio.id != 'no' &&
                _player.state.track.audio.id != 'auto',
            menuTitle: '音轨选择',
            options: audioOptions.map((opt) {
              final track = opt['track'] as AudioTrack;
              return PlayerMenuOption(value: track.id, label: opt['label'] as String);
            }).toList(),
            onSelected: (id) async {
              final opt = audioOptions.firstWhere(
                (o) => (o['track'] as AudioTrack).id == id,
                orElse: () => audioOptions.first,
              );
              await _applyAudioOption(opt);
              _scheduleAutoHide();
            },
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
    FocusNode? focusNode,
  }) {
    return FocusButton(
      focusNode: focusNode,
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
    // 去掉了外层 Focus(focusNode: _helperPanelFocus, ...) 锚点。helper panel
    // 内部有 FocusButton(AI 卡 + 附件),靠 framework 几何算法自然参与遍历。
    // 跨区(视频区 ↔ panel)由顶层 FocusTraversalGroup 保证同 scope。
    return HelperPanel(
      episode: widget.episode,
      attachments: _attachments,
      loadingExtras: _loadingExtras,
      summary: _summary,
      preAdventureTasks: widget.preAdventureTasks,
      disableAiTab: widget.disableAiTab,
      tvModeActive: TvMode.instance.isActive,
      onOpenAttachment: (att) => _openAttachment(att),
      onEnterAiStudy: _enterAiStudy,
    );
  }

  /// 进入 AI 学习页的唯一入口(helper panel 的 AI 卡)。
  ///
  /// 进前暂停视频(避免后台播放含音频);返回时若用户点了 AI 页里的"跳转
  /// 12:38"链接,pop 会带回来一个 [JumpRequest],我们 seek 过去。
  /// 历史:这条逻辑原本在顶栏 AI 图标里,顶栏 AI 砍掉(统一入口)后搬到这,
  /// 顺手补上原来 helper panel 入口漏掉的 JumpRequest 处理 + pause。
  Future<void> _enterAiStudy() async {
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
    // 错误页用纯黑视频沉浸背景(与亮暗主题无关),控件文字用白色/white70,
    // 不走 context.colors —— 视频错误态始终是深色,亮暗一致。
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
              // 错误图标保留红色警示,但正文文字改浅色:原版整段红色在黑底上刺眼且
              // 错误信息可能很长(CDN 报错堆栈),红色长文对 K12 学生不友好。
              Text('播放遇到问题',
                  style: TextStyle(
                      fontSize: 18,
                      fontWeight: FontWeight.bold,
                      color: Colors.white)),
              const SizedBox(height: 8),
              Text(_errorMessage,
                  textAlign: TextAlign.center,
                  style: const TextStyle(fontSize: 13, color: Colors.white70)),
              const SizedBox(height: 32),
              // 两个动作:重试(重新初始化视频)+ 返回。重试覆盖"CDN 偶发断流/重连"
              // 这类可恢复场景,不必每次出错都强退重进。
              Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Button3D.blue(
                    onPressed: () {
                      setState(() {
                        _errorMessage = '';
                        _engineReady = false;
                      });
                      _initializeVideo();
                    },
                    child: const Text('重试'),
                  ),
                  const SizedBox(width: 16),
                  Button3D.white(
                    onPressed: () => Navigator.pop(context),
                    child: const Text('返回'),
                  ),
                ],
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

/// ESC 键的 Intent。**不能用框架的 DismissIntent** —— MaterialPageRoute 会给 push 进来的
/// route 注册一个 `DismissIntent: _DismissModalAction`(见 Flutter routes.dart:1198),它的
/// invoke 是 `Navigator.maybePop()`,会直接 pop 掉播放页。用 DismissIntent 绑 escape,焦点
/// 一旦不在本屏 Actions 子树内,ESC 就命中 ModalRoute 的 pop action → 播放页被退出。
/// 改用本屏专属 Intent,杜绝和 ModalRoute 抢 DismissIntent。见 _handleDismiss。
class _ToggleControlsIntent extends Intent {
  const _ToggleControlsIntent();
}

/// 系统返回键(browserBack/goBack)的 Intent。与 escape 分开:
/// escape 管控件显隐,返回键管退出页面(菜单内则先关菜单)。见 _handlePop。
class _PopPlayerIntent extends Intent {
  const _PopPlayerIntent();
}
