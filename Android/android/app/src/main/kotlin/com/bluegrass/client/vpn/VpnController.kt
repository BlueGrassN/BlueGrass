package com.bluegrass.client.vpn

import java.util.UUID
import java.util.concurrent.atomic.AtomicBoolean
import java.math.BigDecimal

/** Deliberately not a data class: no generated toString containing configuration. */
internal class VpnRequest(
    val requestId: String,
    val resourceKey: String,
    val accountKey: String,
    val config: String,
    val deadline: Long?,
    val transport: VpnTransport,
)

/** Secret-bearing structural value. Deliberately no generated toString/hash/digest. */
internal class VpnTransport(private val host: String, private val port: Int, protocol: Any?,
    val routingMode: String = "global") {
    private val protocol = canonical(protocol, 0)
    fun matches(other: VpnTransport): Boolean = host == other.host && port == other.port &&
        protocol == other.protocol && routingMode == other.routingMode

    private fun canonical(value: Any?, depth: Int): Any? {
        if (depth > 32) throw VpnFailure("INVALID_ARGUMENT")
        return when (value) {
            null, is String, is Boolean -> value
            is Number -> try { BigDecimal(value.toString()).stripTrailingZeros() }
                catch (_: Exception) { throw VpnFailure("INVALID_ARGUMENT") }
            is Map<*, *> -> value.entries.associate { entry ->
                (entry.key as? String ?: throw VpnFailure("INVALID_ARGUMENT")) to canonical(entry.value, depth + 1)
            }.toSortedMap()
            is List<*> -> value.map { canonical(it, depth + 1) }
            else -> throw VpnFailure("INVALID_ARGUMENT")
        }
    }
}

internal class VpnFailure(val code: String) : Exception(code)

internal object VpnPauseReasons {
    fun sanitize(value: String?): String = when (value?.takeIf { it.length <= 64 }?.uppercase()) {
        "RESOURCE_EXPIRED", "EXPIRED", "RESOURCEEXPIRED" -> "RESOURCE_EXPIRED"
        "RESOURCE_REVOKED", "REVOKED", "RESOURCEREVOKED" -> "RESOURCE_REVOKED"
        "RESOURCE_UNAVAILABLE" -> "RESOURCE_UNAVAILABLE"
        "ELIGIBILITY_UNCONFIRMED" -> "ELIGIBILITY_UNCONFIRMED"
        "SESSION_REVOKED" -> "SESSION_REVOKED"
        "CONFIG_CHANGED" -> "CONFIG_CHANGED"
        else -> "PAUSED"
    }
}

internal data class VpnSnapshot(
    val runtimeId: String = UUID.randomUUID().toString(),
    val generation: Long = 0,
    val state: String = "idle",
    val requestId: String? = null,
    val resourceKey: String? = null,
    val accountKey: String? = null,
    val tunActive: Boolean = false,
    val errorCode: String? = null,
    val observedExitIp: String? = null,
    val observationState: String = "unknown",
    val routingMode: String = "global",
) {
    fun toMap(): Map<String, Any?> = linkedMapOf(
        "version" to 1, "runtimeId" to runtimeId, "generation" to generation,
        "state" to state, "requestId" to requestId, "resourceKey" to resourceKey,
        "accountKey" to accountKey, "tunActive" to tunActive, "errorCode" to errorCode,
        "observedExitIp" to observedExitIp, "observationState" to observationState,
        "routingMode" to routingMode,
    )
}

internal interface VpnTunnel { fun close() }
internal interface VpnCore {
    fun close()
    fun observeExit(): String
    fun traffic(): NativeTraffic? = null
}
internal interface VpnBackend {
    fun hasNetwork(): Boolean
    fun networkRevision(): Long
    fun openTun(): VpnTunnel
    fun start(request: VpnRequest, tun: VpnTunnel, valid: () -> Boolean): VpnCore
    fun clearUnderlyingNetwork()
}

/** All methods and resource ownership run on one lifecycle worker; probes only return values. */
internal class VpnController(
    private val backend: VpnBackend,
    private val now: () -> Long,
    private val latestCommand: () -> Long,
    private val publish: (VpnSnapshot) -> Unit,
    private val probe: (() -> Unit) -> Unit,
    private val dispatch: (() -> Unit) -> Unit,
) {
    var snapshot = VpnSnapshot()
        private set
    @Volatile var deadline: Long? = null
        private set
    private var request: VpnRequest? = null
    private var transport: VpnTransport? = null
    private var tun: VpnTunnel? = null
    private var core: VpnCore? = null
    private var coreValid: AtomicBoolean? = null
    private var ticket = 0L
    private var networkRevision = -1L
    private var resumeOnNetwork = false
    private var probeRevision = 0L
    private val trafficLedger = VpnTrafficLedger()
    val commandTicket: Long get() = ticket

    fun connect(next: VpnRequest, command: Long): VpnSnapshot {
        if (snapshot.accountKey != null && snapshot.accountKey != next.accountKey) throw VpnFailure("ACCOUNT_MISMATCH")
        if (next.deadline != null && next.deadline <= now()) throw VpnFailure("RESOURCE_EXPIRED")
        val continueTraffic = snapshot.accountKey == next.accountKey && snapshot.resourceKey == next.resourceKey &&
            snapshot.state in setOf("paused", "error")
        stopCore()
        if (!continueTraffic) trafficLedger.reset()
        request = next
        transport = next.transport
        deadline = next.deadline
        ticket = command
        resumeOnNetwork = true
        update(snapshot.copy(state = "connecting", requestId = next.requestId,
            resourceKey = next.resourceKey, accountKey = next.accountKey,
            routingMode = next.transport.routingMode,
            errorCode = null, observedExitIp = null, observationState = "unknown"))
        return snapshot
    }

    fun requireResourceBinding(runtimeId: String, requestId: String, accountKey: String, resourceKey: String) {
        if (runtimeId != snapshot.runtimeId || requestId != snapshot.requestId ||
            accountKey != snapshot.accountKey || resourceKey != snapshot.resourceKey ||
            transport == null || ticket != latestCommand()) throw VpnFailure("STALE_RESOURCE_UPDATE")
    }

    fun updateResource(runtimeId: String, next: VpnRequest): VpnSnapshot {
        requireResourceBinding(runtimeId, next.requestId, next.accountKey, next.resourceKey)
        if (!transport!!.matches(next.transport)) return pause("CONFIG_CHANGED")
        // Only an authoritative update changes this lease. Network reconnection reuses it.
        request = next
        deadline = next.deadline
        if (expired()) return pause("RESOURCE_EXPIRED")
        // Preserve paused state/resume eligibility; updating a lease never starts a core.
        update(snapshot)
        return snapshot
    }

    fun start(command: Long) {
        if (!resumeOnNetwork) return
        val config = request ?: return
        if (command != ticket || command != latestCommand()) return
        if (expired()) { pause("RESOURCE_EXPIRED"); return }
        if (!backend.hasNetwork()) { networkPause(); return }
        val boundRevision = backend.networkRevision()
        try {
            if (tun == null) {
                tun = backend.openTun()
                update(snapshot.copy(tunActive = true))
            }
            // Never relinquish the original descriptor. Go owns only its dup().
            val valid = AtomicBoolean(true)
            coreValid = valid
            val started = backend.start(config, tun!!) {
                // Read the volatile lease BEFORE the final validity flag. Pause invalidates
                // that flag before clearing the deadline, so null cannot reopen the gate.
                val currentDeadline = deadline
                (currentDeadline == null || now() < currentDeadline) && backend.hasNetwork() &&
                    backend.networkRevision() == boundRevision && command == latestCommand() && valid.get()
            }
            core = started
            trafficLedger.attach()
            if (command != latestCommand()) { stopCore(); return }
            if (expired()) { pause("RESOURCE_EXPIRED"); return }
            if (!backend.hasNetwork() || boundRevision != backend.networkRevision()) {
                networkPause(); return
            }
            networkRevision = boundRevision
            update(snapshot.copy(state = "connected", errorCode = null,
                observedExitIp = null, observationState = "unknown"))
        } catch (failure: Exception) {
            if (command != latestCommand()) return
            when {
                expired() -> pause("RESOURCE_EXPIRED")
                !backend.hasNetwork() || boundRevision != backend.networkRevision() -> networkPause()
                else -> pause(if (failure is VpnFailure) failure.code else "NATIVE_START_FAILED")
            }
        }
    }

    fun pause(reason: String, command: Long? = null): VpnSnapshot {
        if (snapshot.state == "idle") return snapshot
        coreValid?.set(false)
        // A completed pause is now the current command, while the old core's token stays
        // invalid. Matching refreshes may update its lease without resuming the session.
        if (command != null) ticket = command
        resumeOnNetwork = false
        request = null // A manual/resource/native pause cannot be resurrected by a callback.
        deadline = null
        try { stopCore() } catch (failure: VpnFailure) { throw failure }
        if (reason in setOf("RESOURCE_EXPIRED", "RESOURCE_REVOKED", "SESSION_REVOKED", "CONFIG_CHANGED")) trafficLedger.reset()
        backend.clearUnderlyingNetwork()
        update(snapshot.copy(state = if (tun != null) "paused" else "error", errorCode = reason,
            observedExitIp = null, observationState = "unknown"))
        return snapshot
    }

    fun disconnect(): VpnSnapshot {
        resumeOnNetwork = false
        request = null
        stopCore() // Preserve handles and account identity on failure, so logout can retry.
        try { tun?.close() } catch (_: Exception) { cleanupFailed() }
        tun = null
        trafficLedger.reset()
        transport = null
        deadline = null
        backend.clearUnderlyingNetwork()
        update(VpnSnapshot(runtimeId = snapshot.runtimeId, generation = snapshot.generation))
        return snapshot
    }

    fun revoked() = releaseForServiceStop(revoked = true)
    fun destroyed() = releaseForServiceStop(revoked = false)

    private fun releaseForServiceStop(revoked: Boolean) {
        coreValid?.set(false)
        resumeOnNetwork = false
        request = null
        transport = null
        deadline = null
        // Service destruction/revocation must attempt BOTH releases, even on a core error.
        var failed = false
        try { stopCore() } catch (_: Exception) { failed = true }
        try { tun?.close(); tun = null } catch (_: Exception) { failed = true }
        try { backend.clearUnderlyingNetwork() } catch (_: Exception) { failed = true }
        if (!failed || revoked) trafficLedger.reset()
        if (!failed && !revoked) {
            update(VpnSnapshot(runtimeId = snapshot.runtimeId, generation = snapshot.generation))
        } else {
            update(snapshot.copy(state = "error", tunActive = !revoked && (tun != null || core != null),
                errorCode = if (failed) "CLEANUP_FAILED" else "VPN_REVOKED",
                observedExitIp = null, observationState = "unknown"))
        }
    }

    fun networkChanged() {
        if (!resumeOnNetwork || request == null || ticket != latestCommand()) return
        if (expired()) { pause("RESOURCE_EXPIRED"); return }
        if (core != null && backend.hasNetwork() && networkRevision == backend.networkRevision()) return
        networkPause()
        if (backend.hasNetwork()) {
            update(snapshot.copy(state = "connecting", errorCode = null))
            start(ticket)
        }
    }

    fun expire() {
        if (snapshot.state != "idle" && expired()) pause("RESOURCE_EXPIRED")
    }

    fun observeExit(): VpnSnapshot {
        val session = core
        if (session == null || snapshot.state != "connected" || snapshot.observationState == "checking") return snapshot
        if (expired()) { pause("RESOURCE_EXPIRED"); return snapshot }
        val operation = ++probeRevision
        val command = ticket
        update(snapshot.copy(observationState = "checking", observedExitIp = null, errorCode = null))
        try {
            probe {
                val result = try { session.observeExit().takeIf { isIpv4(it) } } catch (_: Exception) { null }
                dispatch {
                    if (core !== session || operation != probeRevision || command != latestCommand() || snapshot.state != "connected") return@dispatch
                    if (expired()) { pause("RESOURCE_EXPIRED"); return@dispatch }
                    update(snapshot.copy(observedExitIp = result,
                        observationState = if (result == null) "failed" else "available",
                        errorCode = if (result == null) "EXIT_PROBE_FAILED" else null))
                }
            }
        } catch (_: Exception) {
            update(snapshot.copy(observationState = "failed", errorCode = "EXIT_PROBE_FAILED"))
        }
        return snapshot
    }

    private fun networkPause() {
        stopCore()
        backend.clearUnderlyingNetwork()
        update(snapshot.copy(state = if (tun != null) "paused" else "connecting", errorCode = "NETWORK_UNAVAILABLE",
            observedExitIp = null, observationState = "unknown"))
    }

    private fun stopCore() {
        coreValid?.set(false)
        probeRevision++
        try { core?.close() } catch (_: Exception) { cleanupFailed() }
        core?.let { closed -> trafficLedger.detach { closed.traffic() } }
        core = null
        coreValid = null
    }

    private fun cleanupFailed(): Nothing {
        resumeOnNetwork = false
        request = null
        update(snapshot.copy(state = "error", errorCode = "CLEANUP_FAILED",
            observedExitIp = null, observationState = "unknown"))
        throw VpnFailure("CLEANUP_FAILED")
    }

    private fun expired(): Boolean = deadline?.let { now() >= it } ?: false

    /** Read-only statistics never change VPN qualification or publish control events. */
    fun traffic(): Map<String, Any?> = trafficLedger.sample(snapshot, now(), core != null,
        snapshot.state == "connected" && core != null && !expired()) { core?.traffic() }
    private fun update(value: VpnSnapshot) {
        snapshot = value.copy(generation = snapshot.generation + 1)
        publish(snapshot)
    }
    private fun isIpv4(value: String): Boolean {
        val parts = value.split('.')
        return parts.size == 4 && parts.all { part -> part.isNotEmpty() && part.length <= 3 &&
            part.all { it in '0'..'9' } && part.toInt() in 0..255 }
    }
}
