package com.bluegrass.client.vpn

import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.os.Handler
import android.os.Build
import org.json.JSONArray
import org.json.JSONObject
import java.net.NetworkInterface
import java.net.InetAddress
import java.util.concurrent.atomic.AtomicLong

/** Callbacks run on the main handler; immutable selected snapshots are safe for Go threads. */
internal class PhysicalNetworks(
    private val manager: ConnectivityManager,
    private val handler: Handler,
    private val changed: () -> Unit,
) {
    internal class Link(val network: Network, properties: LinkProperties) {
        private val interfaceName = properties.interfaceName
        private val linkMtu = if (Build.VERSION.SDK_INT >= 29) properties.mtu else 0
        val addresses = properties.linkAddresses.toList()
        val routes = properties.routes.toList()
        private val dnsServers = properties.dnsServers.toList()
        private val domains = properties.domains
        val fingerprint = listOf(network.networkHandle, interfaceName, linkMtu,
            addresses.sortedBy { it.toString() }, routes.sortedBy { it.toString() }, dnsServers, domains).toString()

        fun json(): String {
            val name = interfaceName ?: throw VpnFailure("NETWORK_INFO_FAILED")
            val iface = NetworkInterface.getByName(name) ?: throw VpnFailure("NETWORK_INFO_FAILED")
            if (iface.index <= 0) throw VpnFailure("NETWORK_INFO_FAILED")
            val mtu = linkMtu.takeIf { it > 0 } ?: iface.mtu.takeIf { it > 0 } ?: 1500
            return JSONObject().put("name", name).put("index", iface.index).put("mtu", mtu)
                .put("addresses", JSONArray(addresses.map {
                    "${it.address.hostAddress!!.substringBefore('%')}/${it.prefixLength}"
                }))
                .put("dnsServers", JSONArray(dnsServers.map { it.hostAddress }))
                .put("domains", JSONArray(domains.orEmpty().split(Regex("\\s+")).filter { it.isNotBlank() }))
                .put("routes", JSONArray(routes.filter { it.destination.prefixLength > 0 }
                    .map { it.destination.toString() }.distinct()))
                .toString()
        }
    }

    @Volatile var selected: Link? = null
        private set
    @Volatile private var links: List<Link> = emptyList()
    val revision = AtomicLong(0)
    private val candidates = mutableMapOf<Network, Link>()
    private val capabilities = mutableMapOf<Network, NetworkCapabilities>()
    private var registered = false
    private val blocked = mutableSetOf<Network>()
    private val callback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) = refresh(network)
        override fun onLost(network: Network) {
            blocked.remove(network)
            candidates.remove(network)
            capabilities.remove(network)
            select()
        }
        override fun onCapabilitiesChanged(network: Network, value: NetworkCapabilities) {
            capabilities[network] = value
            if (!physical(value)) candidates.remove(network)
            select()
        }
        override fun onLinkPropertiesChanged(network: Network, value: LinkProperties) {
            candidates[network] = Link(network, value)
            select()
        }
        override fun onBlockedStatusChanged(network: Network, blocked: Boolean) {
            if (blocked) this@PhysicalNetworks.blocked.add(network) else this@PhysicalNetworks.blocked.remove(network)
            select()
        }
    }

    @Suppress("DEPRECATION")
    fun start() {
        if (registered) return
        manager.registerNetworkCallback(NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN).build(), callback, handler)
        registered = true
        manager.allNetworks.forEach(::refresh)
    }

    fun close() {
        if (registered) manager.unregisterNetworkCallback(callback)
        registered = false
        candidates.clear()
        capabilities.clear()
        blocked.clear()
        select()
    }

    private fun refresh(network: Network) {
        val caps = manager.getNetworkCapabilities(network)
        val properties = manager.getLinkProperties(network)
        if (caps != null && physical(caps) && properties != null) {
            capabilities[network] = caps
            candidates[network] = Link(network, properties)
        }
        select()
    }

    private fun select() {
        val available = candidates.values.filter { it.network !in blocked && physical(capabilities[it.network]) }
        links = available
        val active = manager.activeNetwork
        val next = available.maxByOrNull { link ->
            val caps = capabilities[link.network]!!
            (if (caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)) 100 else 0) +
                (if (link.network == active) 50 else 0) +
                (if (caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)) 30 else 0) +
                (if (caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) 20 else 0)
        }
        if (next?.fingerprint != selected?.fingerprint) {
            selected = next
            revision.incrementAndGet()
            changed()
        }
    }

    /** Every currently known physical link, including broad non-default static routes. */
    fun checkTunConflict() {
        val reserved = listOf(InetAddress.getByName("172.19.0.0") to 30,
            InetAddress.getByName("fdfe:dcba:9876::") to 126)
        for (link in links) {
            val local = link.addresses.map { it.address to it.prefixLength } +
                link.routes.map { it.destination }.filter { it.prefixLength > 0 }.map { it.address to it.prefixLength }
            if (reserved.any { tun -> local.any { other -> overlaps(tun.first, tun.second, other.first, other.second) } }) throw VpnFailure("TUN_ADDRESS_CONFLICT")
        }
    }

    companion object {
        private fun physical(caps: NetworkCapabilities?) = caps != null &&
            caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) &&
            caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) &&
            !caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)

        // IpPrefix's public constructor is API 33; this byte comparison also works on API 26.
        internal fun overlaps(a: InetAddress, aBits: Int, b: InetAddress, bBits: Int): Boolean {
            val left = a.address
            val right = b.address
            if (left.size != right.size) return false
            val bits = minOf(aBits, bBits)
            for (index in 0 until bits / 8) if (left[index] != right[index]) return false
            if (bits % 8 == 0) return true
            val mask = (255 shl (8 - bits % 8)) and 255
            return (left[bits / 8].toInt() and mask) == (right[bits / 8].toInt() and mask)
        }
    }
}
