package com.bluegrass.client.rules

import android.test.InstrumentationTestCase
import org.json.JSONObject
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference

/** Real JNI, local files/status only: no download, VPN, credentials or test directory in Go. */
@Suppress("DEPRECATION")
class RoutingRulesNativeContractTest : InstrumentationTestCase() {
    fun testActualGomobileConfigurationAndStatusContract() {
        val updater = RoutingRulesRuntime.updater(instrumentation.targetContext)
        // Keep Go's global store on the exact production noBackupFilesDir/routing-rules path.
        assertTrue(updater.prepareForVpn())
        val done = CountDownLatch(1)
        val response = AtomicReference<RoutingRuleResult>()
        updater.status { response.set(it); done.countDown() }
        assertTrue(done.await(30, TimeUnit.SECONDS))
        val status = JSONObject(response.get().json)
        assertTrue(status.getString("version").matches(Regex("[0-9]{12}")))
        assertTrue(status.getString("source") in setOf("bundled", "downloaded"))
        assertTrue(status.getString("lastError") in setOf("", "CACHE_INVALID", "UPDATE_FAILED", "UPDATE_CANCELLED"))
        assertTrue(status.get("updating") is Boolean)
    }
}
