package com.revin.studyquest.tv

import android.app.Application
import dagger.hilt.android.HiltAndroidApp

/**
 * Application 入口。Hilt 在这里生成依赖注入容器。
 *
 * 后续 (阶段 1+) 这里会做:
 *   - 启动时从 [com.revin.studyquest.tv.data.local.TokenStore] 读回 token/user/baseUrl
 *     注入到网络层(对应 Flutter 端 AuthService.init + ApiService.authToken)。
 *   - TvMode 检测(TV APK 永远是 TV,这里其实恒 true,保留语义)。
 */
@HiltAndroidApp
class StudyQuestApp : Application()
