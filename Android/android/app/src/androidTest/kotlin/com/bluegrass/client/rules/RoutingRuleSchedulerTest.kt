package com.bluegrass.client.rules

import android.Manifest
import android.app.job.JobInfo
import android.app.job.JobScheduler
import android.content.ComponentName
import android.content.pm.PackageManager
import android.test.InstrumentationTestCase

/** Reads actual registered Android jobs; test IDs and delayed windows prevent downloads. */
@Suppress("DEPRECATION")
class RoutingRuleSchedulerTest : InstrumentationTestCase() {
    private val daily = 0xB71E
    private val check = 0xB71F
    private val context get() = instrumentation.targetContext
    private val scheduler get() = context.getSystemService(JobScheduler::class.java)

    override fun setUp() {
        super.setUp()
        scheduler.cancel(daily)
        scheduler.cancel(check)
    }

    override fun tearDown() {
        scheduler.cancel(daily)
        scheduler.cancel(check)
        super.tearDown()
    }

    fun testActualDailyJobIsPersistedDailyAnyNetworkWithHourlyBackoff() {
        var registrations = 0
        val registration = RoutingRuleScheduler(context, daily, check, register = {
            registrations++
            scheduler.schedule(it) // Actual framework registration and parcel round-trip.
        })
        assertTrue(registration.ensureDaily())
        val job = scheduler.getPendingJob(daily)!!
        assertEquals(ComponentName(context, RoutingRuleJobService::class.java), job.service)
        assertTrue(job.isPersisted)
        assertTrue(job.isPeriodic)
        assertEquals(RoutingRuleScheduler.DAY, job.intervalMillis)
        assertEquals(JobInfo.NETWORK_TYPE_ANY, job.networkType)
        assertFalse(job.isRequireCharging)
        assertFalse(job.isRequireDeviceIdle)
        assertEquals(RoutingRuleScheduler.HOUR, job.initialBackoffMillis)
        assertEquals(JobInfo.BACKOFF_POLICY_EXPONENTIAL, job.backoffPolicy)
        assertTrue(registration.ensureDaily())
        assertEquals("Resume must not schedule again or reset the period", 1, registrations)
        assertEquals(job, scheduler.getPendingJob(daily))
    }

    fun testForegroundCheckPreservesPendingRetryAndDoesNotReplaceDailyJob() {
        val registration = RoutingRuleScheduler(context, daily, check)
        assertTrue(registration.ensureDaily())
        val periodic = scheduler.getPendingJob(daily)
        assertTrue(registration.checkWhenConnected(RoutingRuleScheduler.HOUR))
        val retry = scheduler.getPendingJob(check)!!
        assertFalse(retry.isPeriodic)
        assertTrue(retry.isPersisted)
        assertEquals(JobInfo.NETWORK_TYPE_ANY, retry.networkType)
        assertEquals(RoutingRuleScheduler.HOUR, retry.minLatencyMillis)
        assertEquals(RoutingRuleScheduler.HOUR, retry.initialBackoffMillis)
        assertTrue(registration.checkWhenConnected())
        assertEquals(retry, scheduler.getPendingJob(check))
        assertEquals(periodic, scheduler.getPendingJob(daily))
    }

    fun testMergedManifestGrantsPersistenceAndProtectsTheJobService() {
        assertEquals(PackageManager.PERMISSION_GRANTED,
            context.checkSelfPermission(Manifest.permission.RECEIVE_BOOT_COMPLETED))
        val service = context.packageManager.getServiceInfo(
            ComponentName(context, RoutingRuleJobService::class.java), 0,
        )
        assertFalse(service.exported)
        assertEquals("android.permission.BIND_JOB_SERVICE", service.permission)
        assertEquals(context.applicationInfo.processName, service.processName)
    }
}
