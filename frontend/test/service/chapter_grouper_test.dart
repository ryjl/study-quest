import 'package:flutter_test/flutter_test.dart';
import 'package:study_quest/model/course.dart';
import 'package:study_quest/service/chapter_grouper.dart';

Episode _ep(int id, {int chapterId = 0, int sortOrder = 1}) => Episode(
      id: id,
      courseId: 1,
      chapterId: chapterId,
      sortOrder: sortOrder,
      title: 'ep-$id',
      videoRelativePath: '',
      attachmentJson: '[]',
      fileSize: 0,
      durationSeconds: 0,
    );

Chapter _ch(int id, {String title = '', int sortOrder = 0}) =>
    Chapter(id: id, courseId: 1, title: title, sortOrder: sortOrder);

void main() {
  group('groupEpisodesByChapter', () {
    test('empty episodes + empty chapters → empty list', () {
      expect(groupEpisodesByChapter(const <Episode>[], const <Chapter>[]),
          isEmpty);
    });

    test('multiple chapters — episodes filed under matching chapter', () {
      final chapters = [
        _ch(1, title: '第一章', sortOrder: 1),
        _ch(2, title: '第二章', sortOrder: 2),
      ];
      final episodes = [
        _ep(10, chapterId: 1),
        _ep(11, chapterId: 1),
        _ep(20, chapterId: 2),
      ];
      final groups = groupEpisodesByChapter(episodes, chapters);

      expect(groups.length, 2);
      expect(groups[0].title, '第一章');
      expect(groups[0].isUngrouped, isFalse);
      expect(groups[0].episodes.map((e) => e.id), [10, 11]);
      expect(groups[1].title, '第二章');
      expect(groups[1].episodes.map((e) => e.id), [20]);
    });

    test('episodes whose chapterId points at unknown chapter → ungrouped bucket',
        () {
      final chapters = [_ch(1, title: '第一章', sortOrder: 1)];
      final episodes = [
        _ep(10, chapterId: 1),
        _ep(11, chapterId: 999), // orphan: chapter 999 doesn't exist
        _ep(12, chapterId: 0), // chapterId 0 → ungrouped
      ];
      final groups = groupEpisodesByChapter(episodes, chapters);

      expect(groups.length, 2);
      expect(groups[0].title, '第一章');
      expect(groups[1].isUngrouped, isTrue);
      expect(groups[1].title, '其他课时'); // chapters exist → "其他课时"
      expect(groups[1].episodes.map((e) => e.id), [11, 12]);
    });

    test('episodes with no chapters → "全部课时" ungrouped bucket', () {
      final episodes = [_ep(1), _ep(2), _ep(3)];
      final groups = groupEpisodesByChapter(episodes, const <Chapter>[]);

      expect(groups.length, 1);
      expect(groups[0].title, '全部课时');
      expect(groups[0].isUngrouped, isTrue);
      expect(groups[0].episodes.length, 3);
    });

    test('chapters are sorted by sortOrder then id', () {
      final chapters = [
        _ch(3, title: 'C', sortOrder: 2),
        _ch(1, title: 'A', sortOrder: 1),
        _ch(2, title: 'B', sortOrder: 1), // same sortOrder, lower id
      ];
      final episodes = [
        _ep(10, chapterId: 1),
        _ep(20, chapterId: 2),
        _ep(30, chapterId: 3),
      ];
      final groups = groupEpisodesByChapter(episodes, chapters);

      // Expected order: A(1,so=1), B(2,so=1), C(3,so=2)
      expect(groups.map((g) => g.title).toList(), ['A', 'B', 'C']);
    });

    test('empty chapter (no matching episodes) is dropped', () {
      final chapters = [
        _ch(1, title: '第一章', sortOrder: 1),
        _ch(2, title: '第二章', sortOrder: 2), // no episodes
      ];
      final episodes = [_ep(10, chapterId: 1)];
      final groups = groupEpisodesByChapter(episodes, chapters);

      expect(groups.length, 1);
      expect(groups.first.title, '第一章');
    });
  });
}
