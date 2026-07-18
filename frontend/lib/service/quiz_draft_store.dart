import 'dart:convert';
import 'package:shared_preferences/shared_preferences.dart';

// quiz_draft_store.dart — Phase B 本地缓存未提交答案。
//
// 做题改成"统一提交"后,学生在点"提交全部"之前的选择/填空都只活在内存里。一旦
// APP 被切后台杀掉、或误触返回,做了一半的卷子就没了。这里用 shared_preferences 按
// (userID, episodeID) 存草稿,下次打开 AI 学习页时若该 quiz 尚未交卷,自动恢复填入。
//
// 草稿格式(与后端无关,纯前端约定):
//   {
//     "choice_picks": { "<qid>": <optionIndex> },
//     "fill_texts":   { "<qid>": "<text>" },
//     "multi_picks":  { "<qid>": [<idx>, ...] }
//   }
//
// 生命周期:
//   - 用户改答案 → saveDraft(覆盖写,廉价,SharedPreferences 本地 IO 可接受)
//   - 提交全部成功 → clearDraft(交卷了,草稿没用了)
//   - 换题(regen)成功 → clearDraft(新卷子,旧草稿作废)
//   - 打开页面 quiz 未交卷且有草稿 → loadDraft 恢复
//
// 为什么按 (userID, episodeID):多用户共用一台 PAD,每人每节课的草稿要隔离。
// key 形如 "quiz_draft.<uid>.<eid>",方便定位与清理。

/// 未提交的做题草稿:选择题的已选项 + 填空题的已填文本 + 多选题的已选项集合。
class QuizDraft {
  /// questionId(字符串形式)→ 选中的选项索引(0-based)。
  final Map<String, int> choicePicks;
  /// questionId(字符串形式)→ 填写的文本。
  final Map<String, String> fillTexts;
  /// questionId(字符串形式)→ 多选已选项索引列表(无序)。multi_choice 专用。
  final Map<String, List<int>> multiPicks;

  const QuizDraft({
    this.choicePicks = const <String, int>{},
    this.fillTexts = const <String, String>{},
    this.multiPicks = const <String, List<int>>{},
  });

  bool get isEmpty => choicePicks.isEmpty && fillTexts.isEmpty && multiPicks.isEmpty;

  factory QuizDraft.fromJson(Map<String, dynamic> j) {
    final rawPicks = j['choice_picks'];
    final Map<String, int> picks = {};
    if (rawPicks is Map) {
      rawPicks.forEach((k, v) {
        if (v is num) picks[k.toString()] = v.toInt();
      });
    }
    final rawTexts = j['fill_texts'];
    final Map<String, String> texts = {};
    if (rawTexts is Map) {
      rawTexts.forEach((k, v) {
        texts[k.toString()] = v.toString();
      });
    }
    // multi_picks:JSON 里值是 List<dynamic>(从 prefs 反序列化),这里 cast 回 List<int>。
    // 脏数据(单个 num 而非 list、空 list)跳过;空 list 视为"清空了"不存,保持 map 紧凑。
    final rawMulti = j['multi_picks'];
    final Map<String, List<int>> multi = {};
    if (rawMulti is Map) {
      rawMulti.forEach((k, v) {
        if (v is List) {
          final indices = v.whereType<num>().map((e) => e.toInt()).toList();
          if (indices.isNotEmpty) multi[k.toString()] = indices;
        }
      });
    }
    return QuizDraft(choicePicks: picks, fillTexts: texts, multiPicks: multi);
  }

  Map<String, dynamic> toJson() => {
        'choice_picks': choicePicks,
        'fill_texts': fillTexts,
        'multi_picks': multiPicks,
      };

  static const empty = QuizDraft();
}

/// 草稿的本地持久化。所有方法都是 async(shared_preferences 的 IO 是异步的)。
class QuizDraftStore {
  static const _prefix = 'quiz_draft.';

  static String _key(int userId, int episodeId) =>
      '$_prefix$userId.$episodeId';

  /// 保存草稿(覆盖写)。任一 map 为空也会写入(表示用户清空了某题的答案)。
  /// multiPicks 存的是 questionId → 选中的索引集合(Set 转 List 落盘)。
  static Future<void> saveDraft(
    int userId,
    int episodeId,
    Map<int, int> choicePicks,
    Map<int, String> fillTexts,
    Map<int, Set<int>> multiPicks,
  ) async {
    final prefs = await SharedPreferences.getInstance();
    // 把 int key 转成 string(JSON 的 key 必须是 string)。
    final picks = <String, int>{};
    choicePicks.forEach((qid, idx) => picks[qid.toString()] = idx);
    final texts = <String, String>{};
    fillTexts.forEach((qid, t) => texts[qid.toString()] = t);
    final multi = <String, List<int>>{};
    multiPicks.forEach((qid, set) {
      if (set.isNotEmpty) multi[qid.toString()] = set.toList();
    });
    final draft = QuizDraft(choicePicks: picks, fillTexts: texts, multiPicks: multi);
    await prefs.setString(_key(userId, episodeId), jsonEncode(draft.toJson()));
  }

  /// 读取草稿;没有时返回空 draft(不抛异常)。调用方在 quiz 未交卷时据此恢复。
  static Future<QuizDraft> loadDraft(int userId, int episodeId) async {
    final prefs = await SharedPreferences.getInstance();
    final raw = prefs.getString(_key(userId, episodeId));
    if (raw == null || raw.isEmpty) return QuizDraft.empty;
    try {
      final decoded = jsonDecode(raw);
      if (decoded is Map<String, dynamic>) {
        return QuizDraft.fromJson(decoded);
      }
    } catch (_) {
      // 脏数据(老格式/写入中断),忽略:宁可重新做题也不要把垃圾恢复进去。
    }
    return QuizDraft.empty;
  }

  /// 清除草稿。提交成功 / 换题 / quiz 已交卷时调用。
  static Future<void> clearDraft(int userId, int episodeId) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_key(userId, episodeId));
  }
}
