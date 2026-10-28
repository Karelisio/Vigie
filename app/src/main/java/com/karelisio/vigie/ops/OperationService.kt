package com.karelisio.vigie.ops

import android.annotation.SuppressLint
import android.app.Notification
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.util.Log
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ProcessLifecycleOwner
import com.karelisio.vigie.appContainer
import com.karelisio.vigie.data.AgentJob
import com.karelisio.vigie.data.Connection
import com.karelisio.vigie.data.LiveEvent
import com.karelisio.vigie.data.Repository
import com.karelisio.vigie.data.Server
import com.karelisio.vigie.push.Notifications
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.cancelChildren
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

/**
 * Suit une opération de l'agent (mise à jour, nettoyage, sauvegarde…) dans une notification continue,
 * appli fermée comprise, puis affiche son bilan.
 *
 * Service de premier plan de type `dataSync` : il garde ouverte la connexion WebSocket qui reçoit la
 * progression (transfert de données réseau, lancé par l'utilisateur, de durée bornée). `shortService`
 * est limité à 3 minutes (une sauvegarde ou « tout mettre à jour » dure plus) et `specialUse` est
 * réservé aux cas qu'aucun autre type ne couvre. Le service ne tourne que pendant l'opération,
 * 50 minutes au plus (l'agent abandonne une opération après 45 minutes), et s'arrête dès la fin.
 *
 * Tant que la connexion est ouverte, l'agent n'envoie pas sa propre notification de fin (il ne le fait
 * que sans client connecté) : pas de doublon.
 */
class OperationService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private var tracking: Job? = null
    private var trackedId: String? = null

    /** Incrémenté à chaque suivi : un suivi remplacé ne touche plus au service. */
    private var generation = 0
    private var dismissed = false
    private var lastShown: Notification? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_DISMISSED) {
            // Notification balayée (Android 14 et plus) : plus de mise à jour, mais le bilan s'affichera.
            dismissed = true
            if (tracking?.isActive != true) stopSelf()
            return START_NOT_STICKY
        }
        val c = appContainer
        val live = c.repository.live.value
        val job = intent?.getStringExtra(EXTRA_JOB)?.let { id -> live.jobs.firstOrNull { it.id == id } }
        val server = c.repository.activeServer
        // startForeground doit suivre startForegroundService, même pour s'arrêter aussitôt.
        val notification = if (job == null && tracking?.isActive == true && lastShown != null) {
            lastShown!!
        } else {
            val shown = job ?: AgentJob(id = "", phase = "queued")
            OperationNotifications.progress(
                this, OperationText.progress(shown, OperationText.members(shown, live.containers)), server?.name.orEmpty(), shown.kind, shown.startedAt,
            )
        }
        val started = try {
            val type = if (Build.VERSION.SDK_INT >= 29) ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC else 0
            ServiceCompat.startForeground(this, OperationNotifications.ID, notification, type)
            true
        } catch (e: Exception) {
            Log.w(TAG, "Service de premier plan refusé", e)
            false
        }
        when {
            !started -> stopSelf()
            job == null || server == null || job.done -> if (tracking?.isActive != true) {
                ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
                stopSelf()
            }
            trackedId != job.id -> track(job, server, live.containers.let { OperationText.members(job, it) })
        }
        return START_NOT_STICKY
    }

    private fun track(job: AgentJob, server: Server, members: Int) {
        tracking?.cancel()
        trackedId = job.id
        dismissed = false
        val gen = ++generation
        tracking = scope.launch {
            val repo = appContainer.repository
            repo.hold(true)
            var ending: Ending = Ending.Silent
            try {
                ending = withTimeoutOrNull(MAX_DURATION_MS) { follow(repo, job, server, members) } ?: Ending.Interrupted(OperationText.title(job))
            } finally {
                withContext(NonCancellable) { if (gen == generation) finish(gen, ending, job, server) }
            }
        }
    }

    /** Suit l'opération jusqu'à sa fin ; renvoie ce qu'il faut afficher ensuite. */
    private suspend fun follow(repo: Repository, job: AgentJob, server: Server, members: Int): Ending = coroutineScope {
        val done = CompletableDeferred<Ending>()
        val startVersion = repo.live.value.agentVersion
        val latest = MutableStateFlow<OperationProgress?>(null)
        var restarting = false
        var wasLive = repo.live.value.isLive

        // Au plus une mise à jour de la notification par seconde (l'agent en envoie jusqu'à 4).
        launch {
            latest.filterNotNull().collect { p ->
                val n = OperationNotifications.progress(this@OperationService, p, server.name, job.kind, job.startedAt)
                lastShown = n
                if (!dismissed) post(n)
                delay(1_000)
            }
        }
        launch {
            repo.live.collect { st ->
                if (st.connection == Connection.Unauthorized) done.complete(Ending.Silent)
                val j = st.jobs.firstOrNull { it.id == job.id }
                if (j != null && !restarting) {
                    when {
                        OperationText.awaitsRestart(j) -> {
                            restarting = true
                            latest.value = OperationText.restarting(j)
                            launch { done.complete(watchRestart(repo, j, startVersion)) }
                        }
                        j.done -> done.complete(Ending.Finished(OperationText.outcome(j), j.finishedAt))
                        else -> latest.value = OperationText.progress(j, members)
                    }
                }
                // Reconnexion : la fin a pu être manquée pendant la coupure.
                if (st.isLive && !wasLive && !restarting) launch { verify(repo, job, done) }
                wasLive = st.isLive
            }
        }
        // La nouvelle version de l'agent s'annonce à la reconnexion.
        launch {
            repo.events.collect { e ->
                if (e is LiveEvent.AgentUpdated && job.kind == "self") {
                    done.complete(Ending.Finished(OperationText.selfUpdated(job.id, e.from, e.to), System.currentTimeMillis()))
                }
            }
        }
        // Autre serveur choisi dans l'appli : la connexion suivie n'est plus la bonne.
        launch {
            appContainer.servers.activeId.first { it != server.id }
            done.complete(Ending.Silent)
        }
        // Opération peut-être déjà terminée (fin reçue pendant une coupure).
        launch { verify(repo, job, done) }
        done.await().also { coroutineContext.cancelChildren() }
    }

    /** Confronte l'opération à la liste de l'agent (qui met à jour l'état local si elle est finie). */
    private suspend fun verify(repo: Repository, job: AgentJob, done: CompletableDeferred<Ending>) {
        val list = runCatching { repo.jobs() }.getOrNull() ?: return
        // Absente : l'agent a redémarré pendant l'opération (sauf pour sa propre mise à jour, suivie à part).
        if (list.none { it.id == job.id } && job.kind != "self") done.complete(Ending.Interrupted(OperationText.title(job)))
    }

    /** Après le lancement de la mise à jour de l'agent : attend la nouvelle version, ou l'ancienne rétablie. */
    private suspend fun watchRestart(repo: Repository, job: AgentJob, startVersion: String): Ending {
        delay(15_000)
        repeat(18) {
            val info = runCatching { repo.info() }.getOrNull()
            if (info != null) {
                if (startVersion.isNotEmpty() && info.version.isNotEmpty() && info.version != startVersion) {
                    return Ending.Finished(OperationText.selfUpdated(job.id, startVersion, info.version), System.currentTimeMillis())
                }
                val r = info.lastSelfUpdate
                if (r != null && r.ts >= job.startedAt) {
                    val outcome = if (r.ok) OperationText.selfUpdated(job.id, r.from, info.version) else OperationText.selfRolledBack(job.id, r.error.orEmpty())
                    return Ending.Finished(outcome, r.ts)
                }
            }
            delay(10_000)
        }
        return Ending.Finished(OperationText.outcome(job), job.finishedAt)
    }

    /** Remplace la progression par le bilan (appli en arrière-plan), puis libère la connexion. */
    private suspend fun finish(gen: Int, ending: Ending, job: AgentJob, server: Server) {
        // Appli ouverte : le bilan s'affiche déjà en bas de l'écran, la notification disparaît.
        val visible = ProcessLifecycleOwner.get().lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)
        val replacement = when (ending) {
            is Ending.Finished -> OperationNotifications.outcome(this, ending.outcome, server.name, job.kind, ending.at)
            is Ending.Interrupted -> OperationNotifications.interrupted(this, ending.title, server.name, job.kind)
            Ending.Silent -> null
        }
        if (replacement == null || visible) {
            ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
        } else {
            ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_DETACH)
            post(replacement)
        }
        trackedId = null
        lastShown = null
        // Connexion gardée un instant : l'agent sait que le bilan a été reçu et ne notifie pas en double.
        delay(LINGER_MS)
        // Une nouvelle opération a pris le relais entre-temps : elle garde la connexion et le service.
        if (gen != generation) return
        appContainer.repository.hold(false)
        stopSelf()
    }

    @SuppressLint("MissingPermission") // vérifiée par canPost()
    private fun post(n: Notification) {
        if (!Notifications.canPost(this)) return
        runCatching { NotificationManagerCompat.from(this).notify(OperationNotifications.ID, n) }
    }

    override fun onTimeout(startId: Int, fgsType: Int) {
        // Android 15 : quota quotidien des services dataSync atteint.
        generation++
        tracking?.cancel()
        appContainer.repository.hold(false)
        ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    override fun onDestroy() {
        generation++
        scope.cancel()
        appContainer.repository.hold(false)
        super.onDestroy()
    }

    private sealed interface Ending {
        data class Finished(val outcome: OperationOutcome, val at: Long) : Ending
        data class Interrupted(val title: String) : Ending
        data object Silent : Ending
    }

    companion object {
        private const val TAG = "OperationService"
        private const val EXTRA_JOB = "job_id"
        internal const val ACTION_DISMISSED = "com.karelisio.vigie.ops.DISMISSED"
        private const val MAX_DURATION_MS = 50 * 60_000L
        private const val LINGER_MS = 2_000L

        /** Opérations déjà suivies (jamais deux fois la même). */
        private val handled = java.util.Collections.synchronizedSet(LinkedHashSet<String>())

        /**
         * Affiche la progression d'une opération en cours tant qu'elle dure. Ne fait rien si elle est
         * déjà suivie ou si les notifications de Vigie sont désactivées : l'agent préviendra alors à la fin.
         */
        fun follow(context: Context, job: AgentJob) {
            if (job.done || job.id.isEmpty() || job.id in handled || !OperationNotifications.canShow(context)) return
            handled += job.id
            try {
                ContextCompat.startForegroundService(context, Intent(context, OperationService::class.java).putExtra(EXTRA_JOB, job.id))
            } catch (e: Exception) {
                // Démarrage refusé hors de l'appli (Android 12 et plus) : nouvel essai au prochain retour.
                handled -= job.id
                Log.w(TAG, "Suivi de l'opération impossible", e)
            }
        }

        /**
         * Lance le suivi de toute opération en cours pendant que l'appli est ouverte (lancée depuis
         * l'appli, ou déjà en cours), ou au moment où elle passe en arrière-plan.
         */
        fun watch(context: Context, repository: Repository, scope: CoroutineScope) {
            val foreground = ProcessLifecycleOwner.get().lifecycle.currentStateFlow
                .map { it.isAtLeast(Lifecycle.State.STARTED) }
                .distinctUntilChanged()
            val running = repository.live.map { it.runningJob }.distinctUntilChanged { a, b -> a?.id == b?.id }
            scope.launch {
                combine(running, foreground) { job, _ -> job }.collect { job -> if (job != null) follow(context, job) }
            }
        }
    }
}
