package com.bluegrass.client.vpn

import android.content.Context
import android.annotation.SuppressLint
import android.content.Intent
import android.net.VpnService
import android.os.Handler
import android.os.HandlerThread
import android.os.Looper
import android.os.SystemClock
import com.bluegrass.core.bridge.Bridge
import com.bluegrass.client.rules.RoutingRulesRuntime
import java.util.ArrayDeque
import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.ThreadPoolExecutor
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicLong

/** One process, one controller, one lifecycle worker. No saved requests and no boot restart. */
internal object VpnRuntime {
    val main = Handler(Looper.getMainLooper())
    private val thread = HandlerThread("bluegrass-vpn-lifecycle").apply { start() }
    val worker = Handler(thread.looper)
    private val latestCommand = AtomicLong()
    private val lock = Any()
    private val pending = ArrayDeque<Command>()
    private var desiredAccount: String? = null
    @Volatile private var owner: BlueGrassVpnService? = null // written only on lifecycle worker
    // Owned only while the service is attached; cleared on the serialized onDestroy path.
    @SuppressLint("StaticFieldLeak")
    private var backend: AndroidVpnBackend? = null
    private val observers = linkedMapOf<(VpnSnapshot) -> Unit, Long>() // main only
    private val probes = ThreadPoolExecutor(1, 1, 0, TimeUnit.MILLISECONDS,
        ArrayBlockingQueue<Runnable>(1), { Thread(it, "bluegrass-vpn-probe") }, ThreadPoolExecutor.AbortPolicy())
    private val delegate = object : VpnBackend {
        override fun hasNetwork() = backend?.hasNetwork() == true
        override fun networkRevision() = backend?.networkRevision() ?: -1
        override fun openTun() = (backend ?: throw VpnFailure("SERVICE_UNAVAILABLE")).openTun()
        override fun start(request: VpnRequest, tun: VpnTunnel, valid: () -> Boolean): VpnCore {
            val current = backend ?: throw VpnFailure("SERVICE_UNAVAILABLE")
            val service = owner ?: throw VpnFailure("SERVICE_UNAVAILABLE")
            RoutingRulesRuntime.prepareForVpn(service)
            return current.start(request, tun, valid)
        }
        override fun clearUnderlyingNetwork() { backend?.clearUnderlyingNetwork() }
    }
    private val controller = VpnController(delegate, SystemClock::elapsedRealtime, latestCommand::get,
        ::publish, { probes.execute(it) }, { work -> worker.post { safely(work) } })
    @Volatile var snapshot: VpnSnapshot = controller.snapshot
        private set
    private val expiry = Runnable { safely { controller.expire() }; scheduleExpiry() }

    private class Command(
        val method: String, val ticket: Long, val request: VpnRequest? = null,
        val reason: String = "PAUSED", val resourceUpdate: VpnResourceUpdateInput? = null,
        val reply: (VpnSnapshot?, String?) -> Unit,
    )

    /** Called from the visible Activity. validate() is bounded, local, and has no network IO. */
    fun connect(context: Context, arguments: Any?, reply: (VpnSnapshot?, String?) -> Unit) {
        try {
            if (VpnService.prepare(context) != null) throw VpnFailure("VPN_PERMISSION_REQUIRED")
            val request = VpnRequestParser.parse(arguments, SystemClock.elapsedRealtime())
            try {
                go.Seq.setContext(context.applicationContext)
                Bridge.validate(request.config)
            }
            catch (_: Exception) { throw VpnFailure("INVALID_CONFIG") }
            catch (_: LinkageError) { throw VpnFailure("NATIVE_UNAVAILABLE") }
            synchronized(lock) {
                if (pending.size >= 32) throw VpnFailure("COMMAND_QUEUE_FULL")
                if (desiredAccount != null && desiredAccount != request.accountKey) throw VpnFailure("ACCOUNT_MISMATCH")
                // Only this non-secret action crosses a process/system boundary.
                context.startForegroundService(Intent(context, BlueGrassVpnService::class.java)
                    .setAction(BlueGrassVpnService.ACTION_RUN))
                desiredAccount = request.accountKey
                val command = Command("connect", latestCommand.incrementAndGet(), request, reply = reply)
                pending.addLast(command)
                // A failed system service launch cannot retain a secret request indefinitely.
                worker.postDelayed({
                    val removed = synchronized(lock) { pending.remove(command) }
                    if (removed) {
                        latestCommand.compareAndSet(command.ticket, controller.commandTicket)
                        replyOnMain(command, null, "SERVICE_UNAVAILABLE")
                        releaseAccountIfIdle()
                    }
                }, 15_000)
            }
            worker.post(::drain)
        } catch (failure: VpnFailure) { reply(null, failure.code) }
        catch (_: Exception) { reply(null, "SERVICE_START_FAILED") }
    }

    fun command(method: String, reason: String = "PAUSED", reply: (VpnSnapshot?, String?) -> Unit) {
        synchronized(lock) {
            if (pending.size >= 32) { main.post { reply(null, "COMMAND_QUEUE_FULL") }; return }
            val ticket = if (method == "pause" || method == "disconnect") latestCommand.incrementAndGet() else latestCommand.get()
            pending.addLast(Command(method, ticket, reason = reason, reply = reply))
        }
        worker.post(::drain)
    }

    /** Explicit API-authoritative refresh; never synthesized by a network callback. */
    fun updateResource(arguments: Any?, reply: (VpnSnapshot?, String?) -> Unit) {
        try {
            val input = VpnRequestParser.readUpdate(arguments, SystemClock.elapsedRealtime())
            synchronized(lock) {
                if (pending.size >= 32) throw VpnFailure("COMMAND_QUEUE_FULL")
                // A lease renewal must not invalidate the running core's command identity.
                pending.addLast(Command("updateResource", latestCommand.get(), resourceUpdate = input, reply = reply))
            }
            worker.post(::drain)
        } catch (failure: VpnFailure) { reply(null, failure.code) }
    }

    fun attach(service: BlueGrassVpnService, value: AndroidVpnBackend) {
        worker.post {
            owner = service
            backend = value
            drain()
            main.post { service.render(snapshot) }
            finishWhenIdle(service)
        }
    }

    fun networkChanged(service: BlueGrassVpnService) {
        worker.post { if (owner === service) safely { controller.networkChanged() } }
    }

    fun revoke(service: BlueGrassVpnService) {
        // onRevoke may run on a Binder thread. Never close a session or FD there.
        val invalidated = owner === service
        if (invalidated) latestCommand.incrementAndGet()
        worker.post {
            if (owner !== service) return@post
            if (!invalidated) latestCommand.incrementAndGet()
            safely { controller.revoked() }
            drain()
            finishWhenIdle(service)
        }
    }

    fun detach(service: BlueGrassVpnService) {
        worker.post {
            if (owner !== service) return@post
            safely { controller.destroyed() }
            owner = null
            backend = null
            releaseAccountIfIdle()
        }
    }

    fun serviceFailed(code: String) {
        worker.post {
            val commands = synchronized(lock) { pending.toList().also { pending.clear() } }
            for (command in commands) replyOnMain(command, null, code)
            releaseAccountIfIdle()
        }
    }

    fun listen(observer: (VpnSnapshot) -> Unit) {
        val current = snapshot
        observers[observer] = current.generation
        observer(current)
    }
    fun unlisten(observer: (VpnSnapshot) -> Unit) { observers.remove(observer) }
    fun traffic(reply: (Map<String, Any?>?, String?) -> Unit) {
        worker.post {
            val value = try { controller.traffic() } catch (_: Exception) { null }
            main.post { reply(value, if (value == null) "TRAFFIC_UNAVAILABLE" else null) }
        }
    }
    fun expire() { worker.post { safely { controller.expire() } } }

    fun finishWhenIdle(service: BlueGrassVpnService) {
        val epoch = latestCommand.get()
        main.post {
            if (latestCommand.get() == epoch && canStopService() &&
                synchronized(lock) { pending.isEmpty() }) service.finishService()
        }
    }

    private fun canStopService() = snapshot.state == "idle" ||
        (snapshot.state == "error" && !snapshot.tunActive)

    private fun drain() {
        while (true) {
            val command = synchronized(lock) {
                val first = pending.peekFirst() ?: return
                if (first.method == "connect" && first.ticket == latestCommand.get() && owner == null) return
                pending.removeFirst()
            }
            try {
                val result = when (command.method) {
                    "connect" -> {
                        if (command.ticket != latestCommand.get()) throw VpnFailure("COMMAND_SUPERSEDED")
                        controller.connect(command.request!!, command.ticket).also {
                            // Reply connecting before a potentially blocking native startup.
                            worker.post { safely { controller.start(command.ticket) } }
                        }
                    }
                    "disconnect" -> controller.disconnect()
                    "pause" -> controller.pause(command.reason, command.ticket)
                    "observeExit" -> controller.observeExit()
                    "updateResource" -> applyResourceUpdate(command.resourceUpdate!!)
                    "status" -> { controller.expire(); controller.snapshot }
                    else -> throw VpnFailure("INVALID_ARGUMENT")
                }
                replyOnMain(command, result, null)
            } catch (failure: Exception) {
                if (command.method == "connect") latestCommand.compareAndSet(command.ticket, controller.commandTicket)
                replyOnMain(command, null, (failure as? VpnFailure)?.code ?: "NATIVE_ERROR")
            }
            releaseAccountIfIdle()
            val service = owner
            if (service != null) finishWhenIdle(service)
        }
    }

    private fun applyResourceUpdate(input: VpnResourceUpdateInput): VpnSnapshot {
        fun verifyBinding() = controller.requireResourceBinding(input.runtimeId, input.requestId, input.accountKey, input.resourceKey)
        verifyBinding()
        val request = try { VpnRequestParser.parseUpdate(input) }
        catch (failure: VpnFailure) {
            // An authoritative already-expired lease cannot leave the old lease running.
            // Recheck cancellation before pausing; an old request must not affect a new one.
            if (failure.code != "RESOURCE_EXPIRED") throw failure
            verifyBinding()
            return controller.pause("RESOURCE_EXPIRED")
        }
        try { Bridge.validate(request.config) }
        catch (_: Exception) { throw VpnFailure("INVALID_CONFIG") }
        catch (_: LinkageError) { throw VpnFailure("NATIVE_UNAVAILABLE") }
        // This rechecks all identities and the current command after parse/native validation.
        return controller.updateResource(input.runtimeId, request)
    }

    private fun publish(value: VpnSnapshot) {
        snapshot = value
        scheduleExpiry()
        val service = owner
        val deadline = controller.deadline
        main.post {
            // A new listener already saw the current snapshot. Do not replay older queued events.
            for (observer in observers.keys.toList()) {
                val previous = observers[observer] ?: continue
                if (value.generation > previous) { observers[observer] = value.generation; observer(value) }
            }
            service?.render(value)
            service?.scheduleExpiry(deadline)
        }
        if (service != null) finishWhenIdle(service)
    }

    private fun scheduleExpiry() {
        worker.removeCallbacks(expiry)
        val deadline = controller.deadline ?: return
        worker.postDelayed(expiry, (deadline - SystemClock.elapsedRealtime()).coerceAtLeast(1))
    }
    private fun releaseAccountIfIdle() {
        synchronized(lock) {
            if (controller.snapshot.accountKey == null && pending.none { it.method == "connect" }) desiredAccount = null
        }
    }
    private fun replyOnMain(command: Command, value: VpnSnapshot?, error: String?) {
        main.post { command.reply(value, error) }
    }
    private fun safely(work: () -> Unit) {
        try { work() }
        catch (_: Exception) { try { controller.pause("NATIVE_ERROR") } catch (_: Exception) { /* retained for cleanup retry */ } }
        catch (_: LinkageError) { try { controller.pause("NATIVE_UNAVAILABLE") } catch (_: Exception) { } }
    }
}
