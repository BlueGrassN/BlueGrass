package com.bluegrass.client.rules

import android.app.job.JobInfo
import android.app.job.JobScheduler
import android.content.ComponentName
import android.content.Context
import java.util.concurrent.TimeUnit

internal class RoutingRuleScheduler(
    context: Context,
    private val dailyId: Int = DAILY_JOB,
    private val checkId: Int = CHECK_JOB,
    private val scheduler: JobScheduler = context.getSystemService(JobScheduler::class.java),
    private val register: (JobInfo) -> Int = scheduler::schedule,
) {
    private val service = ComponentName(context, RoutingRuleJobService::class.java)

    fun ensureDaily(): Boolean {
        val expected = base(dailyId).setPeriodic(DAY, TimeUnit.HOURS.toMillis(2)).build()
        // Do not reset the period (or stop running work) on every Activity resume.
        return scheduler.getPendingJob(dailyId) == expected ||
            register(expected) == JobScheduler.RESULT_SUCCESS
    }

    fun checkWhenConnected(delay: Long = 0): Boolean {
        // Preserve an existing attempt, including its OS-managed retry/backoff window.
        if (scheduler.getPendingJob(checkId) != null) return true
        return register(base(checkId).setMinimumLatency(delay).build()) == JobScheduler.RESULT_SUCCESS
    }

    private fun base(id: Int) = JobInfo.Builder(id, service)
        .setRequiredNetworkType(JobInfo.NETWORK_TYPE_ANY)
        .setPersisted(true)
        .setBackoffCriteria(HOUR, JobInfo.BACKOFF_POLICY_EXPONENTIAL)

    companion object {
        const val DAILY_JOB = 0xB710
        const val CHECK_JOB = 0xB711
        val HOUR: Long = TimeUnit.HOURS.toMillis(1)
        val DAY: Long = TimeUnit.DAYS.toMillis(1)
    }
}

/** The Activity, JobService and VPN can each be the first entry into a fresh process. */
internal object RoutingRulesRuntime {
    @Volatile private var instance: RoutingRuleUpdater? = null
    private val schedulerWorker = java.util.concurrent.Executors.newSingleThreadExecutor {
        Thread(it, "bluegrass-rules-scheduler")
    }

    fun updater(context: Context): RoutingRuleUpdater = instance ?: synchronized(this) {
        instance ?: RoutingRuleUpdater(context.applicationContext).also { instance = it }
    }

    fun foreground(context: Context) {
        val app = context.applicationContext
        schedule {
            RoutingRuleScheduler(app).apply { ensureDaily(); checkWhenConnected() }
        }
    }

    fun prepareForVpn(context: Context) {
        val app = context.applicationContext
        schedule { RoutingRuleScheduler(app).ensureDaily() }
        // Already on the VPN lifecycle worker. This never waits for an update/download.
        updater(app).prepareForVpn()
    }

    fun afterManualUpdate(context: Context, result: RoutingRuleResult) {
        val app = context.applicationContext
        schedule {
            RoutingRuleScheduler(app).apply {
                ensureDaily()
                if (result.retry) checkWhenConnected(RoutingRuleScheduler.HOUR)
            }
        }
    }

    private fun schedule(work: () -> Unit) {
        schedulerWorker.execute {
            // Scheduling failure must never prevent opening the app or starting a VPN.
            try { work() } catch (_: Exception) { }
        }
    }
}
