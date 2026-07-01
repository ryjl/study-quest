class UserProgress {
  final int id;
  final int userId;
  final int episodeId;
  final int lastPositionSeconds;
  final int watchSeconds;
  final bool isCompleted;

  UserProgress({
    required this.id,
    required this.userId,
    required this.episodeId,
    required this.lastPositionSeconds,
    required this.watchSeconds,
    required this.isCompleted,
  });

  factory UserProgress.fromJson(Map<String, dynamic> json) {
    return UserProgress(
      id: json['ID'] ?? json['id'] ?? 0,
      userId: json['UserID'] ?? json['user_id'] ?? 0,
      episodeId: json['EpisodeID'] ?? json['episode_id'] ?? 0,
      lastPositionSeconds: json['LastPositionSeconds'] ?? json['last_position_seconds'] ?? 0,
      watchSeconds: json['WatchSeconds'] ?? json['watch_seconds'] ?? 0,
      isCompleted: (json['IsCompleted'] ?? json['is_completed'] ?? 0) == 1,
    );
  }
}

class UserPoint {
  final int userId;
  final int currentPoints;
  final int totalEarnedPoints;

  UserPoint({
    required this.userId,
    required this.currentPoints,
    required this.totalEarnedPoints,
  });

  factory UserPoint.fromJson(Map<String, dynamic> json) {
    return UserPoint(
      userId: json['UserID'] ?? json['user_id'] ?? 0,
      currentPoints: json['CurrentPoints'] ?? json['current_points'] ?? 0,
      totalEarnedPoints: json['TotalEarnedPoints'] ?? json['total_earned_points'] ?? 0,
    );
  }
}
