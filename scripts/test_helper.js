#!/usr/bin/env node
// scripts/test_helper.js —— Flutter app 自动化测试助手(零依赖,只用 Node 内置)
//
// 原理:`adb shell uiautomator dump` 读 Flutter 暴露的 Semantics 无障碍树,
// 拿到中文文本节点 + 坐标 + 可点击/聚焦属性 → 定位元素、点击、发键、断言。
// 替代手动 mumu 测试,尤其用于播放器 ESC/返回键分层这类「按键后看屏幕状态」
// 的回归。app 端零改动 —— Flutter 默认就暴露 Semantics。
//
// 用法:
//   1) 当库:`const t = require('./scripts/test_helper'); t.tap('进入学习'); t.key(111);`
//   2) 自检:`node scripts/test_helper.js` → 连设备 + dump 当前屏 + 打印可见文本
//
// 设备:mumu 模拟器(WSL 网关 → Windows 宿主)。地址用 env ADB_SER 覆盖:
//   ADB_SER=127.0.0.1:7555 node scripts/test_helper.js
//
// 注意点(踩过的坑,别重复):
//   - mumu 上 ESC(111)/BACK(4) 不进 Flutter KeyEvent 系统,走 Android 系统 back
//     通道。所以这俩键的「是否退出页面」只能靠 screenText() 看 UI 变化判断。
//   - app 有两个 package:com.revin.study_quest(主 app,播放器在这)和
//     com.revinstudyquest.tv.debug(TV debug)。启动脚本里 currentApp() 会确认。
//   - dump 偶尔报 "could not get idle state"(动画中)—— 是警告不影响,重试即可。
'use strict';

const { execSync } = require('child_process');
const fs = require('fs');

// mumu 默认走 WSL 网关地址;真机 TV / 其他模拟器用 env 覆盖。
const ADB_SER = process.env.ADB_SER || '172.24.240.1:16384';
const MAIN_PKG = 'com.revin.study_quest';
const MAIN_ACTIVITY = 'com.revin.study_quest.MainActivity';
// dump 临时文件:/sdcard 上设备读,再 pull 到本地 /tmp 解析。
const REMOTE_XML = '/sdcard/ui_test_helper.xml';
const LOCAL_XML = '/tmp/ui_test_helper.xml';

// ─── 底层 adb ─────────────────────────────────────────────────────────────
// 所有 adb 调用都 -s ${ADB_SER} 锁定设备,避免多设备时误发。
function sh(cmd, { allowFail = false } = {}) {
  try {
    return execSync(`adb -s ${ADB_SER} ${cmd}`, {
      encoding: 'utf8',
      stdio: ['pipe', 'pipe', 'pipe'],
    });
  } catch (e) {
    if (allowFail) return '';
    // adb 命令失败时把 stderr 一起抛出,排查才看得见真因。
    const stderr = (e.stderr || '').trim();
    throw new Error(`adb ${cmd} failed: ${e.message}${stderr ? '\nstderr: ' + stderr : ''}`);
  }
}

/// 确保设备已连接。掉线时先 kill-server 再 connect。
/// 建议脚本开头调一次。
function ensureConnected() {
  const devices = sh('devices', { allowFail: true });
  if (devices.includes(`${ADB_SER}\tdevice`)) return true;
  // 没连上:重启 adb server + 重连。
  execSync('adb kill-server', { encoding: 'utf8', stdio: 'ignore' });
  execSync('adb start-server', { encoding: 'utf8', stdio: 'ignore' });
  sh(`connect ${ADB_SER}`);
  const again = sh('devices', { allowFail: true });
  return again.includes(`${ADB_SER}\tdevice`);
}

/// 当前前台 app 的 ActivityRecord 名。用于确认测的是主 app 而非 TV debug 包。
/// 解析 `dumpsys activity activities` 里的 topResumedActivity=ActivityRecord{... <pkg>/...}。
function currentApp() {
  const out = sh('shell dumpsys activity activities', { allowFail: true });
  const m = /topResumedActivity=ActivityRecord\{[^}]* (\S+) /.exec(out)
    || /ResumedActivity.*?ActivityRecord\{[^}]* (\S+) /.exec(out);
  return m ? m[1] : '?';
}

/// 强制重启主 app(先 force-stop 再 am start —— 单 am start 只会切前台不重启进程)。
/// `am start -n` 要 package/package.Activity 的完整组件名(斜杠分隔),不是裸类名。
function restartApp() {
  sh(`shell am force-stop ${MAIN_PKG}`);
  return sh(`shell am start -n ${MAIN_PKG}/${MAIN_ACTIVITY}`);
}

// ─── Semantics 树读取 ─────────────────────────────────────────────────────

/// dump 当前无障碍树,返回节点数组。每个节点是 attrs map(bounds→坐标、text、
/// content-desc、clickable、focused...),外加 _cx/_cy 中心点坐标(已落库方便 tap)。
///
/// **播放态的坑(实测)**:视频持续渲染时窗口永远不 idle,uiautomator dump 报
/// `ERROR: could not get idle state.` 且**不写文件** —— 不是偶发,是视频一播就持续失败。
/// 重试也没用。两个后果必须防:
///   1) 不能 `2>/dev/null` 吞掉错误 —— 否则 pull 会拉到上一次成功的旧 xml,给出错误判据。
///   2) 测试播放器时,先暂停视频(keyevent 62 space,播放器绑到 play/pause)再 dump。
/// 本函数:显式解析 dump 输出判断成功,失败重试 [retries] 次,仍失败则抛错(不返回脏数据)。
/// 调用方在播放页应先用 pauseForDump() 暂停,保证全程可读。
function dumpNodes({ retries = 2, retryDelayMs = 300 } = {}) {
  let lastErr = '';
  for (let attempt = 0; attempt <= retries; attempt++) {
    // 不吞 stderr —— 要能判断 dump 是真的写出文件了,还是 idle 失败啥都没写。
    const out = sh('shell uiautomator dump ' + REMOTE_XML, { allowFail: true });
    // 成功输出含 "UI hierchary dumped to:"(注意是 "hierchary" 而非 "hierarchy",uiautomator 的拼写)
    if (/dumped to/i.test(out)) {
      execSync(`adb -s ${ADB_SER} pull ${REMOTE_XML} ${LOCAL_XML}`, {
        encoding: 'utf8',
        stdio: 'ignore',
      });
      return _parseNodesXml(fs.readFileSync(LOCAL_XML, 'utf8'));
    }
    lastErr = (out || '').trim().split('\n').pop();
    if (attempt < retries) {
      // 同步 sleep:execSync 包一个 sleep。retry 间给窗口一点喘息。
      execSync(`sleep ${retryDelayMs / 1000}`);
    }
  }
  throw new Error(
    `uiautomator dump 失败(${retries + 1} 次):${lastErr || '未知'}。` +
      `视频播放中窗口不 idle —— 播放页测试请先暂停视频(pauseForDump / keyevent 62)。`,
  );
}

/// 把 uiautomator dump 的 XML 解析成节点数组(供 dumpNodes 复用)。
/// uiautomator dump 的 <node .../> 自闭合,属性顺序不固定 → 用宽松正则逐节点抓。
function _parseNodesXml(xml) {
  const nodes = [];
  const re = /<node\s+([^/]*?)\/?>/g;
  let m;
  while ((m = re.exec(xml)) !== null) {
    const attrs = {};
    let am;
    // 属性名含连字符(content-desc / resource-id / long-clickable 等),\w 不匹配 `-`,
    // 必须用 [\w-]+。否则 content-desc 整个属性被吞掉,find() 永远找不到文字 ——
    // Flutter 的 uiautomator dump 把文字标签放在 content-desc 而非 text,这是致命的。
    const attrRe = /([\w-]+)="([^"]*)"/g;
    while ((am = attrRe.exec(m[1])) !== null) attrs[am[1]] = am[2];
    if (attrs.bounds) {
      const b = /\[(\d+),(\d+)\]\[(\d+),(\d+)\]/.exec(attrs.bounds);
      if (b) {
        attrs._cx = (+b[1] + +b[3]) / 2;
        attrs._cy = (+b[2] + +b[4]) / 2;
      }
    }
    nodes.push(attrs);
  }
  return nodes;
}

/// 找含某文本的节点(text 或 content-desc)。返回数组(可能多个)。
/// uiautomator 里文字可能在 content-desc 而非 text(Semantics 转换时常见),两者都查。
function find(text) {
  return dumpNodes().filter(
    (n) => (n.text || '').includes(text) || (n['content-desc'] || '').includes(text),
  );
}

/// 当前屏幕所有可见文本(去重)。快速判断「在哪个屏」—— 播放页会有「字幕/进度/播放」,
/// 课程列表会有「进入学习」之类的课程名。
/// 注意:Flutter 的 uiautomator dump 把文字标签放在 **content-desc** 而非 text,
/// 只读 n.text 会拿到空数组。两者都收集。
function screenText() {
  const all = dumpNodes().flatMap((n) => [n.text, n['content-desc']]);
  return [...new Set(all.filter((t) => t && t.trim()))];
}

// ─── 操作 ─────────────────────────────────────────────────────────────────

/// 点击含某文本的节点(点其中心)。找不到返回 false(不抛 —— 调用方可能想断言失败)。
function tap(text) {
  const n = find(text)[0];
  if (!n || n._cx == null) {
    console.log(`❌ 找不到: ${text}`);
    return false;
  }
  sh(`shell input tap ${Math.round(n._cx)} ${Math.round(n._cy)}`);
  return true;
}

/// 发按键。常用:4=BACK, 111=ESC, 19/20/21/22=↑↓←→, 23=SELECT/ENTER, 24/25=音量±。
function key(keycode) {
  return sh(`shell input keyevent ${keycode}`);
}

/// 暂停播放器视频,让 uiautomator dump 能拿到 window idle。
/// 视频持续渲染时 dump 持续报 "could not get idle state"(见 dumpNodes 注释)。
/// 播放器把 space 绑到 play/pause(Shortcuts: space→ActivateIntent→_togglePlayPause),
/// 所以发 KEYCODE_SPACE(62)切暂停。幂等:已暂停时再发会变播放 —— 因此本函数先 dump
/// 探一次,只在 dump 失败(idle 问题)时才发暂停,避免误把已暂停的视频弄成播放。
/// 返回 true 表示确实暂停了。
function pauseForDump() {
  // 先探一次:能 dump 说明画面静态(可能本来就暂停),无需动。
  try {
    dumpNodes({ retries: 0 });
    return false;
  } catch (_) {
    // dump 失败 → 视频在播,发 space 暂停。
    sh('shell input keyevent 62');
    // 等画面真正静止(uiautomator 还要等 window idle)。
    execSync('sleep 1');
    return true;
  }
}

// ─── 轮询 / 断言 ──────────────────────────────────────────────────────────

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/// 等某文本出现(轮询 dump)。超时抛错 —— 默认 8s 足够大多数界面切换。
async function waitFor(text, timeoutMs = 8000, { interval = 400 } = {}) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (find(text).length > 0) return true;
    await sleep(interval);
  }
  throw new Error(`waitFor 超时(${timeoutMs}ms):"${text}" 没出现。当前屏:${screenText().join(' | ')}`);
}

/// 等某文本消失。用于「关菜单后菜单项消失」「退出后播放页文本消失」。
async function waitForGone(text, timeoutMs = 8000, { interval = 400 } = {}) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (find(text).length === 0) return true;
    await sleep(interval);
  }
  throw new Error(`waitForGone 超时(${timeoutMs}ms):"${text}" 仍在。当前屏:${screenText().join(' | ')}`);
}

/// 断言文本可见。失败抛带当前屏快照的错。
function assertVisible(text) {
  if (find(text).length === 0) {
    throw new Error(`assertVisible 失败:"${text}" 不可见。当前屏:${screenText().join(' | ')}`);
  }
  return true;
}

/// 断言文本不可见。
function assertNotVisible(text) {
  if (find(text).length > 0) {
    throw new Error(`assertNotVisible 失败:"${text}" 仍可见。当前屏:${screenText().join(' | ')}`);
  }
  return true;
}

/// 当前焦点节点(uiautomator 的 focused="true")。判断 D-pad 焦点在哪。
/// 找不到返回 null(可能是无障碍树没暴露焦点,或动画中)。
function focusedNode() {
  return dumpNodes().find((n) => n.focused === 'true') || null;
}

/// 截图存证(辅助 debug)。默认存 /tmp。
function screenshot(path = '/tmp/ui_shot.png') {
  sh(`shell screencap -p /sdcard/ui_shot.png`);
  execSync(`adb -s ${ADB_SER} pull /sdcard/ui_shot.png ${path}`, {
    encoding: 'utf8',
    stdio: 'ignore',
  });
  return path;
}

// ─── 导出 ─────────────────────────────────────────────────────────────────
module.exports = {
  // 常量(给调用方判断用)
  ADB_SER, MAIN_PKG, MAIN_ACTIVITY,
  KEY: { BACK: 4, ESC: 111, UP: 19, DOWN: 20, LEFT: 21, RIGHT: 22, ENTER: 23, SELECT: 23 },
  // 连接 / app
  ensureConnected, currentApp, restartApp,
  // 读屏
  dumpNodes, find, screenText, focusedNode,
  // 操作
  tap, key, screenshot, pauseForDump,
  // 轮询 / 断言
  waitFor, waitForGone, assertVisible, assertNotVisible,
  sleep,
};

// ─── 直接 `node test_helper.js` 时跑 smoke 自检 ────────────────────────────
if (require.main === module) {
  console.log(`==> 连接设备 ${ADB_SER}`);
  if (!ensureConnected()) {
    console.error(`❌ 连不上 ${ADB_SER}。检查 mumu 是否开着、地址是否变了(env ADB_SER 覆盖)。`);
    process.exit(1);
  }
  console.log('==> 当前前台 app:', currentApp());
  console.log('==> dump 当前屏,可见文本:');
  try {
    const texts = screenText();
    if (texts.length === 0) console.log('   (空 —— 可能在全屏视频页/无文字界面,或动画中重试)');
    texts.forEach((t) => console.log('   •', t));
    const f = focusedNode();
    if (f) console.log('==> 焦点节点:', f.text || f['content-desc'] || '(无文字)');
  } catch (e) {
    console.error('❌ dump 失败:', e.message);
    process.exit(1);
  }
}
