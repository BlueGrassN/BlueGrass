package com.bluegrass.client.vpn

import junit.framework.TestCase

/** Runs both on the host JVM and with the platform instrumentation runner. */
class VpnControllerTest : TestCase() {
    private var time = 1_000L
    private var revision = 1L
    private lateinit var backend: FakeBackend
    private lateinit var controller: VpnController
    private val probes = mutableListOf<() -> Unit>()
    private val replies = mutableListOf<() -> Unit>()

    override fun setUp() {
        backend = FakeBackend()
        controller = VpnController(backend, { time }, { revision }, {}, { probes.add(it) }, { replies.add(it) })
    }

    private fun request(account: String = "account", deadline: Long? = 11_000,
        resource: String = "resource", requestId: String = "00000000-0000-4000-8000-000000000001",
        config: String = "{fixture}", routingMode: String = "global",
        transport: VpnTransport = VpnTransport("entry.invalid", 443, mapOf("password" to "fixture-secret"), routingMode)) =
        VpnRequest(requestId, resource, account, config, deadline, transport)

    private fun connect() {
        controller.connect(request(), revision)
        assertEquals("connecting", controller.snapshot.state)
        controller.start(revision)
        assertEquals("connected", controller.snapshot.state)
    }

    fun testPauseRetainsOriginalTunAndDisconnectClosesBoth() {
        connect()
        controller.pause("RESOURCE_REVOKED")
        assertEquals(1, backend.core.closes)
        assertEquals(0, backend.tunnel.closes)
        assertTrue(controller.snapshot.tunActive)
        assertEquals("paused", controller.snapshot.state)
        controller.disconnect()
        assertEquals(1, backend.tunnel.closes)
        assertFalse(controller.snapshot.tunActive)
        assertNull(controller.snapshot.accountKey)
        assertEquals("idle", controller.snapshot.state)
    }

    fun testFailedCleanupDoesNotReportIdleOrPermitAccountChange() {
        connect()
        backend.core.failClose = true
        try { controller.disconnect(); fail("cleanup must fail") } catch (_: VpnFailure) { }
        assertTrue(controller.snapshot.tunActive)
        assertEquals("account", controller.snapshot.accountKey)
        try { controller.connect(request("other"), ++revision); fail("account must be stopped first") } catch (_: VpnFailure) { }
        backend.core.failClose = false
        controller.disconnect()
        assertEquals("idle", controller.snapshot.state)
    }

    fun testNativeStartFailureKeepsTunAndRedactsError() {
        backend.failStart = true
        controller.connect(request(), revision)
        controller.start(revision)
        assertEquals("paused", controller.snapshot.state)
        assertEquals("NATIVE_START_FAILED", controller.snapshot.errorCode)
        assertTrue(controller.snapshot.tunActive)
        assertFalse(controller.snapshot.toMap().toString().contains("fixture-secret"))
    }

    fun testFailedPauseInvalidatesNativeCallbacksEvenIfCommandEpochIsRestored() {
        connect()
        assertTrue(backend.valid())
        backend.core.failClose = true
        try { controller.pause("PAUSED"); fail("cleanup must fail") } catch (_: VpnFailure) { }
        assertFalse(backend.valid())
    }

    fun testRevocationAttemptsTunReleaseEvenIfNativeCloseFails() {
        connect()
        backend.core.failClose = true
        controller.revoked()
        assertEquals(1, backend.tunnel.closes)
        assertFalse(controller.snapshot.tunActive)
        assertEquals("CLEANUP_FAILED", controller.snapshot.errorCode)
    }

    fun testServiceDestructionAttemptsBothHandlesOnNativeFailure() {
        connect()
        backend.core.failClose = true
        controller.destroyed()
        assertEquals(1, backend.tunnel.closes)
        assertFalse(backend.valid())
        assertEquals("CLEANUP_FAILED", controller.snapshot.errorCode)
        assertEquals("account", controller.snapshot.accountKey)
    }

    fun testServiceDestructionClearsSuccessfulSession() {
        connect()
        controller.destroyed()
        assertEquals(1, backend.core.closes)
        assertEquals(1, backend.tunnel.closes)
        assertEquals("idle", controller.snapshot.state)
        assertNull(controller.snapshot.accountKey)
    }

    fun testNetworkReconnectCannotExtendDeadline() {
        connect()
        backend.network = false
        backend.networkRevision++
        controller.networkChanged()
        assertEquals("paused", controller.snapshot.state)
        assertEquals(11_000L, controller.deadline)
        time = 5_000
        backend.network = true
        backend.networkRevision++
        controller.networkChanged()
        assertEquals("connected", controller.snapshot.state)
        time = 11_000
        controller.expire()
        assertEquals("RESOURCE_EXPIRED", controller.snapshot.errorCode)
        assertTrue(controller.snapshot.tunActive)
        backend.networkRevision++
        controller.networkChanged()
        assertEquals("paused", controller.snapshot.state)
    }

    fun testManualPauseDoesNotResumeOnNetworkChange() {
        connect()
        controller.pause("PAUSED")
        backend.networkRevision++
        controller.networkChanged()
        assertEquals("paused", controller.snapshot.state)
        assertEquals(1, backend.starts)
    }

    fun testNoInitialNetworkWaitsWithoutClaimingTunProtectionThenResumes() {
        backend.network = false
        controller.connect(request(), revision)
        controller.start(revision)
        assertEquals("connecting", controller.snapshot.state)
        assertFalse(controller.snapshot.tunActive)
        assertEquals(0, backend.starts)
        backend.network = true
        backend.networkRevision++
        controller.networkChanged()
        assertEquals("connected", controller.snapshot.state)
        assertTrue(controller.snapshot.tunActive)
        assertEquals(11_000L, controller.deadline)
    }

    fun testFlutterPauseReasonsAreFixedAndArbitraryTextIsNeverEchoed() {
        for (reason in listOf("RESOURCE_EXPIRED", "RESOURCE_UNAVAILABLE", "ELIGIBILITY_UNCONFIRMED", "SESSION_REVOKED")) {
            assertEquals(reason, VpnPauseReasons.sanitize(reason))
        }
        assertEquals("PAUSED", VpnPauseReasons.sanitize("fixture-secret"))
        assertEquals("PAUSED", VpnPauseReasons.sanitize(null))
        assertEquals("RESOURCE_EXPIRED", VpnPauseReasons.sanitize("expired"))
    }

    fun testDisconnectIsIdempotent() {
        connect()
        controller.disconnect()
        controller.disconnect()
        assertEquals(1, backend.core.closes)
        assertEquals(1, backend.tunnel.closes)
        assertNull(controller.deadline)
    }

    fun testSupersededStartupIsClosedWithoutConnectedPublication() {
        controller.connect(request(), revision)
        backend.duringStart = { revision++ }
        val generation = controller.snapshot.generation
        controller.start(1)
        assertEquals(1, backend.core.closes)
        assertEquals(generation + 1, controller.snapshot.generation) // TUN ownership only
        assertEquals("connecting", controller.snapshot.state)
        controller.disconnect()
        assertEquals("idle", controller.snapshot.state)
    }

    fun testLateProbeCannotOverwriteDisconnectOrNextConnection() {
        connect()
        controller.observeExit()
        assertEquals("checking", controller.snapshot.observationState)
        probes.removeAt(0).invoke()
        revision++
        controller.disconnect()
        controller.connect(request(), revision)
        controller.start(revision)
        val snapshot = controller.snapshot
        replies.removeAt(0).invoke()
        assertEquals(snapshot, controller.snapshot)
        assertNull(controller.snapshot.observedExitIp)
    }

    fun testProbeFailureIsObservationOnlyAndNeverContainsExceptionText() {
        connect()
        backend.core.failProbe = true
        controller.observeExit()
        probes.removeAt(0).invoke()
        replies.removeAt(0).invoke()
        assertEquals("connected", controller.snapshot.state)
        assertEquals("failed", controller.snapshot.observationState)
        assertEquals("EXIT_PROBE_FAILED", controller.snapshot.errorCode)
        assertFalse(controller.snapshot.toMap().toString().contains("fixture-secret"))
    }

    fun testExpiryWhileStartBlockedClosesReturnedSession() {
        controller.connect(request(), revision)
        backend.duringStart = { time = 11_001 }
        controller.start(revision)
        assertEquals(1, backend.core.closes)
        assertEquals("RESOURCE_EXPIRED", controller.snapshot.errorCode)
        assertTrue(controller.snapshot.tunActive)
    }

    fun testSnapshotHasOnlyFixedContractFieldsAndMonotonicGeneration() {
        val initial = controller.snapshot
        connect()
        controller.pause("PAUSED")
        controller.disconnect()
        assertEquals(initial.runtimeId, controller.snapshot.runtimeId)
        assertTrue(controller.snapshot.generation > initial.generation)
        assertEquals(setOf("version", "runtimeId", "generation", "state", "requestId", "resourceKey", "accountKey", "tunActive", "errorCode", "observedExitIp", "observationState", "routingMode"), controller.snapshot.toMap().keys)
    }

    fun testLegacyRequestPublishesGlobalModeForSnapshotRecovery() {
        assertEquals("global", controller.snapshot.toMap()["routingMode"])
        connect()
        assertEquals("global", controller.snapshot.toMap()["routingMode"])
        controller.pause("PAUSED")
        assertEquals("global", controller.snapshot.toMap()["routingMode"])
    }

    fun testRoutingChangePausesInsteadOfSilentlyUpdatingBothActivePolicies() {
        for ((active, changed) in listOf("global" to "bypass_cn", "bypass_cn" to "global")) {
            controller.connect(request(routingMode = active), revision)
            controller.start(revision)
            val core = backend.core
            val valid = backend.valid
            val runtime = controller.snapshot.runtimeId
            val result = controller.updateResource(runtime, request(routingMode = changed))
            assertEquals("CONFIG_CHANGED", result.errorCode)
            assertEquals("paused", result.state)
            assertEquals(active, result.toMap()["routingMode"])
            assertTrue(result.tunActive)
            assertEquals(1, core.closes)
            assertFalse(valid())
            assertFalse(result.toMap().toString().contains("fixture-secret"))
            // Neither the new policy nor a matching renewal resurrects the paused core.
            controller.updateResource(runtime, request(routingMode = active, deadline = 30_000))
            val starts = backend.starts
            backend.networkRevision++
            controller.networkChanged()
            controller.start(revision)
            assertEquals(starts, backend.starts)
            assertEquals("paused", controller.snapshot.state)
            assertEquals(active, controller.snapshot.toMap()["routingMode"])
            controller.disconnect()
        }
    }

    fun testBypassSnapshotRecoveryRetainsActualModeAcrossNetworkAndLeaseUpdates() {
        controller.connect(request(routingMode = "bypass_cn"), revision)
        assertEquals("bypass_cn", controller.snapshot.toMap()["routingMode"])
        controller.start(revision)
        val originalCore = backend.core
        val originalSnapshot = controller.snapshot.toMap()
        assertEquals("bypass_cn", originalSnapshot["routingMode"])
        controller.updateResource(controller.snapshot.runtimeId,
            request(routingMode = "bypass_cn", deadline = 30_000, config = "{renewed}"))
        assertSame(originalCore, backend.core)
        backend.network = false
        backend.networkRevision++
        controller.networkChanged()
        assertEquals("paused", controller.snapshot.state)
        assertEquals("bypass_cn", controller.snapshot.toMap()["routingMode"])
        backend.network = true
        backend.networkRevision++
        controller.networkChanged()
        assertEquals("connected", controller.snapshot.state)
        assertEquals("bypass_cn", backend.lastRoutingMode)
        assertEquals("{renewed}", backend.lastConfig)
        assertEquals("bypass_cn", controller.snapshot.toMap()["routingMode"])
        assertEquals(originalSnapshot["requestId"], controller.snapshot.toMap()["requestId"])
        controller.pause("PAUSED")
        assertEquals("bypass_cn", controller.snapshot.toMap()["routingMode"])
        controller.disconnect()
        assertEquals("global", controller.snapshot.toMap()["routingMode"])
        assertEquals("bypass_cn", originalSnapshot["routingMode"])
    }

    fun testModeSurvivesWaitingForInitialNetworkAndNativeStartFailure() {
        backend.network = false
        controller.connect(request(routingMode = "bypass_cn"), revision)
        controller.start(revision)
        assertFalse(controller.snapshot.tunActive)
        assertEquals("bypass_cn", controller.snapshot.toMap()["routingMode"])
        backend.network = true
        backend.networkRevision++
        backend.failStart = true
        controller.networkChanged()
        assertEquals("NATIVE_START_FAILED", controller.snapshot.errorCode)
        assertEquals("bypass_cn", controller.snapshot.toMap()["routingMode"])
    }

    fun testFailedCleanupCannotPublishReplacementRoutingMode() {
        controller.connect(request(routingMode = "bypass_cn"), revision)
        controller.start(revision)
        backend.core.failClose = true
        try {
            controller.connect(request(routingMode = "global"), ++revision)
            fail("Expected cleanup failure")
        } catch (failure: VpnFailure) { assertEquals("CLEANUP_FAILED", failure.code) }
        assertEquals("bypass_cn", controller.snapshot.toMap()["routingMode"])
        assertFalse(backend.valid())
    }

    fun testUpdateRequiresEveryCurrentBindingIdentityAndCommand() {
        connect()
        val before = controller.snapshot
        val wrong = listOf(
            "wrong-runtime" to request(),
            before.runtimeId to request(account = "other"),
            before.runtimeId to request(resource = "other"),
            before.runtimeId to request(requestId = "00000000-0000-4000-8000-000000000002"),
        )
        for ((runtime, next) in wrong) {
            assertStaleUpdate { controller.updateResource(runtime, next) }
            assertEquals(before, controller.snapshot)
            assertEquals(11_000L, controller.deadline)
        }
        revision++ // A queued replacement or disconnect already invalidated the old command.
        assertStaleUpdate { controller.updateResource(before.runtimeId, request()) }
        assertEquals(before, controller.snapshot)
    }

    fun testOldUpdateCannotTouchReplacementRequestOrDisconnectedState() {
        connect()
        val runtime = controller.snapshot.runtimeId
        revision++
        controller.connect(request(requestId = "00000000-0000-4000-8000-000000000002"), revision)
        controller.start(revision)
        val replacement = controller.snapshot
        assertStaleUpdate { controller.updateResource(runtime, request(deadline = null)) }
        assertEquals(replacement, controller.snapshot)
        controller.disconnect()
        val idle = controller.snapshot
        assertStaleUpdate { controller.updateResource(runtime,
            request(requestId = "00000000-0000-4000-8000-000000000002")) }
        assertEquals(idle, controller.snapshot)
    }

    fun testUpdatedLeaseStillRequiresCurrentNetworkAndCommand() {
        connect()
        controller.updateResource(controller.snapshot.runtimeId, request(deadline = null))
        val callback = backend.valid
        assertTrue(callback())
        backend.networkRevision++
        assertFalse(callback())
        controller.networkChanged()
        assertTrue(backend.valid())
        assertFalse(callback())
        revision++
        assertFalse(backend.valid())
    }

    fun testTransportChangePausesAndRetainsTunWithoutExposingConfiguration() {
        connect()
        val oldValid = backend.valid
        controller.updateResource(controller.snapshot.runtimeId,
            request(transport = VpnTransport("entry.invalid", 443, mapOf("password" to "new-fixture-secret"))))
        assertEquals("paused", controller.snapshot.state)
        assertEquals("CONFIG_CHANGED", controller.snapshot.errorCode)
        assertTrue(controller.snapshot.tunActive)
        assertEquals(1, backend.core.closes)
        assertEquals(0, backend.tunnel.closes)
        assertFalse(oldValid())
        assertFalse(controller.snapshot.toMap().toString().contains("fixture-secret"))
    }

    fun testCanonicalTransportIgnoresObjectOrderAndNumberSpellingOnly() {
        val a = VpnTransport("entry.invalid", 443, linkedMapOf("version" to 1,
            "nested" to mapOf("enabled" to true, "values" to listOf("fixture-secret", null, 2))))
        val b = VpnTransport("entry.invalid", 443, linkedMapOf("nested" to linkedMapOf(
            "values" to listOf("fixture-secret", null, 2.0), "enabled" to true), "version" to 1.0))
        assertTrue(a.matches(b))
        assertFalse(a.toString().contains("fixture-secret"))
        assertFalse(a.matches(VpnTransport("different.invalid", 443, emptyMap<String, Any>())))
        assertFalse(VpnTransport("entry.invalid", 443, listOf(1, 2)).matches(VpnTransport("entry.invalid", 443, listOf(2, 1))))
        assertFalse(VpnTransport("entry.invalid", 443, mapOf("x" to 1)).matches(VpnTransport("entry.invalid", 443, mapOf("x" to "1"))))
        assertFalse(VpnTransport("entry.invalid", 443, null).matches(VpnTransport("entry.invalid", 444, null)))
    }

    fun testLeaseUpdatesAffectExistingCallbackWithoutRestartingSession() {
        controller.connect(request(deadline = null), revision)
        controller.start(revision)
        val current = backend.core
        val callback = backend.valid
        controller.updateResource(controller.snapshot.runtimeId, request(deadline = 3_000))
        time = 2_999
        assertTrue(callback())
        time = 3_000
        assertFalse(callback())
        controller.updateResource(controller.snapshot.runtimeId, request(deadline = 7_000))
        assertTrue(callback())
        time = 7_000
        assertFalse(callback())
        controller.updateResource(controller.snapshot.runtimeId, request(deadline = null))
        assertTrue(callback())
        assertSame(current, backend.core)
        assertEquals(1, backend.starts)
        assertEquals(0, current.closes)
    }

    fun testEarlierAlreadyElapsedLeasePausesImmediately() {
        connect()
        time = 5_000
        controller.updateResource(controller.snapshot.runtimeId, request(deadline = 4_000))
        assertEquals("RESOURCE_EXPIRED", controller.snapshot.errorCode)
        assertEquals("paused", controller.snapshot.state)
        assertFalse(backend.valid())
        assertTrue(controller.snapshot.tunActive)
    }

    fun testNetworkReconnectKeepsUpdatedConfigAndLeaseWithoutRecalculation() {
        connect()
        controller.updateResource(controller.snapshot.runtimeId, request(deadline = 9_000, config = "{updated-clock}"))
        backend.network = false
        backend.networkRevision++
        controller.networkChanged()
        time = 6_000
        backend.network = true
        backend.networkRevision++
        controller.networkChanged()
        assertEquals("{updated-clock}", backend.lastConfig)
        assertEquals(9_000L, controller.deadline)
        time = 9_000
        assertFalse(backend.valid())
    }

    fun testLeaseRenewalAfterPauseCannotReactivateOldCallbackOrResumeNetwork() {
        connect()
        val oldValid = backend.valid
        revision++ // Model the real command queue's cancellation epoch.
        controller.pause("ELIGIBILITY_UNCONFIRMED", revision)
        controller.updateResource(controller.snapshot.runtimeId, request(deadline = 30_000))
        assertFalse(oldValid())
        controller.start(revision) // A startup continuation queued before the pause.
        backend.networkRevision++
        controller.networkChanged()
        assertEquals("paused", controller.snapshot.state)
        assertEquals(1, backend.starts)
    }

    fun testPauseInvalidatesCallbackBeforeCoreCloseAndDeadlineClear() {
        connect()
        backend.core.duringClose = { assertFalse(backend.valid()) }
        controller.pause("PAUSED")
        assertNull(controller.deadline)
        assertFalse(backend.valid())
    }

    private fun assertStaleUpdate(work: () -> Unit) {
        try { work(); fail("Expected stale binding rejection") }
        catch (failure: VpnFailure) { assertEquals("STALE_RESOURCE_UPDATE", failure.code) }
    }

    fun testTrafficIsReadOnlyAndBoundToNativeIdentity() {
        connect()
        backend.core.uploaded = "9007199254740993"
        backend.core.downloaded = "2048"
        val generation = controller.snapshot.generation
        val value = controller.traffic()
        assertEquals("9007199254740993", value["uploadBytes"])
        assertEquals("2048", value["downloadBytes"])
        assertEquals(controller.snapshot.runtimeId, value["runtimeId"])
        assertEquals("account", value["accountKey"])
        assertEquals("resource", value["resourceKey"])
        assertEquals(time, value["monotonicMillis"])
        assertEquals(true, value["active"])
        assertEquals(generation, controller.snapshot.generation)
        assertFalse(value.toString().contains("fixture-secret"))
    }

    fun testTrafficIncludesFinalCloseBytesAndSurvivesAutomaticReconnect() {
        connect()
        backend.core.uploaded = "100"
        backend.core.downloaded = "200"
        val epoch = controller.traffic()["counterEpoch"]
        val closing = backend.core
        closing.duringClose = { closing.uploaded = "120" }
        backend.networkRevision++
        controller.networkChanged()
        backend.core.uploaded = "30"
        backend.core.downloaded = "40"
        assertEquals("150", controller.traffic()["uploadBytes"])
        assertEquals("240", controller.traffic()["downloadBytes"])
        assertEquals(epoch, controller.traffic()["counterEpoch"])
        controller.connect(request(resource = "other"), ++revision)
        controller.start(revision)
        assertEquals("0", controller.traffic()["uploadBytes"])
        assertFalse(epoch == controller.traffic()["counterEpoch"])
        controller.disconnect()
        assertEquals("0", controller.traffic()["downloadBytes"])
        assertNull(controller.traffic()["accountKey"])
    }

    fun testTrafficSamplingFailureDoesNotPauseVpnAndMissingFinalSampleIsIncomplete() {
        connect()
        backend.core.uploaded = "100"
        controller.traffic()
        backend.core.failTrafficLinkage = true
        assertEquals(false, controller.traffic()["available"])
        assertEquals("connected", controller.snapshot.state)
        backend.core.failTrafficLinkage = false
        backend.core.failTraffic = true
        assertEquals(false, controller.traffic()["available"])
        assertEquals("100", controller.traffic()["uploadBytes"])
        assertEquals("connected", controller.snapshot.state)
        backend.networkRevision++
        controller.networkChanged()
        backend.core.uploaded = "20"
        val result = controller.traffic()
        assertEquals("120", result["uploadBytes"])
        assertEquals(false, result["complete"])
        assertEquals("connected", controller.snapshot.state)
    }

    fun testTrafficCounterRollbackIsNotAcceptedAsACompleteSample() {
        connect()
        backend.core.uploaded = "500"
        controller.traffic()
        backend.core.uploaded = "1"
        assertEquals(false, controller.traffic()["available"])
        assertEquals("500", controller.traffic()["uploadBytes"])
        backend.core.uploaded = "501"
        assertEquals(false, controller.traffic()["complete"])
        controller.pause("RESOURCE_EXPIRED")
        assertEquals("0", controller.traffic()["uploadBytes"])
    }

    private class FakeTunnel : VpnTunnel { var closes = 0; override fun close() { closes++ } }
    private class FakeCore : VpnCore {
        var uploaded = "0"
        var downloaded = "0"
        var failTraffic = false
        var failTrafficLinkage = false
        override fun traffic(): NativeTraffic {
            if (failTrafficLinkage) throw UnsatisfiedLinkError("test-missing-statistics-binding")
            if (failTraffic) error("private-native-error")
            return NativeTraffic(uploaded, downloaded, true)
        }
        var closes = 0
        var failClose = false
        var failProbe = false
        var duringClose: () -> Unit = {}
        override fun close() { duringClose(); if (failClose) error("fixture-secret"); closes++ }
        override fun observeExit(): String { if (failProbe) error("fixture-secret"); return "8.8.4.4" }
    }
    private class FakeBackend : VpnBackend {
        var network = true
        var networkRevision = 1L
        var starts = 0
        var failStart = false
        var duringStart: () -> Unit = {}
        var valid: () -> Boolean = { false }
        var lastConfig: String? = null
        var lastRoutingMode: String? = null
        val tunnel = FakeTunnel()
        var core = FakeCore()
        override fun networkRevision() = networkRevision
        override fun hasNetwork() = network
        override fun openTun(): VpnTunnel = tunnel
        override fun start(request: VpnRequest, tun: VpnTunnel, valid: () -> Boolean): VpnCore {
            this.valid = valid
            lastConfig = request.config
            lastRoutingMode = request.transport.routingMode
            starts++
            if (failStart) error("fixture-secret")
            core = FakeCore()
            duringStart()
            return core
        }
        override fun clearUnderlyingNetwork() = Unit
    }
}
