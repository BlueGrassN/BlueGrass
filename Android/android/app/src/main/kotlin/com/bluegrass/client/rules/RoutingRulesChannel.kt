package com.bluegrass.client.rules

import io.flutter.plugin.common.BinaryMessenger
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel

internal class RoutingRulesChannel(
    messenger: BinaryMessenger,
    private val updater: RoutingRuleUpdater,
    private val afterUpdate: (RoutingRuleResult) -> Unit,
) {
    private var channel: MethodChannel? = MethodChannel(messenger, "bluegrass/routing-rules")
    private val attachment = Attachment()

    init { channel!!.setMethodCallHandler(::handle) }

    private fun handle(call: MethodCall, result: MethodChannel.Result) {
        if (!attachment.active) return
        if (call.method != "status" && call.method != "update") { result.notImplemented(); return }
        val reply = attachment.track(result) ?: run {
            result.success(RoutingRuleResult.unavailable("RULES_BUSY").json)
            return
        }
        if (call.method == "status") updater.status(reply)
        else {
            // Ignore all arguments: callers cannot select a directory, URL or automatic mode.
            val complete = afterUpdate
            updater.update(true) { value -> complete(value); reply(value) }
        }
    }

    fun detach() {
        attachment.active = false
        attachment.pending.clear()
        channel?.setMethodCallHandler(null)
        channel = null
        // Accepted work is process-owned. Detaching an Activity suppresses its replies,
        // and neither cancels a Job's update nor retains its engine in pending results.
    }

    private class Attachment {
        var active = true // accessed only on main, including callbacks
        val pending = mutableMapOf<Any, MethodChannel.Result>()
        fun track(result: MethodChannel.Result): ((RoutingRuleResult) -> Unit)? {
            if (pending.size >= 32) return null
            val key = Any()
            pending[key] = result
            return { value -> if (active) pending.remove(key)?.success(value.json) }
        }
    }
}
