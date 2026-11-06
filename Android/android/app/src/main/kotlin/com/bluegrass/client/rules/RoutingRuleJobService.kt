package com.bluegrass.client.rules

import android.app.job.JobParameters
import android.app.job.JobService
import java.util.IdentityHashMap

class RoutingRuleJobService : JobService() {
    private lateinit var runs: RoutingRuleJobRuns

    override fun onCreate() {
        super.onCreate()
        runs = RoutingRuleJobRuns(RoutingRulesRuntime.updater(applicationContext)) { key, retry ->
            jobFinished(key as JobParameters, retry)
        }
    }

    override fun onStartJob(params: JobParameters): Boolean {
        runs.start(params)
        return true
    }

    override fun onStopJob(params: JobParameters): Boolean = runs.stop(params)

    override fun onDestroy() {
        runs.destroy()
        super.onDestroy()
    }
}

/** Main-thread lifecycle state, independent of JobParameters construction for instrumentation. */
internal class RoutingRuleJobRuns(
    private val updater: RoutingRuleUpdater,
    private val finished: (Any, Boolean) -> Unit,
) {
    private class Run { var ticket: RoutingRuleUpdater.Ticket? = null }
    private val runs = IdentityHashMap<Any, Run>()

    fun start(key: Any) {
        if (runs.containsKey(key)) return
        val run = Run()
        runs[key] = run
        run.ticket = updater.update(false) { result ->
            if (runs[key] === run) {
                runs.remove(key)
                finished(key, result.retry)
            }
        }
    }

    fun stop(key: Any): Boolean {
        val run = runs.remove(key) ?: return false
        run.ticket?.let(updater::cancel)
        // JobInfo supplies an hour of initial exponential backoff, not an immediate loop.
        return true
    }

    fun destroy() {
        val stopped = runs.values.toList()
        runs.clear()
        stopped.forEach { it.ticket?.let(updater::cancel) }
    }
}
