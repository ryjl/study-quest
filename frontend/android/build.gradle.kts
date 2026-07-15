allprojects {
    repositories {
        google()
        mavenCentral()
    }
}

val newBuildDir: Directory =
    rootProject.layout.buildDirectory
        .dir("../../build")
        .get()
rootProject.layout.buildDirectory.value(newBuildDir)

subprojects {
    val newSubprojectBuildDir: Directory = newBuildDir.dir(project.name)
    project.layout.buildDirectory.value(newSubprojectBuildDir)
}
subprojects {
    project.evaluationDependsOn(":app")
}

tasks.register<Delete>("clean") {
    delete(rootProject.layout.buildDirectory)
}
subprojects {
    val configureAndroid = {
        if (plugins.hasPlugin("com.android.library")) {
            val android = extensions.findByName("android")
            if (android is com.android.build.gradle.BaseExtension) {
                android.compileSdkVersion(36)
            }
        }
    }

    if (state.executed) {
        configureAndroid()
    } else {
        afterEvaluate {
            configureAndroid()
        }
    }
}

gradle.projectsEvaluated {
    subprojects {
        val android = extensions.findByName("android")
        if (android is com.android.build.gradle.BaseExtension) {
            val targetJavaVersion = android.compileOptions.targetCompatibility.toString()
            val kotlinJvmTarget = if (targetJavaVersion == "1.8") "1.8" else targetJavaVersion
            
            tasks.configureEach {
                if (name.startsWith("compile") && name.endsWith("Kotlin")) {
                    try {
                        val kotlinOptions = this::class.java.getMethod("getKotlinOptions").invoke(this)
                        kotlinOptions::class.java.getMethod("setJvmTarget", String::class.java).invoke(kotlinOptions, kotlinJvmTarget)
                    } catch (e: Exception) {
                        // ignore
                    }
                }
            }
        }
    }
}
