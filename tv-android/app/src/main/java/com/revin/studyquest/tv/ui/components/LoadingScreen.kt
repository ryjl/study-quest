package com.revin.studyquest.tv.ui.components

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material.CircularProgressIndicator
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.revin.studyquest.tv.ui.theme.primaryColor
import com.revin.studyquest.tv.ui.theme.slate400

/**
 * TV 加载页 — TV 大屏居中,品牌色转圈。
 *
 * 用于网络请求 / 初始化等异步态。居中 [CircularProgressIndicator] + 可选文案。
 * 用 [androidx.compose.material3.CircularProgressIndicator](compose 基础组件,
 * TV 没专属替代),颜色用 [primaryColor] 品牌蓝。
 *
 * @param message 可选加载文案(如"加载课程中…")。
 * @param modifier 外部 modifier,默认占满父。
 */
@Composable
fun LoadingScreen(
    modifier: Modifier = Modifier,
    message: String? = null,
) {
    Box(
        modifier = modifier.fillMaxSize(),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(16.dp),
            modifier = Modifier.padding(24.dp),
        ) {
            CircularProgressIndicator(
                modifier = Modifier.size(56.dp),
                color = primaryColor,
                strokeWidth = 4.dp,
            )
            if (!message.isNullOrBlank()) {
                Text(
                    text = message,
                    color = slate400,
                    style = MaterialTheme.typography.bodyMedium,
                )
            }
        }
    }
}
