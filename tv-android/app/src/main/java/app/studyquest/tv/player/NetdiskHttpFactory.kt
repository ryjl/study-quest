package app.studyquest.tv.player

import okhttp3.OkHttpClient
import androidx.media3.datasource.okhttp.OkHttpDataSource

/**
 * 网盘直链鉴权头注入工厂。
 *
 * 对照 `docs/business-rules.md` 第 7 节 + PAD 端
 * `frontend/lib/ui/screen/player_screen.dart` 的 `Media(url, httpHeaders: headers)`。
 *
 * **为什么需要这个**:play-info 返回的 url 是 AList 网盘代理地址,实际播放时后端 302
 * 重定向到云盘 CDN 直链(115 等)。这些 CDN 直链需要鉴权头(尤其 `Referer`),
 * 否则返回 403。ExoPlayer 的 HTTP 数据源必须把这些头设为**默认请求头**,对所有
 * 请求(含 302 跳转后的 CDN 直链)生效。
 *
 * 用 OkHttpDataSource(来自 media3-datasource-okhttp)而不是 DefaultHttpDataSource,
 * 因为 OkHttp 对自定义头 + 重定向 + 超时的控制更可靠。
 *
 * 用法(在 PlayerScreenViewModel 里):
 * ```
 * val dataSourceFactory = netdiskHttpFactory.create(playInfo.headers)
 * val mediaSourceFactory = DefaultMediaSourceFactory(context).setDataSourceFactory(dataSourceFactory)
 * exoPlayer.setMediaSource(mediaItem, mediaSourceFactory) // 或 setMediaItem + prepare
 * ```
 *
 * 注:headers 来自 play-info 响应的 `headers: Map<String,String>` 字段
 * (如 `{Referer: "https://...", User-Agent: "..."}`)。
 */
class NetdiskHttpFactory(
    private val okHttpClient: OkHttpClient,
) {
    /**
     * 创建一个带 [headers] 默认请求头的 OkHttpDataSource.Factory。
     *
     * 这些头会被注入到 ExoPlayer 拉流时的**每个** HTTP 请求(含 302 跳转后的 CDN 直链)。
     * 这是 ExoPlayer 播放网盘流的关键 —— 没有它,115 等网盘直链返回 403。
     */
    fun create(headers: Map<String, String>): OkHttpDataSource.Factory {
        return OkHttpDataSource.Factory(okHttpClient).apply {
            setDefaultRequestProperties(headers)
        }
    }
}
