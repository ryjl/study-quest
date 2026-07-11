plugins {
    id("com.android.application")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

android {
    namespace = "com.revin.study_quest"
    compileSdk = flutter.compileSdkVersion
    ndkVersion = flutter.ndkVersion

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    defaultConfig {
        // TODO: Specify your own unique Application ID (https://developer.android.com/studio/build/application-id.html).
        applicationId = "com.revin.study_quest"
        // You can update the following values to match your application needs.
        // For more information, see: https://flutter.dev/to/review-gradle-config.
        minSdk = flutter.minSdkVersion
        targetSdk = flutter.targetSdkVersion
        versionCode = flutter.versionCode
        versionName = flutter.versionName
    }

    // MuMu (and most x86 emulators) only run x86_64. media_kit_libs_android_video
    // bundles arm64-v8a / armeabi-v7a / x86 / x86_64 .so inside dependency
    // jars, which ndk.abiFilters alone does NOT prune. The packaging.jniLibs
    // excludes below strip the unwanted ABIs at the final APK assembly step,
    // cutting the APK from ~180MB to ~45MB and avoiding houdini translation.
    packaging {
        jniLibs {
            excludes += listOf(
                "**/lib/arm64-v8a/**",
                "**/lib/armeabi-v7a/**",
                "**/lib/x86/**",
            )
        }
    }

    buildTypes {
        release {
            // TODO: Add your own signing config for the release build.
            // Signing with the debug keys for now, so `flutter run --release` works.
            signingConfig = signingConfigs.getByName("debug")
        }
    }
}

kotlin {
    compilerOptions {
        jvmTarget = org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17
    }
}

flutter {
    source = "../.."
}

dependencies {
    // FileProvider (used by the OTA APK self-install flow to expose the
    // downloaded update to the system package installer). Flutter pulls core
    // transitively, but declaring it explicitly avoids resolution surprises.
    implementation("androidx.core:core-ktx:1.13.1")
}
