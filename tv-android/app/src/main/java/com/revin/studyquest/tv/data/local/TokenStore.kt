package com.revin.studyquest.tv.data.local

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import javax.inject.Inject
import javax.inject.Singleton

/**
 * 持久化存储：auth token / 当前用户 / 后端 baseUrl / 字幕字号档位。
 *
 * 对照 PAD 端 `auth_service.dart` + `config.dart` + `ui_prefs.dart`（字幕字号）。
 * 用 `EncryptedSharedPreferences`（business-rules.md 第 6 节指定 TV 可用 Encrypted
 * 或 DataStore，这里用 Encrypted 与 build.gradle 已配的依赖一致）。
 *
 * 所有 SharedPreferences 的读写都套 `Dispatchers.IO`（SP 是同步磁盘 IO，主线程
 * 调用会卡 UI）。Hilt 把 EncryptedSharedPreferences 实例作为 `@Singleton` 提供，
 * 复用同一个 MasterKey。
 *
 * 注意：EncryptedSharedPreferences 的实例创建有成本（密钥派生），整个 app 共享
 * 一个实例（见下方 @Provides），不要每次 get/都新建。
 */
@Singleton
class TokenStore @Inject constructor(
    private val prefs: SharedPreferences,
    private val json: Json,
) {

    // ── auth_token（Bearer token，opaque session token）──────────────────────

    suspend fun saveToken(token: String) = withContext(Dispatchers.IO) {
        prefs.edit().putString(KEY_AUTH_TOKEN, token).apply()
    }

    suspend fun getToken(): String? = withContext(Dispatchers.IO) {
        prefs.getString(KEY_AUTH_TOKEN, null)
    }

    // ── current_user（登录用户 JSON：{id, nickname, avatar_url, role}）─────────

    suspend fun saveCurrentUser(user: StoredUser) = withContext(Dispatchers.IO) {
        prefs.edit().putString(KEY_CURRENT_USER, json.encodeToString(user)).apply()
    }

    suspend fun getCurrentUser(): StoredUser? = withContext(Dispatchers.IO) {
        val raw = prefs.getString(KEY_CURRENT_USER, null) ?: return@withContext null
        runCatching { json.decodeFromString<StoredUser>(raw) }.getOrNull()
    }

    // ── backend_base_url（后端地址；空 → 首次启动引导用户配置）──────────────

    suspend fun saveBaseUrl(url: String) = withContext(Dispatchers.IO) {
        // 去掉尾部斜杠，对齐 PAD 端 AppConfig.setBaseUrl 的清理逻辑（baseUrlRef 要求无尾斜杠）。
        val cleaned = url.trim().let { if (it.endsWith('/')) it.dropLast(1) else it }
        prefs.edit().putString(KEY_BACKEND_BASE_URL, cleaned).apply()
    }

    suspend fun getBaseUrl(): String? = withContext(Dispatchers.IO) {
        prefs.getString(KEY_BACKEND_BASE_URL, null)?.takeIf { it.isNotBlank() }
    }

    // ── ui_subtitle_size_index（字幕字号档位，见 business-rules.md 第 6 节）───

    suspend fun saveSubtitleSizeIndex(index: Int) = withContext(Dispatchers.IO) {
        prefs.edit().putInt(KEY_SUBTITLE_SIZE_INDEX, index.coerceIn(0, 3)).apply()
    }

    suspend fun getSubtitleSizeIndex(): Int = withContext(Dispatchers.IO) {
        prefs.getInt(KEY_SUBTITLE_SIZE_INDEX, DEFAULT_SUBTITLE_SIZE_INDEX)
    }

    // ── 一次性清空登录态（登出 / 401 强制下线）──────────────────────────────

    suspend fun clearAuth() = withContext(Dispatchers.IO) {
        prefs.edit()
            .remove(KEY_AUTH_TOKEN)
            .remove(KEY_CURRENT_USER)
            .apply()
        // 不清 baseUrl / 字幕字号：那是设备级配置，登出不丢。
    }

    companion object {
        // key 对齐 PAD 端：auth_service.dart / config.dart / ui_prefs.dart。
        const val KEY_AUTH_TOKEN = "auth_token"
        const val KEY_CURRENT_USER = "current_user"
        const val KEY_BACKEND_BASE_URL = "backend_base_url"
        const val KEY_SUBTITLE_SIZE_INDEX = "ui_subtitle_size_index"

        // 字幕字号默认档位 = 1（中，24.0dp），见 business-rules.md 第 6 节。
        const val DEFAULT_SUBTITLE_SIZE_INDEX = 1
    }
}

/**
 * 持久化的登录用户（current_user JSON 的形状）。
 *
 * 对照 PAD 端 auth_service.dart login 里 `prefs.setString(_keyCurrentUser, jsonEncode({
 * ID, Nickname, AvatarURL, Role }))`——存的就是这四个字段（不是整个 User 对象，
 * 避免 baseUrl 依赖耦合）。这里用 snake_case 存（DTO 已用 snake_case），重进 app
 * 反序列化幂等。
 *
 * 独立于 [com.revin.studyquest.tv.data.remote.dto.UserDto]：这是本地缓存形状，
 * 不走网络反序列化，单独定义避免把「本地存储」和「网络 DTO」耦合在一起（DTO 改
 * 字段不该影响已落盘的缓存格式）。
 */
@kotlinx.serialization.Serializable
data class StoredUser(
    val id: Int,
    val nickname: String,
    val avatarUrl: String,
    val role: String,
)

/**
 * Hilt module：提供 EncryptedSharedPreferences 单例。
 *
 * Json 实例由 NetworkModule 统一提供（网络层和本地缓存共享同一套宽容配置），
 * 这里不再重复 provide，避免 Hilt 重复绑定报错。TokenStore 通过构造注入拿到 Json。
 *
 * 注意：EncryptedSharedPreferences 在某些 OEM 旧设备上可能抛 KeyStore 异常，
 * 这里 fallback 到普通 SharedPreferences（降级：明文存，至少不崩）。token 明文
 * 存在本地不算严重（设备已 root 才能读），但优先尝试加密。
 */
@Module
@InstallIn(SingletonComponent::class)
object LocalStorageModule {

    @Provides
    @Singleton
    fun provideEncryptedSharedPreferences(
        @ApplicationContext context: Context,
    ): SharedPreferences {
        return runCatching {
            val masterKey = MasterKey.Builder(context)
                .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
                .build()
            EncryptedSharedPreferences.create(
                context,
                "studyquest_tv_secure_prefs",
                masterKey,
                EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
                EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
            )
        }.getOrElse {
            // Fallback：加密存储初始化失败（旧设备 KeyStore 损坏）→ 明文存储。
            // 不崩是第一要务；token 明文风险可接受（参考 PAD 端就是明文 SharedPreferences）。
            context.getSharedPreferences("studyquest_tv_prefs", Context.MODE_PRIVATE)
        }
    }
}
