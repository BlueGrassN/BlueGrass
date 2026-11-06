package com.bluegrass.client.vpn

import org.json.JSONArray
import org.json.JSONObject
import java.time.Duration
import java.time.Instant
import java.util.UUID

/** Only the five declared update fields are retained; never an arbitrary channel argument map. */
internal class VpnResourceUpdateInput(
    val runtimeId: String, val requestId: String, val accountKey: String, val resourceKey: String,
    val config: String, val receivedAt: Long,
)

internal object VpnRequestParser {
    const val MAX_CONFIG_BYTES = 32768

    fun parse(arguments: Any?, elapsedNow: Long): VpnRequest = guarded {
        val args = arguments as? Map<*, *> ?: invalid()
        text(args, "name", 512) // Never put names or node information into system notifications.
        parseConfig(uuid(args, "requestId"), text(args, "resourceKey", 1024),
            text(args, "accountKey", 1024), config(args), elapsedNow)
    }

    fun readUpdate(arguments: Any?, elapsedNow: Long): VpnResourceUpdateInput = guarded {
        val args = arguments as? Map<*, *> ?: invalid()
        VpnResourceUpdateInput(uuid(args, "runtimeId"), uuid(args, "requestId"),
            text(args, "accountKey", 1024), text(args, "resourceKey", 1024), config(args), elapsedNow)
    }

    fun parseUpdate(update: VpnResourceUpdateInput): VpnRequest = guarded {
        parseConfig(update.requestId, update.resourceKey, update.accountKey, update.config, update.receivedAt)
    }

    private fun parseConfig(requestId: String, resourceKey: String, accountKey: String,
        config: String, elapsedNow: Long): VpnRequest {
        val json = JSONObject(config)
        if (json.get("version") != 1 || json.get("host") !is String || json.getString("host").isBlank()) invalid()
        val port = json.get("port")
        if (port !is Int || port !in 1..65535 || json.get("protocolConfig") !is JSONObject) invalid()
        // JSONObject.getString coerces non-strings; only an absent field gets the legacy default.
        val routingMode = if (json.has("routingMode")) json.get("routingMode") as? String ?: invalid() else "global"
        if (routingMode !in setOf("global", "bypass_cn")) invalid()
        if (!json.has("expiresAtUtc")) invalid()
        val effective = utc(json.get("effectiveNowUtc"))
        val expires = if (json.isNull("expiresAtUtc")) null else utc(json.get("expiresAtUtc"))
        val deadline = expires?.let {
            val remaining = Duration.between(effective, it).toMillis()
            if (remaining <= 0) throw VpnFailure("RESOURCE_EXPIRED")
            Math.addExact(elapsedNow, remaining)
        }
        val transport = VpnTransport(json.getString("host"), port,
            jsonValue(json.getJSONObject("protocolConfig"), 0), routingMode)
        return VpnRequest(requestId, resourceKey, accountKey, config, deadline, transport)
    }

    private fun jsonValue(value: Any?, depth: Int): Any? {
        if (depth > 32) invalid()
        return when (value) {
            null, JSONObject.NULL -> null
            is JSONObject -> value.keys().asSequence().associateWith { jsonValue(value.get(it), depth + 1) }
            is JSONArray -> (0 until value.length()).map { jsonValue(value.get(it), depth + 1) }
            is String, is Boolean, is Number -> value
            else -> invalid()
        }
    }

    private fun text(args: Map<*, *>, key: String, limit: Int): String {
        val value = args[key] as? String ?: invalid()
        if (value.isBlank() || value.length > limit) invalid()
        return value
    }
    private fun uuid(args: Map<*, *>, key: String): String {
        val value = text(args, key, 36)
        if (UUID.fromString(value).toString() != value.lowercase()) invalid()
        return value
    }
    private fun config(args: Map<*, *>): String {
        val value = text(args, "config", MAX_CONFIG_BYTES)
        if (value.toByteArray(Charsets.UTF_8).size > MAX_CONFIG_BYTES) invalid()
        return value
    }
    private fun utc(value: Any): Instant {
        val text = value as? String ?: invalid()
        if (!text.endsWith("Z")) invalid()
        return Instant.parse(text)
    }
    private fun <T> guarded(work: () -> T): T = try { work() }
        catch (failure: VpnFailure) { throw failure }
        catch (_: Exception) { invalid() }
        catch (_: StackOverflowError) { invalid() }
    private fun invalid(): Nothing = throw VpnFailure("INVALID_ARGUMENT")
}
