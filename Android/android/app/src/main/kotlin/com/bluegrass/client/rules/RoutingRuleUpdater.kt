package com.bluegrass.client.rules

import android.content.Context
import android.os.Handler
import android.os.Looper
import com.bluegrass.core.bridge.Bridge
import org.json.JSONObject
import java.io.File
import java.time.Instant
import java.util.concurrent.Executor
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.ScheduledFuture
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

internal interface RoutingRulesNative {
    fun configure(directory: String)
    fun status(): String
    fun update(force: Boolean): String
    fun cancel()
}

internal class GomobileRoutingRules(context: Context) : RoutingRulesNative {
    private val app = context.applicationContext
    override fun configure(directory: String) {
        go.Seq.setContext(app)
        Bridge.configureRoutingRules(directory)
    }
    override fun status(): String = Bridge.routingRulesStatus()
    override fun update(force: Boolean): String = Bridge.updateRoutingRules(force)
    override fun cancel() = Bridge.cancelRoutingRuleUpdate()
}

/** Only fixed, non-sensitive fields cross the channel, including on JNI/JSON failures. */
internal data class RoutingRuleResult(val json: String, val retry: Boolean) {
    companion object {
        private val errors = setOf("", "UPDATE_FAILED", "UPDATE_CANCELLED", "CACHE_INVALID")
        fun decode(raw: String, error: String? = null, busy: Boolean = false): RoutingRuleResult {
            require(raw.length <= 4096)
            val input = JSONObject(raw)
            val version = input.get("version") as String
            require(version.matches(Regex("[0-9]{12}")))
            val source = input.get("source") as String
            require(source == "bundled" || source == "downloaded")
            val checked = timestamp(input.get("checkedAt") as String)
            val updated = timestamp(input.get("updatedAt") as String)
            val updating = (input.get("updating") as Boolean) || busy
            val nativeError = input.get("lastError") as String
            require(nativeError in errors)
            return encode(version, source, checked, updated, updating, error ?: nativeError)
        }

        fun unavailable(error: String) = encode("", "bundled", "", "", false, error)

        private fun timestamp(value: String): String {
            require(value.length <= 40)
            if (value.isNotEmpty()) Instant.parse(value)
            return value
        }

        private fun encode(version: String, source: String, checked: String, updated: String,
                           updating: Boolean, error: String): RoutingRuleResult = RoutingRuleResult(
            JSONObject().put("version", version).put("source", source)
                .put("checkedAt", checked).put("updatedAt", updated)
                .put("updating", updating).put("lastError", error).toString(),
            updating || error.isNotEmpty(),
        )
    }
}

/**
 * One instance per process. The control executor alone owns update/cancel transitions;
 * downloads and status reads use separate workers. No native work runs on the UI thread.
 */
internal class RoutingRuleUpdater(
    context: Context,
    private val native: RoutingRulesNative = GomobileRoutingRules(context),
    private val control: ScheduledExecutorService = Executors.newSingleThreadScheduledExecutor {
        Thread(it, "bluegrass-rules-control")
    },
    private val updates: Executor = Executors.newSingleThreadExecutor { Thread(it, "bluegrass-rules-update") },
    private val reads: Executor = Executors.newSingleThreadExecutor { Thread(it, "bluegrass-rules-status") },
    private val deliver: (() -> Unit) -> Unit = mainDelivery(),
) {
    private val app = context.applicationContext
    private val configurationLock = Any()
    private var configured = false // guarded by configurationLock; never held during a download
    @Volatile private var active: Ticket? = null

    internal class Ticket {
        val cancelled = AtomicBoolean()
        val enteredNative = AtomicBoolean()
        var cancellation: ScheduledFuture<*>? = null // control executor only
    }

    /** Called on the VPN lifecycle worker before VpnBackend.start; failure retains bundled rules. */
    fun prepareForVpn(): Boolean = synchronized(configurationLock) {
        if (configured) return@synchronized true
        try {
            val directory = File(app.noBackupFilesDir, "routing-rules")
            check(directory.isDirectory || directory.mkdirs())
            native.configure(directory.absolutePath)
            configured = true
        } catch (_: Exception) {
            // No exception message, path, network configuration or credentials are logged.
        } catch (_: LinkageError) {
            // A genuinely unavailable native library must not disable bundled VPN fallback.
        }
        configured
    }

    fun status(reply: (RoutingRuleResult) -> Unit) {
        reads.execute {
            val error = if (prepareForVpn()) null else "RULES_CONFIG_FAILED"
            val value = readStatus(error, busy = active != null)
            deliver { reply(value) }
        }
    }

    fun update(force: Boolean, reply: (RoutingRuleResult) -> Unit): Ticket {
        val ticket = Ticket()
        control.execute {
            if (ticket.cancelled.get()) return@execute
            if (active != null) {
                // This caller observes the current work; it never acquires cancellation ownership.
                reads.execute {
                    val value = readStatus(busy = true)
                    deliver { if (!ticket.cancelled.get()) reply(value) }
                }
            } else {
                active = ticket
                updates.execute {
                    val value = try {
                        when {
                            ticket.cancelled.get() -> null
                            !prepareForVpn() -> readStatus("RULES_CONFIG_FAILED")
                            ticket.cancelled.get() -> null
                            else -> {
                                ticket.enteredNative.set(true)
                                RoutingRuleResult.decode(native.update(force))
                            }
                        }
                    } catch (_: Exception) {
                        readStatus("RULES_UPDATE_FAILED")
                    } catch (_: LinkageError) {
                        RoutingRuleResult.unavailable("RULES_UNAVAILABLE")
                    }
                    control.execute {
                        ticket.cancellation?.cancel(false)
                        if (active === ticket) active = null
                        if (value != null) deliver { if (!ticket.cancelled.get()) reply(value) }
                    }
                }
            }
        }
        return ticket
    }

    fun cancel(ticket: Ticket) {
        ticket.cancelled.set(true)
        control.execute {
            if (active !== ticket || ticket.cancellation != null) return@execute
            // A stop can arrive just before Go installs its cancel function. Repeat until
            // that SAME synchronous call returns, closing the cancel-before-entry race.
            // Completion and every cancel run on control; a later update cannot be hit.
            ticket.cancellation = control.scheduleWithFixedDelay({
                if (active === ticket && ticket.enteredNative.get()) {
                    try { native.cancel() } catch (_: Exception) { } catch (_: LinkageError) { }
                }
            }, 0, 100, TimeUnit.MILLISECONDS)
        }
    }

    private fun readStatus(error: String? = null, busy: Boolean = false): RoutingRuleResult = try {
        RoutingRuleResult.decode(native.status(), error, busy)
    } catch (_: Exception) {
        RoutingRuleResult.unavailable(error ?: "RULES_UNAVAILABLE")
    } catch (_: LinkageError) {
        RoutingRuleResult.unavailable("RULES_UNAVAILABLE")
    }

    companion object {
        private fun mainDelivery(): (() -> Unit) -> Unit {
            val main = Handler(Looper.getMainLooper())
            return { main.post(it) }
        }
    }
}
