package com.bluegrass.client.vpn

import android.Manifest
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.net.VpnService
import android.os.Build
import io.flutter.plugin.common.BinaryMessenger
import io.flutter.plugin.common.EventChannel
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel

/** Activity owns only authorization and channel attachments; the Service owns the VPN. */
internal class VpnActivityBridge(private val activity: Activity, messenger: BinaryMessenger) {
    private val methods = MethodChannel(messenger, "bluegrass/vpn")
    private val events = EventChannel(messenger, "bluegrass/vpn/events")
    private var prepared: MethodChannel.Result? = null
    private val pending = mutableSetOf<MethodChannel.Result>()
    private var observer: ((VpnSnapshot) -> Unit)? = null
    private var attached = true

    init {
        methods.setMethodCallHandler(::handle)
        events.setStreamHandler(object : EventChannel.StreamHandler {
            override fun onListen(arguments: Any?, sink: EventChannel.EventSink) {
                observer?.let(VpnRuntime::unlisten)
                val listener: (VpnSnapshot) -> Unit = { sink.success(it.toMap()) }
                observer = listener
                VpnRuntime.listen(listener)
            }
            override fun onCancel(arguments: Any?) {
                observer?.let(VpnRuntime::unlisten)
                observer = null
            }
        })
    }

    private fun handle(call: MethodCall, result: MethodChannel.Result) {
        if (!attached) { result.error("ACTIVITY_DESTROYED", "ACTIVITY_DESTROYED", null); return }
        when (call.method) {
            "traffic" -> {
                if (pending.size >= 32) { result.error("BUSY", "BUSY", null); return }
                pending.add(result)
                VpnRuntime.traffic { value, error ->
                    if (attached && pending.remove(result)) {
                        if (error == null) result.success(value) else result.error(error, error, null)
                    }
                }
            }
            "prepare" -> prepare(result)
            "connect" -> {
                pending.add(result)
                VpnRuntime.connect(activity.applicationContext, call.arguments, reply(result))
            }
            "updateResource" -> {
                pending.add(result)
                VpnRuntime.updateResource(call.arguments, reply(result))
            }
            "status", "disconnect", "observeExit" -> {
                pending.add(result)
                VpnRuntime.command(call.method, reply = reply(result))
            }
            "pause" -> {
                val reason = (call.arguments as? Map<*, *>)?.get("reason") as? String
                // Never echo caller-supplied text into events or notifications.
                val code = VpnPauseReasons.sanitize(reason)
                pending.add(result)
                VpnRuntime.command("pause", code, reply(result))
            }
            else -> result.notImplemented()
        }
    }

    @Suppress("DEPRECATION")
    private fun prepare(result: MethodChannel.Result) {
        if (prepared != null) { result.error("PREPARE_IN_PROGRESS", "PREPARE_IN_PROGRESS", null); return }
        try {
            val intent = VpnService.prepare(activity)
            if (intent == null) {
                requestNotificationPermission()
                result.success(true)
            } else {
                prepared = result
                activity.startActivityForResult(intent, PREPARE_REQUEST)
            }
        } catch (_: Exception) {
            prepared = null
            result.success(false)
        }
    }

    fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?): Boolean {
        if (requestCode != PREPARE_REQUEST) return false
        val result = prepared ?: return true
        prepared = null
        val allowed = attached && resultCode == Activity.RESULT_OK &&
            try { VpnService.prepare(activity) == null } catch (_: Exception) { false }
        if (allowed) requestNotificationPermission()
        result.success(allowed)
        return true
    }

    private fun requestNotificationPermission() {
        if (Build.VERSION.SDK_INT >= 33 && activity.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            // Notification permission is separate from VPN consent and never fabricates it.
            try { activity.requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), NOTIFICATION_REQUEST) }
            catch (_: Exception) { }
        }
    }

    private fun reply(result: MethodChannel.Result): (VpnSnapshot?, String?) -> Unit = { value, error ->
        if (attached && pending.remove(result)) {
            if (error == null) result.success(value!!.toMap()) else result.error(error, error, null)
        }
    }

    fun detach() {
        if (!attached) return
        attached = false
        // Resolve authorization denial/destroy exactly once, without mutating the VPN.
        prepared?.success(false)
        prepared = null
        for (result in pending) result.error("ACTIVITY_DESTROYED", "ACTIVITY_DESTROYED", null)
        pending.clear()
        observer?.let(VpnRuntime::unlisten)
        observer = null
        methods.setMethodCallHandler(null)
        events.setStreamHandler(null)
    }

    companion object {
        const val PREPARE_REQUEST = 0xB701
        private const val NOTIFICATION_REQUEST = 0xB702
    }
}
