package app.studyquest.tv.data.remote.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * 用户对象（列表/登录后回填）。对应 Dart: `User`；后端 `userResponse`
 * （user_handler.go GetUsers）全小写：`id` / `nickname` / `avatar_url` / `role`。
 *
 * 注意：avatar 后端存的是 server-relative 路径（`/uploads/xxx.jpg`），需要调用方
 * 拼 baseUrl（Dart 在 fromJson 里用 AppConfig.baseUrl 拼）。这里 DTO 不做拼接，
 * 保持纯数据；解析成绝对 URL 放在 Repository / UI 层（那里能拿到 baseUrl）。
 */
@Serializable
data class UserDto(
    @SerialName("id") val id: Int = 0,
    @SerialName("nickname") val nickname: String = "",
    @SerialName("avatar_url") val avatarUrl: String = "",
    @SerialName("role") val role: String = "student",
)

/**
 * `/users/login` 响应。后端 user_handler.go Login 下发：
 *   - token：opaque session token（**不是** user id），后续请求放 `Authorization:
 *     Bearer <token>`。
 *   - role：登录用户的角色（便于客户端不二次请求 users 列表）。
 *   - user_id：回显，用于客户端确认。
 */
@Serializable
data class LoginResponseDto(
    @SerialName("token") val token: String = "",
    @SerialName("role") val role: String = "student",
    @SerialName("user_id") val userId: Int = 0,
)

/**
 * `/users/login` 请求体。deviceName 可选（OS 层设备标签，给 admin 设备列表用；
 * 缺失时后端回退到 User-Agent）。
 *
 * 对应 Dart: api_service.dart loginUser 的 body。
 */
@Serializable
data class LoginRequestDto(
    @SerialName("user_id") val userId: Int,
    @SerialName("pin") val pin: String,
    @SerialName("device_name") val deviceName: String? = null,
)
