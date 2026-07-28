// Root build file. Plugin versions come from gradle/libs.versions.toml and
// are applied per-module; no version literals here so bumps stay single-source.
// 注意:AGP 9.0 内置 Kotlin 支持,kotlin.android 插件不再需要。
plugins {
    alias(libs.plugins.android.application) apply false
    alias(libs.plugins.kotlin.compose) apply false
    alias(libs.plugins.kotlin.serialization) apply false
    alias(libs.plugins.ksp) apply false
    alias(libs.plugins.hilt) apply false
}
