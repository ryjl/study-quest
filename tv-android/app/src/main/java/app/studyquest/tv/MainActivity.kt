package app.studyquest.tv

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import app.studyquest.tv.ui.nav.AppNav
import app.studyquest.tv.ui.theme.StudyQuestTheme
import dagger.hilt.android.AndroidEntryPoint

/**
 * 单 Activity 入口。Hilt 注入 + Compose 接管 UI。
 *
 * 阶段 0 是空壳占位,现在接通 [StudyQuestTheme] + [AppNav] 路由表。
 * AppNav 内部按 TokenStore 登录态决定起始页(login / home)。
 */
@AndroidEntryPoint
class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            StudyQuestTheme {
                AppNav()
            }
        }
    }
}
