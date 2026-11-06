package com.bluegrass.client.vpn

import android.net.VpnService
import android.os.ParcelFileDescriptor
import com.bluegrass.core.bridge.Bridge
import com.bluegrass.core.bridge.Platform
import java.net.Inet4Address
import org.json.JSONObject

internal class AndroidVpnBackend(
    private val service: VpnService,
    private val networks: PhysicalNetworks,
) : VpnBackend {
    private class Tunnel(val descriptor: ParcelFileDescriptor) : VpnTunnel {
        override fun close() = descriptor.close()
    }
    override fun hasNetwork() = networks.selected != null
    override fun networkRevision() = networks.revision.get()

    override fun openTun(): VpnTunnel {
        if (VpnService.prepare(service) != null) throw VpnFailure("VPN_PERMISSION_REQUIRED")
        networks.checkTunConflict()
        val link = networks.selected ?: throw VpnFailure("NETWORK_UNAVAILABLE")
        val builder = service.Builder().setSession("BlueGrass").setMtu(1500)
            .addAddress("172.19.0.1", 30).addAddress("fdfe:dcba:9876::1", 126)
            .addDnsServer("172.19.0.2")
            .addRoute("0.0.0.0", 0).addRoute("::", 0)
            .addDisallowedApplication(service.packageName)
            .setUnderlyingNetworks(arrayOf(link.network))
            .setBlocking(false)
        // No allowBypass(), allowFamily(), or route exclusions: Go owns DNS/LAN classification.
        return Tunnel(builder.establish() ?: throw VpnFailure("VPN_PERMISSION_REQUIRED"))
    }

    override fun start(request: VpnRequest, tun: VpnTunnel, valid: () -> Boolean): VpnCore {
        networks.checkTunConflict()
        val link = networks.selected ?: throw VpnFailure("NETWORK_UNAVAILABLE")
        if (!service.setUnderlyingNetworks(arrayOf(link.network))) throw VpnFailure("NETWORK_BIND_FAILED")
        val platform = object : Platform {
            // Called by Go's read/write gate as well as before opening a new socket.
            override fun isValid(): Boolean = try {
                valid() && networks.selected?.fingerprint == link.fingerprint
            } catch (_: Throwable) { false }
            private fun checkCurrent() {
                if (!isValid()) throw VpnFailure("NETWORK_UNAVAILABLE")
            }
            override fun protect(fd: Int) {
                guarded("SOCKET_PROTECT_FAILED") {
                    checkCurrent()
                    if (!service.protect(fd)) throw VpnFailure("SOCKET_PROTECT_FAILED")
                    // fromFd duplicates the descriptor. bindSocket changes the shared socket;
                    // closing this temporary duplicate never closes Go's owning descriptor.
                    ParcelFileDescriptor.fromFd(fd).use { link.network.bindSocket(it.fileDescriptor) }
                    checkCurrent()
                }
            }
            override fun resolve(host: String): String = guarded("ENTRY_RESOLVE_FAILED") {
                checkCurrent()
                val result = link.network.getAllByName(host).filterIsInstance<Inet4Address>()
                    .mapNotNull { it.hostAddress }.distinct().joinToString(",")
                checkCurrent()
                if (result.isEmpty()) throw VpnFailure("ENTRY_RESOLVE_FAILED")
                result
            }
            override fun networkInfo(): String = guarded("NETWORK_INFO_FAILED") { checkCurrent(); link.json() }
            private fun <T> guarded(code: String, work: () -> T): T = try { work() }
                catch (failure: VpnFailure) { throw failure }
                catch (_: Exception) { throw VpnFailure(code) }
        }
        val session = Bridge.start(request.config, (tun as Tunnel).descriptor.fd, platform)
        return object : VpnCore {
            override fun close() = session.close()
            override fun observeExit(): String = session.observeExit()
            override fun traffic(): NativeTraffic {
                val value = JSONObject(session.traffic())
                return NativeTraffic(value.getString("uploadBytes"), value.getString("downloadBytes"), value.getBoolean("complete"))
            }
        }
    }

    override fun clearUnderlyingNetwork() {
        // Empty means unavailable; null would let Android pick a default network.
        service.setUnderlyingNetworks(emptyArray())
    }
}
