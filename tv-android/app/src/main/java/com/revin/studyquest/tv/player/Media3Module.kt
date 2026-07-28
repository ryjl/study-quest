package com.revin.studyquest.tv.player

import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import okhttp3.OkHttpClient
import okhttp3.logging.HttpLoggingInterceptor
import java.util.concurrent.TimeUnit
import javax.inject.Qualifier
import javax.inject.Singleton

/**
 * Hilt module —— 提供播放器专用的 OkHttp + [NetdiskHttpFactory]。
 *
 * 为什么单独给 media3 一个 OkHttpClient(不复用 NetworkModule 的 API 客户端):
 *   1. 超时不同:API 调用 8s 足够;但流媒体拉流慢(尤其网盘 CDN 冷启动),
 *      连接/读取要更宽松(30s),否则首帧前就超时报错。
 *   2. 关注点分离:API 客户端有 Bearer 拦截器 / baseUrl 改写拦截器,
 *      这些对 media3 拉流毫无意义(流 URL 是绝对地址),反而可能干扰。
 *   3. 缓存策略不同:API 不缓存;media3 后续可能加大缓冲(LoadControl)。
 *
 * 对照 PAD 端:media_kit 的 mpv 配置 `cache-secs=60`(1 分钟前向缓存)。
 * ExoPlayer 侧用 DefaultLoadControl 配 buffer(在 PlayerScreen 里建 ExoPlayer 时设)。
 */
@Module
@InstallIn(SingletonComponent::class)
object Media3Module {

    /**
     * 播放器专用的 OkHttpClient(更长超时,无 API 拦截器)。
     *
     * @Qualifier [Media3Client] 标注,避免和 NetworkModule 的 OkHttpClient 冲突。
     */
    @Provides
    @Singleton
    @Media3Client
    fun provideMedia3OkHttpClient(): OkHttpClient {
        return OkHttpClient.Builder()
            .connectTimeout(30, TimeUnit.SECONDS)
            .readTimeout(30, TimeUnit.SECONDS)
            .writeTimeout(30, TimeUnit.SECONDS)
            .callTimeout(0, TimeUnit.SECONDS) // 流式拉取不设总超时
            .addInterceptor(HttpLoggingInterceptor().apply {
                level = HttpLoggingInterceptor.Level.BASIC // 只记 URL,避免 BODY 把视频流打到日志
            })
            .followRedirects(true) // 跟随后端 302 到 CDN 直链(默认就是 true,显式标注)
            .build()
    }

    @Provides
    @Singleton
    fun provideNetdiskHttpFactory(@Media3Client client: OkHttpClient): NetdiskHttpFactory {
        return NetdiskHttpFactory(client)
    }
}

/**
 * Qualifier —— 标注"播放器专用"的 OkHttpClient,区别于 NetworkModule 的 API 客户端。
 * Hilt 遇到多个同类型 provider 时靠这个区分。
 */
@Qualifier
@Retention(AnnotationRetention.BINARY)
annotation class Media3Client
