package com.bluegrass.client.vpn

import android.app.AlarmManager
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.VpnService
import android.os.Build
import com.bluegrass.client.MainActivity
import com.bluegrass.client.R

class BlueGrassVpnService : VpnService() {
    private lateinit var networks: PhysicalNetworks
    private var foreground = false
    private var destroyed = false
    private var lastGeneration = -1L
    private var startId = 0
    private var alarmDeadline: Long? = null
    private val expiryAlarm = AlarmManager.OnAlarmListener { VpnRuntime.expire() }

    override fun onCreate() {
        super.onCreate()
        getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(CHANNEL, "VPN 连接", NotificationManager.IMPORTANCE_LOW).apply {
                description = "BlueGrass VPN 连接与流量保护状态"
                setShowBadge(false)
                lockscreenVisibility = Notification.VISIBILITY_PRIVATE
            })
        networks = PhysicalNetworks(getSystemService(ConnectivityManager::class.java), VpnRuntime.main) {
            VpnRuntime.networkChanged(this)
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        this.startId = startId
        if (intent?.action == ACTION_DISCONNECT) {
            VpnRuntime.command("disconnect") { _, _ -> VpnRuntime.finishWhenIdle(this) }
            return START_NOT_STICKY
        }
        if (intent?.action != ACTION_RUN) { finishService(); return START_NOT_STICKY }
        try {
            val notification = notification(VpnRuntime.snapshot)
            if (Build.VERSION.SDK_INT >= 34) {
                startForeground(NOTIFICATION, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_SYSTEM_EXEMPTED)
            } else startForeground(NOTIFICATION, notification)
            foreground = true
            networks.start()
            VpnRuntime.attach(this, AndroidVpnBackend(this, networks))
        } catch (_: Exception) {
            VpnRuntime.serviceFailed("SERVICE_START_FAILED")
            finishService()
        }
        return START_NOT_STICKY
    }

    override fun onRevoke() { VpnRuntime.revoke(this) }

    override fun onDestroy() {
        destroyed = true
        getSystemService(AlarmManager::class.java).cancel(expiryAlarm)
        try { networks.close() } catch (_: Exception) { }
        VpnRuntime.detach(this)
        super.onDestroy()
    }

    internal fun render(value: VpnSnapshot) {
        if (destroyed || !foreground || value.generation < lastGeneration) return
        lastGeneration = value.generation
        getSystemService(NotificationManager::class.java).notify(NOTIFICATION, notification(value))
    }

    internal fun scheduleExpiry(deadline: Long?) {
        if (destroyed || deadline == alarmDeadline) return
        val alarms = getSystemService(AlarmManager::class.java)
        alarms.cancel(expiryAlarm)
        alarmDeadline = deadline
        if (deadline == null) return
        // Foreground-service listener, never persisted. Android 12/13 may deny exact alarms;
        // the lifecycle timer and every native callback still check elapsedRealtime.
        try { alarms.setExact(AlarmManager.ELAPSED_REALTIME_WAKEUP, deadline, "bluegrass-expiry", expiryAlarm, VpnRuntime.worker) }
        catch (_: SecurityException) { alarms.set(AlarmManager.ELAPSED_REALTIME_WAKEUP, deadline, "bluegrass-expiry", expiryAlarm, VpnRuntime.worker) }
    }

    internal fun finishService() {
        if (destroyed) return
        if (stopSelfResult(startId)) {
            foreground = false
            stopForeground(STOP_FOREGROUND_REMOVE)
        }
    }

    private fun notification(snapshot: VpnSnapshot): Notification {
        val text = when (snapshot.state) {
            "connecting" -> if (snapshot.errorCode == "NETWORK_UNAVAILABLE") "等待网络，VPN 尚未建立" else "正在连接 VPN"
            "connected" -> "VPN 已连接"
            "paused" -> if (!snapshot.tunActive) "等待网络，VPN 尚未建立" else when (snapshot.errorCode) {
                "RESOURCE_EXPIRED" -> "资源已到期，流量已暂停"
                "CONFIG_CHANGED" -> "资源配置已变更，流量已暂停"
                "NETWORK_UNAVAILABLE" -> "等待网络，流量已暂停"
                else -> "连接已暂停，流量已阻断"
            }
            "error" -> if (snapshot.tunActive) "连接异常，保留流量保护" else "VPN 连接异常"
            else -> "正在准备 VPN"
        }
        val content = PendingIntent.getActivity(this, 0,
            Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP), PendingIntent.FLAG_IMMUTABLE)
        val disconnect = PendingIntent.getService(this, 1,
            Intent(this, BlueGrassVpnService::class.java).setAction(ACTION_DISCONNECT), PendingIntent.FLAG_IMMUTABLE)
        return Notification.Builder(this, CHANNEL).setSmallIcon(R.drawable.ic_vpn_notification)
            .setContentTitle("BlueGrass").setContentText(text).setContentIntent(content)
            .setOngoing(true).setOnlyAlertOnce(true).setCategory(Notification.CATEGORY_SERVICE)
            .setVisibility(Notification.VISIBILITY_PRIVATE)
            .addAction(Notification.Action.Builder(null, "断开", disconnect).build()).build()
    }

    companion object {
        const val ACTION_RUN = "com.bluegrass.client.vpn.RUN"
        const val ACTION_DISCONNECT = "com.bluegrass.client.vpn.DISCONNECT"
        private const val CHANNEL = "bluegrass.vpn"
        private const val NOTIFICATION = 1901
    }
}
