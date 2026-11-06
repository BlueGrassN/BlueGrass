package com.bluegrass.client

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Handler
import android.os.Looper
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel
import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.ThreadPoolExecutor
import java.util.concurrent.TimeUnit
import com.bluegrass.client.vpn.VpnActivityBridge
import com.bluegrass.client.rules.RoutingRulesChannel
import com.bluegrass.client.rules.RoutingRulesRuntime

class MainActivity : FlutterActivity() {
    private var storageChannel: MethodChannel? = null
    private var attachment: Attachment? = null
    private var vpnBridge: VpnActivityBridge? = null
    private var routingRulesChannel: RoutingRulesChannel? = null

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        vpnBridge?.detach()
        vpnBridge = VpnActivityBridge(this, flutterEngine.dartExecutor.binaryMessenger)
        detachStorageChannel()
        val current = Attachment()
        val appContext = applicationContext
        routingRulesChannel?.detach()
        routingRulesChannel = RoutingRulesChannel(
            flutterEngine.dartExecutor.binaryMessenger, RoutingRulesRuntime.updater(appContext),
        ) { RoutingRulesRuntime.afterManualUpdate(appContext, it) }
        attachment = current
        storageChannel = MethodChannel(flutterEngine.dartExecutor.binaryMessenger, CHANNEL).also {
            it.setMethodCallHandler { call, result ->
                if (call.method == "openOfficialWebsite") {
                    try {
                        startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("https://sunipip.com/")))
                        result.success(null)
                    } catch (_: Exception) {
                        result.error("website_unavailable", "website_unavailable", null)
                    }
                } else dispatch(appContext, current, call, result)
            }
        }
    }

    override fun cleanUpFlutterEngine(flutterEngine: FlutterEngine) {
        routingRulesChannel?.detach()
        routingRulesChannel = null
        vpnBridge?.detach()
        vpnBridge = null
        detachStorageChannel()
        super.cleanUpFlutterEngine(flutterEngine)
    }

    override fun onDestroy() {
        routingRulesChannel?.detach()
        routingRulesChannel = null
        vpnBridge?.detach()
        vpnBridge = null
        detachStorageChannel()
        super.onDestroy()
    }

    override fun onResume() {
        super.onResume()
        RoutingRulesRuntime.foreground(applicationContext)
    }

    @Deprecated("Android platform result API is required for VpnService.prepare")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (vpnBridge?.onActivityResult(requestCode, resultCode, data) == true) return
        super.onActivityResult(requestCode, resultCode, data)
    }

    private fun detachStorageChannel() {
        attachment?.active = false
        attachment = null
        storageChannel?.setMethodCallHandler(null)
        storageChannel = null
    }

    private class Attachment {
        // Accessed on the main thread, including queued result callbacks.
        var active = true
    }

    companion object {
        private const val CHANNEL = "bluegrass/android"
        private val mainHandler = Handler(Looper.getMainLooper())

        // Process lifetime: a recreated Activity must queue behind earlier writes/clears.
        // Jobs hold only the application context. Detachment suppresses stale replies,
        // while accepted writes still finish in order. The bounded queue rejects overload.
        private val storageExecutor = ThreadPoolExecutor(
            1, 1, 0L, TimeUnit.MILLISECONDS, ArrayBlockingQueue<Runnable>(32),
            { runnable -> Thread(runnable, "bluegrass-storage") },
            ThreadPoolExecutor.AbortPolicy(),
        )

        private fun dispatch(
            context: Context,
            attachment: Attachment,
            call: MethodCall,
            result: MethodChannel.Result,
        ) {
            // Retain only the validated value, never the caller's entire argument map.
            val operation: (SecureSessionStorage) -> Any? = when (call.method) {
                "sessionRead" -> { store -> store.read() }
                "sessionClear" -> { store -> store.clear(); null }
                "deviceId" -> { store -> store.deviceId() }
                "themeRead" -> { store -> store.themeRead() }
                "routingModeRead" -> { store -> store.routingModeRead() }
                "sessionReplace", "themeWrite", "routingModeWrite" -> {
                    val value = (call.arguments as? Map<*, *>)?.get("value") as? String
                    if (value == null || value.length > SecureSessionStorage.MAX_SESSION_BYTES ||
                        (call.method == "themeWrite" && value !in setOf("system", "light", "dark")) ||
                        (call.method == "routingModeWrite" && value !in setOf("global", "bypass_cn"))
                    ) {
                        reply(attachment) { result.error("invalid_argument", "invalid_argument", null) }
                        return
                    }
                    if (call.method == "sessionReplace") {
                        { store -> store.replace(value); null }
                    } else if (call.method == "routingModeWrite") {
                        { store -> store.routingModeWrite(value); null }
                    } else {
                        { store -> store.themeWrite(value); null }
                    }
                }
                else -> {
                    reply(attachment) { result.notImplemented() }
                    return
                }
            }
            try {
                storageExecutor.execute {
                    try {
                        val value = operation(SecureSessionStorage(context))
                        reply(attachment) { result.success(value) }
                    } catch (_: SecureSessionStorage.InvalidArgumentException) {
                        reply(attachment) { result.error("invalid_argument", "invalid_argument", null) }
                    } catch (_: Exception) {
                        reply(attachment) { result.error("storage_error", "storage_error", null) }
                    }
                }
            } catch (_: RejectedExecutionException) {
                reply(attachment) { result.error("storage_error", "storage_error", null) }
            }
        }

        private fun reply(attachment: Attachment, complete: () -> Unit) {
            mainHandler.post { if (attachment.active) complete() }
        }
    }
}
