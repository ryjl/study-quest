package app.studyquest.tv.ui.components

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import app.studyquest.tv.ui.theme.BorderRadiusValue
import app.studyquest.tv.ui.theme.accentOrange
import app.studyquest.tv.ui.theme.slate400
import app.studyquest.tv.ui.theme.slate50

/**
 * TV 错误页 — 居中错误提示 + 重试按钮。
 *
 * 用于网络失败 / 空数据等错误态。居中显示错误图标(用 [TvIconButton] 的刷新箭头)+
 * 错误文案 + 重试按钮。重试按钮复用 [TvIconButton] 的焦点发光视觉,保证交互一致。
 *
 * @param message 错误文案。
 * @param onRetry 重试回调(点重试按钮触发)。为 null 时不显示重试按钮。
 * @param retryLabel 重试按钮文案(非图标场景可换 Text 按钮)。当前用图标按钮,此值仅作 a11y。
 * @param modifier 外部 modifier,默认占满父。
 */
@Composable
fun ErrorScreen(
    message: String,
    modifier: Modifier = Modifier,
    onRetry: (() -> Unit)? = null,
    retryLabel: String = "重试",
) {
    Box(
        modifier = modifier.fillMaxSize(),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(20.dp),
            modifier = Modifier
                .wrapContentHeight()
                .padding(24.dp),
        ) {
            Text(
                text = message,
                color = slate50,
                style = MaterialTheme.typography.titleLarge,
                textAlign = TextAlign.Center,
            )
            Text(
                text = "请检查网络后重试",
                color = slate400,
                style = MaterialTheme.typography.bodyMedium,
                textAlign = TextAlign.Center,
            )
            if (onRetry != null) {
                TvIconButton(
                    icon = Icons.Filled.Refresh,
                    onClick = onRetry,
                    size = 56.dp,
                    shape = RoundedCornerShape(BorderRadiusValue),
                    backgroundColor = accentOrange.copy(alpha = 0.15f),
                )
            }
        }
    }
}
