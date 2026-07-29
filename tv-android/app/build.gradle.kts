plugins {
    alias(libs.plugins.android.application)
    // 注意:AGP 9.0 已内置 Kotlin 支持,不再需要显式 apply org.jetbrains.kotlin.android。
    // 见 https://kotl.in/gradle/agp-built-in-kotlin
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.ksp)
    alias(libs.plugins.hilt)
}

android {
    namespace = "com.revin.studyquest.tv"
    compileSdk = 36

    defaultConfig {
        applicationId = "com.revin.studyquest.tv"
        // Compose for TV (androidx.tv:tv-material 1.0.0) 要求 API 21+。
        // 对齐 JetStreamCompose 范例 + Flutter 端的 minSdk。
        minSdk = 21
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"
    }

    buildTypes {
        release {
            // 初期用 debug 签名跑通(对齐 frontend 现状,TODO 后续配正式签名)。
            isMinifyEnabled = false
            signingConfig = signingConfigs.getByName("debug")
        }
        debug {
            applicationIdSuffix = ".debug"
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlin {
        compilerOptions {
            jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
        }
    }

    buildFeatures {
        compose = true
    }
}

dependencies {
    // Compose BOM 统一管理 compose 库版本。
    val composeBom = platform(libs.androidx.compose.bom)
    implementation(composeBom)

    // Compose core
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.compose.ui)
    implementation(libs.androidx.compose.ui.graphics)
    implementation(libs.androidx.compose.ui.tooling.preview)
    implementation(libs.androidx.compose.material.icons.extended)
    // 基础 material(提供 Text/Button 等基础组件;TV 专属组件在下面 tv-material)。
    // 占位阶段用 androidx.compose.material.Text;UI 主题层完成后逐步迁 tv-material3。
    implementation("androidx.compose.material:material")
    debugImplementation(libs.androidx.compose.ui.tooling)

    // TV Material(Compose for TV)— D-pad 友好组件(Surface/Button/Card...)
    implementation(libs.androidx.tv.material)

    // Lifecycle / ViewModel / Navigation
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.core.splashscreen)
    implementation(libs.androidx.lifecycle.runtime.ktx)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)

    // Media3 ExoPlayer(播放器核心)+ Compose UI surface + OkHttp datasource
    // (datasource-okhttp 用于注入网盘 Referer 鉴权头)
    // (ui 提供 SubtitleView —— PlayerSurface 只渲染视频画面不画字幕,字幕需要单独的
    //  SubtitleView overlay 监听 player 的 text cues,对照 media3 官方文档。)
    implementation(libs.androidx.media3.exoplayer)
    implementation(libs.androidx.media3.ui)
    implementation(libs.androidx.media3.ui.compose)
    implementation(libs.androidx.media3.datasource.okhttp)

    // Hilt(DI)
    implementation(libs.hilt.android)
    ksp(libs.hilt.compiler)
    implementation(libs.androidx.hilt.navigation.compose)

    // 网络(Retrofit + kotlinx.serialization)
    implementation(libs.retrofit)
    implementation(libs.retrofit.kotlinx.serialization.converter)
    implementation(libs.okhttp)
    implementation(libs.okhttp.logging.interceptor)
    implementation(libs.kotlinx.serialization.json)

    // 图片加载
    implementation(libs.coil.compose)

    // 加密存储(token / baseUrl 持久化)
    implementation(libs.androidx.security.crypto)

    // 测试
    testImplementation(libs.junit)
    androidTestImplementation(libs.androidx.junit)
}
