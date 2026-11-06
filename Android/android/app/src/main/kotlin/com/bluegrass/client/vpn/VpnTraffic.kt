package com.bluegrass.client.vpn

import java.math.BigInteger
import java.util.UUID

internal class NativeTraffic(upload: String, download: String, val complete: Boolean) {
    val upload = parse(upload)
    val download = parse(download)
    companion object {
        private val maximum = BigInteger.ONE.shiftLeft(64).subtract(BigInteger.ONE)
        private fun parse(value: String): BigInteger {
            require(value.length <= 20 && value.matches(Regex("0|[1-9][0-9]*")))
            return BigInteger(value).also { require(it <= maximum) }
        }
    }
}

/** Lifecycle-worker owned; no account data or counters are persisted. */
internal class VpnTrafficLedger {
    private var epoch = UUID.randomUUID().toString()
    private var sequence = 0L
    private var upload = BigInteger.ZERO
    private var download = BigInteger.ZERO
    private var last: NativeTraffic? = null
    private var complete = true

    fun reset() {
        epoch = UUID.randomUUID().toString()
        sequence = 0
        upload = BigInteger.ZERO
        download = BigInteger.ZERO
        last = null
        complete = true
    }

    fun attach() { last = null }

    private fun read(reader: () -> NativeTraffic?): Boolean = try {
        val value = reader()
        val previous = last
        if (value == null) {
            false
        } else if (previous != null && (value.upload < previous.upload || value.download < previous.download)) {
            complete = false
            false
        } else {
            last = value
            complete = complete && value.complete
            true
        }
    } catch (_: Exception) { false }
      catch (_: LinkageError) { false }

    fun detach(reader: () -> NativeTraffic?) {
        if (!read(reader)) complete = false
        last?.let { upload += it.upload; download += it.download }
        last = null
    }

    fun sample(snapshot: VpnSnapshot, now: Long, hasCore: Boolean, active: Boolean,
        reader: () -> NativeTraffic?): Map<String, Any?> {
        val available = !hasCore || read(reader)
        return linkedMapOf(
            "version" to 1, "runtimeId" to snapshot.runtimeId,
            "requestId" to snapshot.requestId, "resourceKey" to snapshot.resourceKey,
            "accountKey" to snapshot.accountKey, "generation" to snapshot.generation,
            "counterEpoch" to epoch, "sequence" to ++sequence,
            "monotonicMillis" to now, "active" to active,
            "uploadBytes" to (upload + (last?.upload ?: BigInteger.ZERO)).toString(),
            "downloadBytes" to (download + (last?.download ?: BigInteger.ZERO)).toString(),
            "available" to available, "complete" to complete,
        )
    }
}
