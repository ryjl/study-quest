package app.studyquest.tv.data.remote

import com.jakewharton.retrofit2.converter.kotlinx.serialization.asConverterFactory
import app.studyquest.tv.data.local.TokenStore
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.logging.HttpLoggingInterceptor
import retrofit2.Retrofit
import java.util.concurrent.TimeUnit
import javax.inject.Singleton

/**
 * Hilt 网络模块：提供 Json / OkHttpClient / Retrofit / ApiService。
 *
 * 三件事：
 *  1. **动态 baseUrl**：Retrofit baseUrl 不能为空，构造时给占位
 *     `http://localhost:8080`；真实 baseUrl（从 TokenStore 读）在拦截器里改写
 *     请求的 host/port/scheme。这样用户在设置页改 baseUrl 后，下一次请求就生效，
 *     无需重建 Retrofit（Retrofit 实例是 @Singleton）。
 *  2. **Bearer 鉴权头**：拦截器从 TokenStore 读 token，非空时加
 *     `Authorization: Bearer <token>`。未登录请求（fetchUsers / login）不带。
 *  3. **Json**：kotlinx-serialization，`ignoreUnknownKeys = true`（后端 PascalCase /
 *     snake_case 混用 + 字段会 add-only 增长）+ `isLenient = true`。
 *
 * 网盘鉴权头（play-info 返回的 headers，含 115 网盘 Referer）**不在这里注入**——
 * 那是给 ExoPlayer 的，由播放器层的 NetdiskHttpFactory 用
 * `OkHttpDataSource.Factory.defaultRequestProperties.headers(headers)` 注入
 * （business-rules.md 第 7 节）。这里只管 API 调用的 Bearer 头。
 */
@Module
@InstallIn(SingletonComponent::class)
object NetworkModule {

    /** 占位 baseUrl（Retrofit 构造要求非空；真实请求会被拦截器改写）。 */
    private const val PLACEHOLDER_BASE_URL = "http://localhost:8080/"

    /**
     * 全局 Json 实例。网络层 + 本地缓存（TokenStore 的 current_user）共享。
     * `coerceInputValues = true` 让脏数据（如类型不匹配）落回默认值而非抛异常，
     * 进一步增强健壮性。
     */
    @Provides
    @Singleton
    fun provideJson(): Json = Json {
        ignoreUnknownKeys = true
        isLenient = true
        encodeDefaults = true
        coerceInputValues = true
    }

    /**
     * 日志拦截器（仅 debug 打印 body，对齐 PAD 端 http 调试习惯）。
     * release 构建里 HttpLoggingInterceptor 会被 R8/proguard 处理掉日志，
     * 这里 level 固定 BODY（debug 友好；release 默认 minify=false 见 build.gradle）。
     */
    @Provides
    @Singleton
    fun provideLoggingInterceptor(): HttpLoggingInterceptor =
        HttpLoggingInterceptor().apply {
            level = HttpLoggingInterceptor.Level.BODY
        }

    /**
     * 核心 OkHttpClient。
     *
     * 拦截器链顺序：
     *  1. [baseUrlInterceptor]：改写 host/scheme/port 到真实后端地址（最先，确保后续
     *     拦截器看到的 URL 已是真实地址）。
     *  2. [authInterceptor]：加 Bearer 头。
     *  3. [loggingInterceptor]：打日志（最外层，能看到最终发出的请求）。
     *
     * 超时对齐 PAD 端（默认 8s）。play-info 可能稍慢，给 10s。
     */
    @Provides
    @Singleton
    fun provideOkHttpClient(
        tokenStore: TokenStore,
        loggingInterceptor: HttpLoggingInterceptor,
    ): OkHttpClient {
        val baseUrlInterceptor = Interceptor { chain ->
            val original = chain.request()
            // runBlocking 读 SP（已套 IO dispatcher，这里再 block 一次是 OkHttp
            // 后台线程，不会卡主线程）。SP 单次读很快（毫秒级）。
            val baseUrl = runBlocking { tokenStore.getBaseUrl() }
            val newRequest = if (baseUrl.isNullOrBlank()) {
                // 未配置 baseUrl → 不改写（保持占位 localhost，这种请求本不该发，
                // UI 层在配好 baseUrl 前不应该走到业务请求）。
                original
            } else {
                val target = baseUrl.trimEnd('/')
                // 只替换 scheme://host[:port]，保留 path/query/fragment。
                // 占位 baseUrl 的 host 是 localhost:8080，path 以 /api/v1 开头。
                val newUrl = original.url.newBuilder()
                    .scheme(if (target.startsWith("https://")) "https" else "http")
                    .host(extractHost(target))
                    .also { b ->
                        extractPort(target)?.let { b.port(it) }
                    }
                    .build()
                original.newBuilder().url(newUrl).build()
            }
            chain.proceed(newRequest)
        }

        val authInterceptor = Interceptor { chain ->
            val original = chain.request()
            val token = runBlocking { tokenStore.getToken() }
            val request = if (token.isNullOrBlank()) {
                original
            } else {
                original.newBuilder()
                    .header("Authorization", "Bearer $token")
                    .build()
            }
            chain.proceed(request)
        }

        return OkHttpClient.Builder()
            .addInterceptor(baseUrlInterceptor)
            .addInterceptor(authInterceptor)
            .addInterceptor(loggingInterceptor)
            .connectTimeout(8, TimeUnit.SECONDS)
            .readTimeout(10, TimeUnit.SECONDS)
            .writeTimeout(8, TimeUnit.SECONDS)
            .build()
    }

    /**
     * Retrofit 实例。baseUrl 用占位（真实地址在拦截器里改）。kotlinx-serialization
     * converter 走 `Json.asConverterFactory("application/json".toMediaType())`。
     */
    @Provides
    @Singleton
    fun provideRetrofit(client: OkHttpClient, json: Json): Retrofit =
        Retrofit.Builder()
            .baseUrl(PLACEHOLDER_BASE_URL)
            .client(client)
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()

    /** ApiService。Retrofit.create 生成实现类。 */
    @Provides
    @Singleton
    fun provideApiService(retrofit: Retrofit): ApiService =
        retrofit.create(ApiService::class.java)

    // ── helpers：从 baseUrl 字符串解析 host / port（手写避免引入 URI 解析坑）──

    /** 从 `http://192.168.1.5:8080` 提取 `192.168.1.5`。 */
    private fun extractHost(baseUrl: String): String {
        val noScheme = baseUrl.substringAfter("://")
        val hostPort = noScheme.substringBefore('/')
        return hostPort.substringBefore(':')
    }

    /** 从 `http://192.168.1.5:8080` 提取 `8080`；无端口返回 null（保留默认 80/443）。 */
    private fun extractPort(baseUrl: String): Int? {
        val noScheme = baseUrl.substringAfter("://")
        val hostPort = noScheme.substringBefore('/')
        val colon = hostPort.indexOf(':')
        return if (colon >= 0) hostPort.substring(colon + 1).toIntOrNull() else null
    }
}
