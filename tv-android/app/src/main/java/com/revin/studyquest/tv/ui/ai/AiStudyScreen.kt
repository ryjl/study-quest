package com.revin.studyquest.tv.ui.ai

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.tv.material3.Text
import com.revin.studyquest.tv.data.remote.dto.EpisodeSummaryDto
import com.revin.studyquest.tv.ui.theme.accentGreen
import com.revin.studyquest.tv.ui.theme.accentOrange
import com.revin.studyquest.tv.ui.theme.primaryColor
import com.revin.studyquest.tv.ui.theme.slate400
import com.revin.studyquest.tv.ui.theme.slate900

/**
 * AI 学习页 —— TV 端只读。
 *
 * 对照 PAD `ai_study_screen.dart`,但砍掉 quiz/history/输入(TV 只读):
 *   - headline(主标题)
 *   - sections(知识点小节:title + points)
 *   - keyPoints / methods / commonMistakes / concepts(列表)
 *   - takeaway(要点总结)
 *   - advice(学习建议,可能 generating 轮询中)
 *
 * 大字排版(TV 远距离可读),D-pad 垂直滚动。对照 design-tokens.md 字号 ×1.1。
 */
@Composable
fun AiStudyScreen(
    episodeId: Int,
    onBack: () -> Unit,
    viewModel: AiStudyViewModel = hiltViewModel(),
) {
    val uiState by viewModel.uiState.collectAsStateWithLifecycle()

    LaunchedEffect(episodeId) { viewModel.load(episodeId) }

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(slate900),
    ) {
        when (val s = uiState) {
            is AiUiState.Loading -> LoadingView()
            is AiUiState.SummaryLoaded -> AiContent(
                summary = s.summary,
                adviceStatus = s.advice?.status,
                adviceText = s.advice?.advice?.adviceText,
                onBack = onBack,
            )
        }
    }
}

@Composable
private fun LoadingView() {
    Box(Modifier.fillMaxSize(), Alignment.Center) {
        Text("加载 AI 学习内容...", color = primaryColor, fontSize = 20.sp)
    }
}

@Composable
private fun AiContent(
    summary: EpisodeSummaryDto?,
    adviceStatus: String?,
    adviceText: String?,
    onBack: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(48.dp),
        verticalArrangement = Arrangement.spacedBy(24.dp),
    ) {
        if (summary == null || summary.isEmpty) {
            Text(
                "本课时暂无 AI 学习总结",
                color = slate400,
                fontSize = 22.sp,
                fontWeight = FontWeight.Medium,
            )
        } else {
            // headline
            if (summary.headline.isNotEmpty()) {
                Text(
                    summary.headline,
                    color = Color.White,
                    fontSize = 32.sp,
                    fontWeight = FontWeight.Bold,
                )
            }

            // sections(知识点小节)
            summary.sections.forEach { section ->
                SectionBlock(title = section.title, points = section.points)
            }

            // 列表类内容
            BulletList(title = "关键要点", items = summary.keyPoints, color = primaryColor)
            BulletList(title = "学习方法", items = summary.methods, color = accentGreen)
            BulletList(title = "常见误区", items = summary.commonMistakes, color = accentOrange)
            BulletList(title = "核心概念", items = summary.concepts, color = primaryColor)

            // takeaway
            if (summary.takeaway.isNotEmpty()) {
                Spacer(Modifier.height(8.dp))
                Box(
                    modifier = Modifier
                        .fillMaxWidth()
                        .background(primaryColor.copy(alpha = 0.12f))
                        .padding(20.dp),
                ) {
                    Column {
                        Text("总结", color = primaryColor, fontSize = 20.sp, fontWeight = FontWeight.Bold)
                        Spacer(Modifier.height(8.dp))
                        Text(summary.takeaway, color = Color.White, fontSize = 18.sp)
                    }
                }
            }
        }

        // advice(学习建议)
        Spacer(Modifier.height(16.dp))
        AdviceBlock(status = adviceStatus, text = adviceText)
    }
}

@Composable
private fun SectionBlock(title: String, points: List<String>) {
    if (title.isBlank() && points.isEmpty()) return
    Column {
        if (title.isNotBlank()) {
            Text(title, color = primaryColor, fontSize = 24.sp, fontWeight = FontWeight.Bold)
            Spacer(Modifier.height(12.dp))
        }
        points.forEach { point ->
            Text("• $point", color = Color.White, fontSize = 18.sp)
            Spacer(Modifier.height(6.dp))
        }
    }
}

@Composable
private fun BulletList(title: String, items: List<String>, color: Color) {
    if (items.isEmpty()) return
    Column {
        Text(title, color = color, fontSize = 22.sp, fontWeight = FontWeight.Bold)
        Spacer(Modifier.height(12.dp))
        items.forEach { item ->
            Text("• $item", color = Color.White, fontSize = 18.sp)
            Spacer(Modifier.height(6.dp))
        }
    }
}

@Composable
private fun AdviceBlock(status: String?, text: String?) {
    when (status) {
        "ready" -> {
            if (!text.isNullOrEmpty()) {
                Column {
                    Text("学习建议", color = accentGreen, fontSize = 22.sp, fontWeight = FontWeight.Bold)
                    Spacer(Modifier.height(12.dp))
                    Text(text, color = Color.White, fontSize = 18.sp, lineHeight = 28.sp)
                }
            }
        }
        "generating" -> {
            Text("正在生成学习建议...", color = slate400, fontSize = 18.sp)
        }
        "cooling" -> {
            Text("建议生成暂时受阻,稍后再试", color = accentOrange, fontSize = 18.sp)
        }
        "unavailable", null -> {
            // AI 未配置,不显示建议区
        }
    }
}
