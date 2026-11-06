package com.bluegrass.client.vpn

import android.test.InstrumentationTestCase
import org.json.JSONObject
import java.net.InetAddress

/** Device-only: exercises Android's actual JSON and IP prefix APIs, never a real account. */
@Suppress("DEPRECATION")
class VpnPlatformContractTest : InstrumentationTestCase() {
    private fun config() = JSONObject().put("version", 1).put("host", "example.invalid")
        .put("port", 443).put("protocolConfig", JSONObject().put("version", 1))
        .put("expiresAtUtc", "2026-10-13T00:01:00Z").put("effectiveNowUtc", "2026-10-13T00:00:00Z")
    private fun arguments(config: String) = mapOf("requestId" to "00000000-0000-4000-8000-000000000001",
        "resourceKey" to "resource", "accountKey" to "account", "name" to "fixture", "config" to config)

    fun testDeadlineUsesEffectiveTimeAndElapsedClock() {
        val request = VpnRequestParser.parse(arguments(config().toString()), 1234)
        assertEquals(61_234L, request.deadline)
        assertFalse(request.toString().contains("example.invalid"))
        assertNull(VpnRequestParser.parse(arguments(config().put("expiresAtUtc", JSONObject.NULL).toString()), 1234).deadline)
    }

    fun testUtf8LimitIncludesMultibyteCharacters() {
        val value = config().put("padding", "界".repeat(11_000)).toString()
        assertTrue(value.length < VpnRequestParser.MAX_CONFIG_BYTES)
        assertFailure("INVALID_ARGUMENT") { VpnRequestParser.parse(arguments(value), 0) }
    }

    fun testRoutingModeDefaultsToGlobalOnlyWhenFieldIsAbsent() {
        val legacy = VpnRequestParser.parse(arguments(config().toString()), 0)
        val global = VpnRequestParser.parse(arguments(config().put("routingMode", "global").toString()), 0)
        val bypass = VpnRequestParser.parse(arguments(config().put("routingMode", "bypass_cn").toString()), 0)
        assertEquals("global", legacy.transport.routingMode)
        assertEquals("global", global.transport.routingMode)
        assertEquals("bypass_cn", bypass.transport.routingMode)
        assertTrue(legacy.transport.matches(global.transport))
        assertFalse(global.transport.matches(bypass.transport))
    }

    fun testExplicitInvalidRoutingModesAreRejectedOnConnectAndUpdate() {
        for (mode in listOf(JSONObject.NULL, "", "auto", "GLOBAL", "global ", "bypass-cn",
            1, true, JSONObject(), org.json.JSONArray())) {
            val args = arguments(config().put("routingMode", mode).toString())
            assertFailure("INVALID_ARGUMENT") { VpnRequestParser.parse(args, 0) }
            val input = VpnRequestParser.readUpdate(args.minus("name") +
                ("runtimeId" to "00000000-0000-4000-8000-000000000010"), 0)
            assertFailure("INVALID_ARGUMENT") { VpnRequestParser.parseUpdate(input) }
        }
    }

    fun testRoutingModeIsPartOfParsedResourceUpdateIdentity() {
        val initial = VpnRequestParser.parse(arguments(config().put("routingMode", "bypass_cn").toString()), 0)
        for ((mode, matches) in listOf("bypass_cn" to true, "global" to false)) {
            val args = arguments(config().put("routingMode", mode)
                .put("expiresAtUtc", JSONObject.NULL).toString()).minus("name") +
                ("runtimeId" to "00000000-0000-4000-8000-000000000010")
            val updated = VpnRequestParser.parseUpdate(VpnRequestParser.readUpdate(args, 10_000))
            assertEquals(matches, initial.transport.matches(updated.transport))
            assertEquals(mode, updated.transport.routingMode)
            assertNull(updated.deadline)
        }
    }

    fun testInvalidConfigIsFixedErrorWithoutInputOrParserDetails() {
        for (value in listOf("fixture-secret", config().put("port", 1.5).toString(),
            config().put("version", "1").toString(), config().put("effectiveNowUtc", "invalid-secret").toString())) {
            assertFailure("INVALID_ARGUMENT") { VpnRequestParser.parse(arguments(value), 0) }
        }
        assertFailure("RESOURCE_EXPIRED") {
            VpnRequestParser.parse(arguments(config().put("expiresAtUtc", "2026-10-13T00:00:00Z").toString()), 0)
        }
    }

    fun testTunOverlapIncludesContainingAndContainedRoutesButNotOtherFamily() {
        assertTrue(overlaps("172.19.0.0/30", "172.16.0.0/12"))
        assertTrue(overlaps("172.19.0.0/30", "172.19.0.2/32"))
        assertFalse(overlaps("172.19.0.0/30", "172.19.0.4/30"))
        assertFalse(overlaps("172.19.0.0/30", "fdfe:dcba:9876::/126"))
        assertTrue(overlaps("fdfe:dcba:9876::/126", "fdfe::/16"))
    }

    fun testUpdateEnvelopeNeedsNoNameAndAnchorsDeadlineAtReceipt() {
        val args = arguments(config().toString()).minus("name") +
            ("runtimeId" to "00000000-0000-4000-8000-000000000010")
        val envelope = VpnRequestParser.readUpdate(args, 2_000)
        val request = VpnRequestParser.parseUpdate(envelope)
        assertEquals(62_000L, request.deadline)
        assertEquals("account", request.accountKey)
        assertFalse(envelope.toString().contains("example.invalid"))
        assertFailure("INVALID_ARGUMENT") { VpnRequestParser.readUpdate(args.minus("runtimeId"), 2_000) }
    }

    fun testJsonTransportEqualityIgnoresTimestampsAndDeepObjectOrder() {
        val left = config().put("protocolConfig", JSONObject("""{"version":1,"nested":{"password":"fixture-secret","values":[true,null,2]}}"""))
        val right = config().put("effectiveNowUtc", "2026-10-13T00:00:10Z")
            .put("expiresAtUtc", JSONObject.NULL)
            .put("protocolConfig", JSONObject("""{"nested":{"values":[true,null,2.0],"password":"fixture-secret"},"version":1.0}"""))
        val a = VpnRequestParser.parse(arguments(left.toString()), 0)
        val b = VpnRequestParser.parse(arguments(right.toString()), 10_000)
        assertTrue(a.transport.matches(b.transport))
        assertFalse(a.transport.toString().contains("fixture-secret"))
        for (changed in listOf(config().put("host", "other.invalid"), config().put("port", 444),
            config().put("protocolConfig", JSONObject("""{"version":1,"nested":{"password":"changed"}}""")))) {
            assertFalse(a.transport.matches(VpnRequestParser.parse(arguments(changed.toString()), 0).transport))
        }
    }

    private fun overlaps(a: String, b: String) = PhysicalNetworks.overlaps(
        InetAddress.getByName(a.substringBefore('/')), a.substringAfter('/').toInt(),
        InetAddress.getByName(b.substringBefore('/')), b.substringAfter('/').toInt())

    private fun assertFailure(code: String, work: () -> Unit) {
        try { work(); fail("Expected $code") }
        catch (failure: VpnFailure) { assertEquals(code, failure.message); assertNull(failure.cause) }
    }
}
