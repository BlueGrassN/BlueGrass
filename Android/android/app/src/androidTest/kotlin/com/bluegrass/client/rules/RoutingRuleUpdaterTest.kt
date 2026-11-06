package com.bluegrass.client.rules

import android.content.Context
import android.content.ContextWrapper
import android.os.Handler
import android.os.Looper
import android.test.InstrumentationTestCase
import org.json.JSONObject
import java.io.File
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executor
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

/** No real download and no change to Go's process-global configuration. */
@Suppress("DEPRECATION")
class RoutingRuleUpdaterTest : InstrumentationTestCase() {
    private lateinit var directory: File
    private lateinit var context: Context
    private val control = Executors.newSingleThreadScheduledExecutor()
    private val updates = Executors.newSingleThreadExecutor()
    private val reads = Executors.newSingleThreadExecutor()
    private val main = Handler(Looper.getMainLooper())

    override fun setUp() {
        super.setUp()
        directory = File(instrumentation.targetContext.noBackupFilesDir, "rules-test-${UUID.randomUUID()}")
        context = object : ContextWrapper(instrumentation.targetContext) {
            override fun getApplicationContext(): Context = this
            override fun getNoBackupFilesDir(): File = directory
        }
    }

    override fun tearDown() {
        updates.shutdownNow()
        reads.shutdownNow()
        updates.awaitTermination(5, TimeUnit.SECONDS)
        reads.awaitTermination(5, TimeUnit.SECONDS)
        control.shutdownNow()
        control.awaitTermination(5, TimeUnit.SECONDS)
        directory.deleteRecursively()
        super.tearDown()
    }

    private fun updater(native: FakeNative, updateWorker: Executor = updates) = RoutingRuleUpdater(
        context, native, control, updateWorker, reads, { main.post(it) },
    )

    fun testConfigurationIsPrivateIdempotentAndPrecedesUpdate() {
        val native = FakeNative()
        val updater = updater(native)
        val result = awaitReply { updater.update(false, it) }
        assertEquals(File(directory, "routing-rules").absolutePath, native.path)
        assertTrue(File(native.path!!).isDirectory)
        assertEquals(listOf(false), native.forces)
        assertEquals("202610232206", JSONObject(result.json).getString("version"))
        assertFalse(result.retry)
        assertTrue(updater.prepareForVpn())
        assertEquals(1, native.configures.get())
    }

    fun testBlockedDownloadDoesNotBlockMainOrStatusAndCannotOverlap() {
        val native = FakeNative().apply { release = CountDownLatch(1) }
        val updater = updater(native)
        val completed = CountDownLatch(1)
        instrumentation.runOnMainSync { updater.update(true) { completed.countDown() } }
        assertTrue(native.entered.await(5, TimeUnit.SECONDS))
        val status = awaitReply { updater.status(it) }
        assertTrue(JSONObject(status.json).getBoolean("updating"))
        assertTrue(awaitReply { updater.update(false, it) }.retry)
        assertEquals(listOf(true), native.forces)
        val mainResponsive = CountDownLatch(1)
        main.post { mainResponsive.countDown() }
        assertTrue(mainResponsive.await(1, TimeUnit.SECONDS))
        native.release!!.countDown()
        assertTrue(completed.await(5, TimeUnit.SECONDS))
        assertFalse(native.calledOnMain)
    }

    fun testCancellationBeforeWorkerStartsDoesNotCancelAnotherUpdate() {
        val queued = AtomicReference<Runnable>()
        val scheduled = CountDownLatch(1)
        val native = FakeNative()
        val updater = updater(native, Executor { queued.set(it); scheduled.countDown() })
        val ticket = updater.update(false) { fail("Stopped job must not reply") }
        assertTrue(scheduled.await(5, TimeUnit.SECONDS))
        updater.cancel(ticket)
        queued.get().run()
        barrier()
        assertEquals(0, native.forces.size)
        assertEquals(0, native.cancels.get())
    }

    fun testStoppedJobCancelsItsDownloadAndOldStopCannotCancelLaterManualUpdate() {
        val native = FakeNative().apply { release = CountDownLatch(1) }
        val updater = updater(native)
        val old = updater.update(false) { fail("Stopped job must not reply") }
        assertTrue(native.entered.await(5, TimeUnit.SECONDS))
        updater.cancel(old)
        assertTrue(native.cancelled.await(5, TimeUnit.SECONDS))
        native.release!!.countDown()
        assertTrue(native.returned.await(5, TimeUnit.SECONDS))
        // Wait for the worker's completion hand-off before creating the next operation.
        updates.submit {}.get(5, TimeUnit.SECONDS)
        barrier()
        val before = native.cancels.get()
        native.entered = CountDownLatch(1)
        native.release = CountDownLatch(1)
        val done = CountDownLatch(1)
        updater.update(true) { done.countDown() }
        assertTrue(native.entered.await(5, TimeUnit.SECONDS))
        updater.cancel(old)
        barrier()
        assertEquals(before, native.cancels.get())
        native.release!!.countDown()
        assertTrue(done.await(5, TimeUnit.SECONDS))
    }

    fun testStopBeforeNativeInstallsCancellationIsRetriedUntilThatCallStops() {
        val native = FakeNative().apply {
            install = CountDownLatch(1)
            release = CountDownLatch(1)
            cancelReleases = true
        }
        val updater = updater(native)
        val replies = AtomicInteger()
        val ticket = updater.update(false) { replies.incrementAndGet() }
        assertTrue(native.entered.await(5, TimeUnit.SECONDS))
        updater.cancel(ticket)
        assertTrue(native.cancelled.await(5, TimeUnit.SECONDS)) // This first cancel has no native target yet.
        native.install!!.countDown()
        assertTrue(native.effectiveCancellation.await(5, TimeUnit.SECONDS))
        updates.submit {}.get(5, TimeUnit.SECONDS)
        barrier()
        instrumentation.waitForIdleSync()
        assertTrue(native.cancels.get() >= 2)
        assertEquals(0, replies.get())
    }

    fun testJobThatOnlyObservedBusyUpdateDoesNotOwnItsCancellation() {
        val native = FakeNative().apply { release = CountDownLatch(1) }
        val updater = updater(native)
        val finished = CountDownLatch(1)
        updater.update(true) { finished.countDown() }
        assertTrue(native.entered.await(5, TimeUnit.SECONDS))
        val observed = CountDownLatch(1)
        val job = updater.update(false) { assertTrue(it.retry); observed.countDown() }
        assertTrue(observed.await(5, TimeUnit.SECONDS))
        updater.cancel(job)
        barrier()
        assertEquals(0, native.cancels.get())
        native.release!!.countDown()
        assertTrue(finished.await(5, TimeUnit.SECONDS))
    }

    fun testConfigurationFailurePreservesVpnFallbackAndCanRecover() {
        val native = FakeNative().apply { configureFails = true }
        val updater = updater(native)
        assertFalse(updater.prepareForVpn()) // Callers may still start the bundled VPN core.
        val result = awaitReply { updater.update(true, it) }
        assertEquals("RULES_CONFIG_FAILED", JSONObject(result.json).getString("lastError"))
        assertTrue(result.retry)
        assertEquals("bundled", JSONObject(result.json).getString("source"))
        assertEquals(0, native.forces.size)
        native.configureFails = false
        assertTrue(updater.prepareForVpn())
        assertFalse(awaitReply { updater.update(true, it) }.retry)
    }

    fun testNativeFixedErrorsRetryAndExceptionsNeverEscapeAsDetails() {
        val native = FakeNative()
        val updater = updater(native)
        for (code in listOf("UPDATE_FAILED", "UPDATE_CANCELLED", "CACHE_INVALID")) {
            native.error = code
            val result = awaitReply { updater.update(false, it) }
            assertEquals(code, JSONObject(result.json).getString("lastError"))
            assertTrue(result.retry)
        }
        native.updateFails = true
        val failed = awaitReply { updater.update(true, it) }
        assertEquals("RULES_UPDATE_FAILED", JSONObject(failed.json).getString("lastError"))
        assertFalse(failed.json.contains("fixture-secret"))
        native.rawStatus = "fixture-secret-invalid-json"
        val invalid = awaitReply { updater.status(it) }
        assertEquals("RULES_UNAVAILABLE", JSONObject(invalid.json).getString("lastError"))
        assertFalse(invalid.json.contains("fixture-secret"))
    }

    fun testStatusWhitelistsFieldsAndErrorCodes() {
        val native = FakeNative().apply {
            rawStatus = JSONObject(json()).put("password", "fixture-secret")
                .put("lastError", "fixture-secret").toString()
        }
        val result = awaitReply { updater(native).status(it) }
        assertEquals("RULES_UNAVAILABLE", JSONObject(result.json).getString("lastError"))
        assertFalse(result.json.contains("fixture-secret"))
    }

    fun testDownloadedStatusRetainsVersionAndTimestampsWithoutUnknownFields() {
        val native = FakeNative().apply {
            rawStatus = JSONObject(json()).put("source", "downloaded")
                .put("version", "202610241530")
                .put("checkedAt", "2026-10-24T15:30:01Z")
                .put("updatedAt", "2026-10-24T15:29:59.123456789Z")
                .put("password", "fixture-secret").toString()
        }
        val result = awaitReply { updater(native).status(it) }
        val status = JSONObject(result.json)
        assertEquals("downloaded", status.getString("source"))
        assertEquals("202610241530", status.getString("version"))
        assertEquals("2026-10-24T15:30:01Z", status.getString("checkedAt"))
        assertEquals("2026-10-24T15:29:59.123456789Z", status.getString("updatedAt"))
        assertFalse(status.has("password"))
        assertFalse(result.retry)
    }

    private fun barrier() { control.submit {}.get(5, TimeUnit.SECONDS) }

    private fun awaitReply(start: ((RoutingRuleResult) -> Unit) -> Unit): RoutingRuleResult {
        val done = CountDownLatch(1)
        val result = AtomicReference<RoutingRuleResult>()
        start { result.set(it); done.countDown() }
        assertTrue("Reply did not arrive", done.await(5, TimeUnit.SECONDS))
        return result.get()
    }

    internal class FakeNative : RoutingRulesNative {
        @Volatile var path: String? = null
        val configures = AtomicInteger()
        val cancels = AtomicInteger()
        val forces = java.util.Collections.synchronizedList(mutableListOf<Boolean>())
        @Volatile var calledOnMain = false
        @Volatile var configureFails = false
        @Volatile var updateFails = false
        @Volatile var rawStatus: String? = null
        @Volatile var error = ""
        @Volatile var entered = CountDownLatch(1)
        @Volatile var release: CountDownLatch? = null
        @Volatile var install: CountDownLatch? = null
        @Volatile var cancelReleases = false
        val returned = CountDownLatch(1)
        val cancelled = CountDownLatch(1)
        val effectiveCancellation = CountDownLatch(1)
        @Volatile private var updating = false

        private fun recordThread() { if (Looper.myLooper() == Looper.getMainLooper()) calledOnMain = true }
        override fun configure(directory: String) {
            recordThread()
            configures.incrementAndGet()
            if (configureFails) throw IllegalStateException("fixture-secret")
            path = directory
        }
        override fun status(): String { recordThread(); return rawStatus ?: json(updating, error) }
        override fun update(force: Boolean): String {
            recordThread()
            check(path != null)
            forces.add(force)
            entered.countDown()
            try {
                install?.await(10, TimeUnit.SECONDS)
                updating = true
                release?.await(10, TimeUnit.SECONDS)
                if (updateFails) throw IllegalStateException("fixture-secret")
                return json(false, error)
            } finally { updating = false; returned.countDown() }
        }
        override fun cancel() {
            recordThread()
            cancels.incrementAndGet()
            if (updating) {
                effectiveCancellation.countDown()
                if (cancelReleases) release?.countDown()
            }
            cancelled.countDown()
        }
    }

    companion object {
        fun json(updating: Boolean = false, error: String = "") = JSONObject()
            .put("version", "202610232206").put("source", "bundled")
            .put("checkedAt", "").put("updatedAt", "")
            .put("updating", updating).put("lastError", error).toString()
    }
}
