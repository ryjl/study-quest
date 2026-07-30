import 'package:flutter_test/flutter_test.dart';
import 'package:study_quest/model/user.dart';

void main() {
  group('User.fromJson grade parsing', () {
    test('parses Grade (PascalCase, backend DTO + cached form)', () {
      final u = User.fromJson({
        'ID': 1,
        'Nickname': 'a',
        'AvatarURL': '',
        'Role': 'student',
        'Grade': '四年级',
      });
      expect(u.grade, '四年级');
    });

    test('parses grade (snake_case, client-facing API)', () {
      final u = User.fromJson({
        'id': 2,
        'nickname': 'b',
        'avatar_url': '',
        'role': 'student',
        'grade': '初二',
      });
      expect(u.grade, '初二');
    });

    test('tolerates missing grade → empty string (old backend / legacy cache)', () {
      final u = User.fromJson({
        'ID': 3,
        'Nickname': 'c',
        'AvatarURL': '',
        'Role': 'student',
      });
      expect(u.grade, '');
    });

    test('defaults grade to empty in the constructor', () {
      final u = User(id: 4, nickname: 'd', avatarUrl: '', role: 'student');
      expect(u.grade, '');
    });
  });

  group('gradeOrRoleLabel', () {
    test('prefers grade when non-empty', () {
      final u = User(id: 1, nickname: 'x', avatarUrl: '', role: 'student', grade: '四年级');
      expect(gradeOrRoleLabel(u), '四年级');
    });

    test('falls back to role label when grade is empty', () {
      final student = User(id: 2, nickname: 'x', avatarUrl: '', role: 'student');
      final admin = User(id: 3, nickname: 'y', avatarUrl: '', role: 'admin');
      expect(gradeOrRoleLabel(student), '学生');
      expect(gradeOrRoleLabel(admin), '管理员');
    });

    test('unknown role falls back to 学生', () {
      final u = User(id: 4, nickname: 'z', avatarUrl: '', role: 'weird');
      expect(gradeOrRoleLabel(u), '学生');
    });
  });
}
