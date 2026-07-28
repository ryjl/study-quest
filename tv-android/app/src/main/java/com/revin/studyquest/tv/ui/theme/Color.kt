package com.revin.studyquest.tv.ui.theme

import androidx.compose.ui.graphics.Color

/**
 * 色板常量 — 跨端视觉 token 的 TV 端实现。
 *
 * 对照 `docs/design-tokens.md` 和 PAD 端 `frontend/lib/theme.dart` 的 `AppTheme`。
 * 品牌主色 / 渐变终点 / slate ramp 两端完全一致;TV 端只在使用取向上不同
 * (深色底 slate900,主文字 slate600)。
 *
 * 注:Color() 入参 ARGB,与 Flutter 0xFFRRGGBB 形式一致(前 2 位 FF = 不透明)。
 */

// ---- 品牌主色(两端一致)----
/** 主色 Blue-500。按钮 / 链接 / 焦点高亮 / 进度条 active。 */
val primaryColor = Color(0xFF3B82F6)

/** 次强调 Emerald-500。成功 / 完成态 / 成长积分。 */
val accentGreen = Color(0xFF10B981)

/** 强调 Orange-500。徽章 / 等级 / 警告。 */
val accentOrange = Color(0xFFF97316)

// ---- Slate 中性灰阶 ramp(两端共用 ramp)----
/** PAD 浅色底;TV 不用作底,留作 ramp 一致性。 */
val slate50 = Color(0xFFF8FAFC)

/** PAD 次背景。 */
val slate100 = Color(0xFFF1F5F9)

/** PAD 边框 borderMuted;TV 次要边框(ramp 备用)。 */
val slate200 = Color(0xFFE2E8F0)

/** TV 次要边框。 */
val slate300 = Color(0xFFCBD5E1)

/** TV 辅助文字。 */
val slate400 = Color(0xFF94A3B8)

/** PAD 静音文字 textMuted;TV 正文文字。 */
val slate500 = Color(0xFF64748B)

/** TV 主文字(深底上保证对比度)。 */
val slate600 = Color(0xFF475569)

/**
 * TV 非焦点态边框(`#334155`)。PAD 没用到,ramp 中间灰。
 * 非焦点卡片边框 / 弱化分隔。
 */
val slate700 = Color(0xFF334155)

/** PAD 主文字 textWhite。 */
val slate800 = Color(0xFF1E293B)

/** TV 深色底(核心背景)。 */
val slate900 = Color(0xFF0F172A)

// ---- 品牌延伸色 ----
/** 渐变终点、次品牌色 Indigo-500。 */
val indigo500 = Color(0xFF6366F1)

/** 头像光环、装饰 Violet-500。 */
val violet500 = Color(0xFF8B5CF6)

/** 浅蓝背景(PAD 卡片高亮;TV ramp 备用)。 */
val blue100 = Color(0xFFEFF6FF)

/** 深蓝(按下态)Blue-600。 */
val blue600 = Color(0xFF2563EB)

// ---- 语义 / 状态色 ----
/** 成功浅底。 */
val emerald100 = Color(0xFFECFDF5)

/** 警告浅底。 */
val amber50 = Color(0xFFFFFBEB)

/** 渐变中间色 Orange-400。 */
val orange400 = Color(0xFFFB923C)

/** 等级徽章渐变终点 Yellow-400。 */
val yellow400 = Color(0xFFFACC15)
