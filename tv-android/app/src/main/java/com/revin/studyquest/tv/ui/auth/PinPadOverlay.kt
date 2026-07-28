package com.revin.studyquest.tv.ui.auth

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.blur
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import com.revin.studyquest.tv.data.remote.dto.UserDto
import com.revin.studyquest.tv.ui.theme.accentOrange
import com.revin.studyquest.tv.ui.theme.primaryColor
import com.revin.studyquest.tv.ui.theme.slate400

/**
 * PIN 输入蒙层 —— TV 遥控器数字键盘。
 *
 * 对照 PAD `_buildNumPad` 的功能(TV 端用 D-pad 在 3x4 网格里聚焦,不是触屏):
 *   - 3x4 网格:1-9 / 清除 / 0 / 删除
 *   - 顶部 4 个 PIN 圆点(已输入位数高亮)
 *   - 确认按钮(4 位输满可点)
 *   - 取消按钮(回选人页)
 *
 * D-pad 导航:方向键在网格内移动,Enter 激活当前键。这是 TV 输入数字的标准姿势
 * (对照腾讯/爱奇艺 TV 的密码输入)。
 *
 * 蒙层用半透明深色遮罩 + 模糊背景(对照 PAD BackdropFilter blur)。
 */
@Composable
fun PinOverlay(
    user: UserDto,
    viewModel: LoginViewModel,
    onSuccess: () -> Unit,
) {
    val pinInput by viewModel.pinInput.collectAsStateWithLifecycle()
    val pinError by viewModel.pinError.collectAsStateWithLifecycle()

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black.copy(alpha = 0.85f)),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            modifier = Modifier.padding(48.dp),
        ) {
            // 标题:验证谁的 PIN
            Text(
                text = "验证 ${user.nickname} 的 PIN 码",
                color = Color.White,
                fontSize = 24.sp,
                fontWeight = FontWeight.Bold,
            )
            Spacer(Modifier.height(32.dp))

            // PIN 圆点指示器(4 位)
            PinDots(filled = pinInput.length)
            Spacer(Modifier.height(32.dp))

            // 3x4 数字键盘
            PinGrid(
                onDigit = viewModel::onPinDigit,
                onDelete = viewModel::onPinDelete,
                onSubmit = { viewModel.submitPin(onSuccess) },
                canSubmit = pinInput.length == LoginViewModel.PIN_MAX_LENGTH,
            )
            Spacer(Modifier.height(24.dp))

            // 错误提示
            pinError?.let { err ->
                Text(err, color = Color.Red.copy(alpha = 0.9f), fontSize = 16.sp, fontWeight = FontWeight.Bold)
                Spacer(Modifier.height(16.dp))
            }

            // 取消按钮
            FocusableTextButton(text = "取消", onClick = viewModel::cancelPin)
        }
    }
}

/** 4 个 PIN 圆点,已输入的高亮(对照 PAD NumPad 的圆点指示)。 */
@Composable
private fun PinDots(filled: Int) {
    Row(horizontalArrangement = Arrangement.spacedBy(20.dp)) {
        repeat(LoginViewModel.PIN_MAX_LENGTH) { i ->
            val isFilled = i < filled
            Box(
                modifier = Modifier
                    .size(if (isFilled) 20.dp else 16.dp)
                    .background(
                        color = if (isFilled) primaryColor else slate400.copy(alpha = 0.4f),
                        shape = CircleShape,
                    )
            )
        }
    }
}

/** 3x4 数字键盘:1-9 / 清除 / 0 / 删除。 */
@Composable
private fun PinGrid(
    onDigit: (Char) -> Unit,
    onDelete: () -> Unit,
    onSubmit: () -> Unit,
    canSubmit: Boolean,
) {
    val keys = listOf(
        listOf("1", "2", "3"),
        listOf("4", "5", "6"),
        listOf("7", "8", "9"),
        listOf("清除", "0", "删除"),
    )

    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        keys.forEach { row ->
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                row.forEach { key ->
                    PinKey(
                        label = key,
                        onClick = {
                            when (key) {
                                "清除" -> repeat(LoginViewModel.PIN_MAX_LENGTH) { onDelete() }
                                "删除" -> onDelete()
                                else -> onDigit(key.first())
                            }
                        },
                    )
                }
            }
        }
        Spacer(Modifier.height(16.dp))
        // 确认按钮(居中,4 位输满才可点)
        SubmitButton(enabled = canSubmit, onSubmit = onSubmit)
    }
}

@Composable
private fun PinKey(label: String, onClick: () -> Unit) {
    var focused by remember { mutableStateOf(false) }
    val bgColor = if (focused) primaryColor else Color(0xFF1E293B)
    Box(
        modifier = Modifier
            .size(72.dp)
            .onFocusChanged { focused = it.isFocused }
            .focusable()
            .clickable(onClick = onClick)
            .background(bgColor, CircleShape),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = label,
            color = Color.White,
            fontSize = if (label.length > 1) 16.sp else 28.sp,
            fontWeight = FontWeight.Bold,
        )
    }
}

@Composable
private fun SubmitButton(enabled: Boolean, onSubmit: () -> Unit) {
    var focused by remember { mutableStateOf(false) }
    val bgColor = when {
        !enabled -> slate400.copy(alpha = 0.3f)
        focused -> accentOrange
        else -> accentOrange.copy(alpha = 0.85f)
    }
    Box(
        modifier = Modifier
            .onFocusChanged { focused = it.isFocused }
            .then(if (enabled) Modifier.focusable().clickable(onClick = onSubmit) else Modifier)
            .background(bgColor, RoundedCornerShape(12.dp)),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = "确 认",
            modifier = Modifier.padding(horizontal = 48.dp, vertical = 14.dp),
            color = Color.White,
            fontSize = 18.sp,
            fontWeight = FontWeight.Bold,
        )
    }
}
