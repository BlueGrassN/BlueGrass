package com.bluegrass.client.vpn

import android.test.InstrumentationTestCase
import com.bluegrass.core.bridge.Bridge
import org.json.JSONObject

/** Actual JNI load/validation on the installed device ABI; no remote proxy. */
@Suppress("DEPRECATION")
class NativeCoreDeviceTest : InstrumentationTestCase() {
    fun testInstalledNativeLibraryIsXrayBridge() {
        assertTrue(Bridge.version().startsWith("Xray-core "))
        assertTrue(Bridge.version().endsWith(" / bridge 2"))
        assertFalse(Bridge.version().contains("sing-box"))
    }

    fun testNativeValidationAcceptsResourceWithoutNetwork() {
        Bridge.validate("""{"version":1,"host":"entry.example.invalid","port":1080,"protocolConfig":{"version":1,"type":"socks5","socks5":{"username":"fixture","password":"TEST_ONLY"}},"expiresAtUtc":null,"effectiveNowUtc":"2026-10-24T00:00:00Z"}""")
    }

    fun testNativeValidationDoesNotExposeInvalidInput() {
        try {
            Bridge.validate("INVALID_TEST_ONLY_PASSWORD")
            fail("Expected fixed validation failure")
        } catch (failure: Exception) {
            assertEquals("INVALID_CONFIG", failure.message)
            assertFalse(failure.toString().contains("TEST_ONLY_PASSWORD"))
        }
    }

    fun testInstalledCoreValidatesBothRoutingModes() {
        val config = JSONObject("""{"version":1,"host":"entry.example.invalid","port":1080,"protocolConfig":{"version":1,"type":"socks5","socks5":{"username":"fixture","password":"TEST_ONLY"}},"expiresAtUtc":null,"effectiveNowUtc":"2026-10-24T00:00:00Z"}""")
        for (mode in listOf("global", "bypass_cn")) {
            Bridge.validate(config.put("routingMode", mode).toString())
        }
        for (invalid in listOf<Any>(JSONObject.NULL, "", "direct", "GLOBAL", 1)) {
            try {
                Bridge.validate(config.put("routingMode", invalid).toString())
                fail("Invalid mode reached core")
            } catch (failure: Exception) {
                assertEquals("INVALID_CONFIG", failure.message)
            }
        }
    }
}
