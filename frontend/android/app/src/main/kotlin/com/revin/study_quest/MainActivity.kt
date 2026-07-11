package com.revin.study_quest

import android.content.Intent
import android.os.Build
import androidx.core.content.FileProvider
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import java.io.File

class MainActivity : FlutterActivity() {
    // Channel for native helpers that Flutter can't (cleanly) do itself:
    //  - getAbi: read the device's primary ABI so the OTA check requests the
    //    matching APK build.
    //  - installApk: launch the system package installer via a FileProvider
    //    content:// URI. Must be native because Android 7+ (API 24+) forbids
    //    sharing file:// URIs across processes, and the installer needs the
    //    FLAG_GRANT_READ_URI_PERMISSION that url_launcher can't set.
    private val channelName = "study_quest/device"

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, channelName)
            .setMethodCallHandler { call, result ->
                when (call.method) {
                    "getAbi" -> {
                        // SUPPORTED_ABIS is ordered by preference; [0] is the
                        // best match for this device's CPU.
                        val abi = if (Build.SUPPORTED_ABIS.isNotEmpty()) {
                            Build.SUPPORTED_ABIS[0]
                        } else {
                            ""
                        }
                        result.success(abi)
                    }
                    "installApk" -> {
                        val path = call.argument<String>("path")
                        if (path == null) {
                            result.error("invalid_arg", "path is required", null)
                            return@setMethodCallHandler
                        }
                        val launched = try {
                            launchInstaller(path)
                        } catch (e: Exception) {
                            result.error("install_failed", e.message, null)
                            return@setMethodCallHandler
                        }
                        result.success(launched)
                    }
                    else -> result.notImplemented()
                }
            }
    }

    /**
     * Builds a FileProvider content:// URI for the APK and launches the system
     * package installer with read permission granted. Returns true if the
     * ACTION_VIEW intent resolved to an activity (installer available).
     *
     * Using content:// (not file://) is mandatory on API 24+; a bare file://
     * URI raises FileUriExposedException at the framework level.
     */
    private fun launchInstaller(path: String): Boolean {
        val file = File(path)
        val authority = "${packageName}.fileprovider"
        val uri = FileProvider.getUriForFile(this, authority, file)

        val intent = Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(uri, "application/vnd.android.package-archive")
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            // Start the installer as a new task so it shows outside our app.
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        }
        // Guard against no installer (unlikely on a real device, but cheap).
        if (intent.resolveActivity(packageManager) == null) {
            return false
        }
        startActivity(intent)
        return true
    }
}
