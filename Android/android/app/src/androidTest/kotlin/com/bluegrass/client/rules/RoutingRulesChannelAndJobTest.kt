package com.bluegrass.client.rules

import android.content.Context
import android.content.ContextWrapper
import android.os.Handler
import android.os.Looper
import android.test.InstrumentationTestCase
import io.flutter.plugin.common.BinaryMessenger
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.StandardMethodCodec
import org.json.JSONObject
import java.io.File
import java.nio.ByteBuffer
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

@Suppress("DEPRECATION")
class RoutingRulesChannelAndJobTest : InstrumentationTestCase() {
    private val control = Executors.newSingleThreadScheduledExecutor()
    private val updates = Executors.newSingleThreadExecutor()
    private val reads = Executors.newSingleThreadExecutor()
    private val native = RoutingRuleUpdaterTest.FakeNative()
    private val main = Handler(Looper.getMainLooper())
    private lateinit var updater: RoutingRuleUpdater
    private lateinit var directory: File
    private var channel: RoutingRulesChannel? = null
    private var jobs: RoutingRuleJobRuns? = null

    override fun setUp() {
        super.setUp()
        directory = File(instrumentation.targetContext.noBackupFilesDir, "rules-channel-test-${UUID.randomUUID()}")
        val context = object : ContextWrapper(instrumentation.targetContext) {
            override fun getApplicationContext(): Context = this
            override fun getNoBackupFilesDir(): File = directory
        }
        updater = RoutingRuleUpdater(context, native, control, updates, reads, { main.post(it) })
    }

    override fun tearDown() {
        instrumentation.runOnMainSync { channel?.detach(); jobs?.destroy() }
        native.release?.countDown()
        updates.shutdown()
        reads.shutdown()
        updates.awaitTermination(5, TimeUnit.SECONDS)
        reads.awaitTermination(5, TimeUnit.SECONDS)
        control.shutdownNow()
        control.awaitTermination(5, TimeUnit.SECONDS)
        directory.deleteRecursively()
        super.tearDown()
    }

    fun testActualMethodChannelReturnsJsonStringAndManualUpdateAlwaysForces() {
        val messenger = Messenger()
        val observed = AtomicInteger()
        instrumentation.runOnMainSync { channel = RoutingRulesChannel(messenger, updater) { observed.incrementAndGet() } }
        val status = call(messenger, "status")
        assertTrue(status is String)
        assertEquals("bundled", JSONObject(status as String).getString("source"))
        assertEquals(0, native.forces.size)
        val updated = call(messenger, "update", mapOf("force" to false, "directory" to "fixture-secret"))
        assertTrue(updated is String)
        assertEquals(listOf(true), native.forces)
        assertEquals(1, observed.get())
        assertFalse(native.path!!.contains("fixture-secret"))
        assertFalse(native.calledOnMain)
    }

    fun testDetachSuppressesLateRepliesButAcceptedManualUpdateFinishes() {
        val messenger = Messenger()
        native.release = CountDownLatch(1)
        val replies = AtomicInteger()
        val completed = CountDownLatch(1)
        instrumentation.runOnMainSync {
            channel = RoutingRulesChannel(messenger, updater) { completed.countDown() }
            messenger.invoke("update") { replies.incrementAndGet() }
        }
        assertTrue(native.entered.await(5, TimeUnit.SECONDS))
        instrumentation.runOnMainSync { channel!!.detach() }
        assertNull(messenger.handler)
        assertEquals(0, native.cancels.get())
        native.release!!.countDown()
        assertTrue(completed.await(5, TimeUnit.SECONDS))
        instrumentation.waitForIdleSync()
        assertEquals(0, replies.get())
        // A new Activity may inspect the process-owned result normally.
        instrumentation.runOnMainSync { channel = RoutingRulesChannel(messenger, updater) {} }
        assertFalse(JSONObject(call(messenger, "status") as String).getBoolean("updating"))
    }

    fun testStoppedJobCannotFinishOrCancelAReplacementJob() {
        native.release = CountDownLatch(1)
        val old = Any()
        val next = Any()
        val completed = AtomicInteger()
        val nextDone = CountDownLatch(1)
        instrumentation.runOnMainSync {
            jobs = RoutingRuleJobRuns(updater) { key, _ ->
                assertSame(next, key)
                completed.incrementAndGet()
                nextDone.countDown()
            }
            jobs!!.start(old)
        }
        assertTrue(native.entered.await(5, TimeUnit.SECONDS))
        instrumentation.runOnMainSync { assertTrue(jobs!!.stop(old)) }
        assertTrue(native.cancelled.await(5, TimeUnit.SECONDS))
        native.release!!.countDown()
        drainWorkers()
        assertEquals(0, completed.get())
        val cancels = native.cancels.get()
        native.entered = CountDownLatch(1)
        native.release = CountDownLatch(1)
        instrumentation.runOnMainSync { jobs!!.start(next) }
        assertTrue(native.entered.await(5, TimeUnit.SECONDS))
        instrumentation.runOnMainSync { assertFalse(jobs!!.stop(old)) }
        control.submit {}.get(5, TimeUnit.SECONDS)
        assertEquals(cancels, native.cancels.get())
        native.release!!.countDown()
        assertTrue(nextDone.await(5, TimeUnit.SECONDS))
        assertEquals(1, completed.get())
        assertEquals(listOf(false, false), native.forces)
    }

    fun testJobReportsNativeFailureAsRetryAndSuccessWithoutRetry() {
        for (code in listOf("UPDATE_FAILED", "UPDATE_CANCELLED", "CACHE_INVALID", "")) {
            native.error = code
            val finished = CountDownLatch(1)
            val retry = AtomicReference<Boolean>()
            instrumentation.runOnMainSync {
                jobs = RoutingRuleJobRuns(updater) { _, again -> retry.set(again); finished.countDown() }
                jobs!!.start(Any())
            }
            assertTrue(finished.await(5, TimeUnit.SECONDS))
            assertEquals(code.isNotEmpty(), retry.get())
        }
    }

    fun testDestroySuppressesAllJobCallbacksAndCancelsOnlyOwnedWork() {
        native.release = CountDownLatch(1)
        val completed = AtomicInteger()
        instrumentation.runOnMainSync {
            jobs = RoutingRuleJobRuns(updater) { _, _ -> completed.incrementAndGet() }
            jobs!!.start(Any())
        }
        assertTrue(native.entered.await(5, TimeUnit.SECONDS))
        instrumentation.runOnMainSync { jobs!!.destroy() }
        assertTrue(native.cancelled.await(5, TimeUnit.SECONDS))
        native.release!!.countDown()
        drainWorkers()
        assertEquals(0, completed.get())
    }

    private fun drainWorkers() {
        updates.submit {}.get(5, TimeUnit.SECONDS)
        reads.submit {}.get(5, TimeUnit.SECONDS)
        control.submit {}.get(5, TimeUnit.SECONDS)
        instrumentation.waitForIdleSync()
    }

    private fun call(messenger: Messenger, method: String, args: Any? = null): Any? {
        val response = AtomicReference<Any?>()
        val replied = CountDownLatch(1)
        instrumentation.runOnMainSync {
            messenger.invoke(method, args) { value -> response.set(value); replied.countDown() }
        }
        assertTrue(replied.await(5, TimeUnit.SECONDS))
        return response.get()
    }

    private class Messenger : BinaryMessenger {
        var handler: BinaryMessenger.BinaryMessageHandler? = null
        override fun send(channel: String, message: ByteBuffer?) = error("Unexpected outbound message")
        override fun send(channel: String, message: ByteBuffer?, callback: BinaryMessenger.BinaryReply?) =
            error("Unexpected outbound message")
        override fun setMessageHandler(channel: String, handler: BinaryMessenger.BinaryMessageHandler?) {
            check(channel == "bluegrass/routing-rules")
            this.handler = handler
        }
        fun invoke(method: String, arguments: Any? = null, complete: (Any?) -> Unit) {
            val codec = StandardMethodCodec.INSTANCE
            val message = codec.encodeMethodCall(MethodCall(method, arguments)).apply { flip() }
            handler!!.onMessage(message) { response ->
                response?.flip()
                complete(if (response == null) null else codec.decodeEnvelope(response))
            }
        }
    }
}
