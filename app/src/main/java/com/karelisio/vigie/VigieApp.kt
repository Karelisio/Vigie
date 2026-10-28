package com.karelisio.vigie

import android.app.Application
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner
import com.karelisio.vigie.data.Api
import com.karelisio.vigie.data.CachedState
import com.karelisio.vigie.data.Prefs
import com.karelisio.vigie.data.Repository
import com.karelisio.vigie.data.Server
import com.karelisio.vigie.data.ServerStore
import com.karelisio.vigie.data.StateCache
import com.karelisio.vigie.push.Notifications
import com.karelisio.vigie.push.Push
import com.karelisio.vigie.update.Updater
import com.karelisio.vigie.widget.SurfaceUpdater
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch

/** Conteneur de dépendances (injection manuelle). */
class AppContainer(app: Application) {
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    val api = Api()
    val servers = ServerStore(app)
    val prefs = Prefs(app)
    val cache = StateCache(app)
    val surfaces = SurfaceUpdater(app, scope)
    val repository = Repository(servers, api, cache, scope) { server: Server, state: CachedState ->
        if (server.id == servers.activeId.value) surfaces.onState(state)
    }
    val push = Push(app, repository, prefs, scope)
    val updater = Updater(app, api.http, prefs)
}

class VigieApp : Application() {
    lateinit var container: AppContainer
        private set

    override fun onCreate() {
        super.onCreate()
        instance = this
        container = AppContainer(this)
        Notifications.createChannels(this)
        ProcessLifecycleOwner.get().lifecycle.addObserver(object : DefaultLifecycleObserver {
            override fun onStart(owner: LifecycleOwner) = container.repository.setForeground(true)
            override fun onStop(owner: LifecycleOwner) = container.repository.setForeground(false)
        })
        container.surfaces.schedulePeriodic()
        container.scope.launch { container.push.syncToken() }
    }

    companion object {
        lateinit var instance: VigieApp
            private set
    }
}

val appContainer: AppContainer get() = VigieApp.instance.container
