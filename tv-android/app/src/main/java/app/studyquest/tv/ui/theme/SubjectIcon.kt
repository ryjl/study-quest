package app.studyquest.tv.ui.theme

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.MenuBook
import androidx.compose.material.icons.filled.AutoStories
import androidx.compose.material.icons.filled.Balance
import androidx.compose.material.icons.filled.Biotech
import androidx.compose.material.icons.filled.Book
import androidx.compose.material.icons.filled.Calculate
import androidx.compose.material.icons.filled.Explore
import androidx.compose.material.icons.filled.Extension
import androidx.compose.material.icons.filled.LiveTv
import androidx.compose.material.icons.filled.Map
import androidx.compose.material.icons.filled.Movie
import androidx.compose.material.icons.filled.MovieCreation
import androidx.compose.material.icons.filled.MovieFilter
import androidx.compose.material.icons.filled.MusicNote
import androidx.compose.material.icons.filled.Palette
import androidx.compose.material.icons.filled.Park
import androidx.compose.material.icons.filled.Public
import androidx.compose.material.icons.filled.Science
import androidx.compose.material.icons.filled.Sports
import androidx.compose.material.icons.filled.Translate
import androidx.compose.material.icons.filled.VideoCameraBack
import androidx.compose.ui.graphics.vector.ImageVector

/**
 * 学科 key → 图标映射(对照 PAD `frontend/lib/ui/widget/subject_icon.dart` 的
 * `subjectIconData`)。
 *
 * 学科是 DB-driven(admin 可建自定义学科),这里映射系统预设的 key(见 backend
 * `service/subject_service.go` seed list)到语义接近的 Material 图标,自定义或
 * 未识别的 fallback 到 [Icons.Filled.Book]。调用方用学科的 `color`(DB 配置)给图标着色。
 *
 * **两端对齐契约**:PAD `subjectIconData` 和这里的 key→icon 映射必须一致(对照
 * admin SPA `subjectIcon.tsx` 是同一份映射的第三端拷贝)。改任一端 → 改另两端。
 * 接受 key 大小写不敏感(label "语文" 或别名 "chinese" 都能匹配),对照 PAD 行为。
 *
 * TV 用 `Icons.Filled.*`(对照 PAD 的 `Icons.*_rounded` —— Compose 的 Filled 系列
 * 对应 Material outlined/filled,TV 深底上 Filled 比 Outlined 更醒目)。
 */
fun subjectIconData(key: String): ImageVector = when (key.lowercase()) {
    "math", "数学" -> Icons.Filled.Calculate
    "chinese", "语文" -> Icons.AutoMirrored.Filled.MenuBook
    "english", "英语" -> Icons.Filled.Translate
    "physics", "物理", "科学" -> Icons.Filled.Science
    "chemistry", "化学" -> Icons.Filled.Biotech
    "biology", "生物" -> Icons.Filled.Park
    "history", "历史" -> Icons.Filled.AutoStories
    "geography", "地理" -> Icons.Filled.Public
    "politics", "道法", "道德与法治" -> Icons.Filled.Balance
    "extra", "课外", "百科" -> Icons.Filled.Explore
    "entertainment", "娱乐" -> Icons.Filled.Movie
    // 2026-07-20 新增娱乐子类(配合 Subject.Category=entertainment)。
    "animation", "动画", "动画片" -> Icons.Filled.MovieFilter
    "movie", "电影" -> Icons.Filled.MovieCreation
    "documentary", "纪录片" -> Icons.Filled.VideoCameraBack
    "variety", "综艺" -> Icons.Filled.LiveTv
    "music", "音乐" -> Icons.Filled.MusicNote
    "art", "美术" -> Icons.Filled.Palette
    "pe", "sport", "体育" -> Icons.Filled.Sports
    "兴趣" -> Icons.Filled.Extension
    "综合" -> Icons.Filled.Map
    else -> Icons.Filled.Book
}
