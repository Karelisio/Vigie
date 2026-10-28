package com.karelisio.vigie.wear

import android.app.Application
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.karelisio.vigie.wear.ui.WearApp
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent { WearApp() }
    }
}

class StatusViewModel(app: Application) : AndroidViewModel(app) {
    val state = WatchData.state

    private val _refreshing = MutableStateFlow(false)
    val refreshing: StateFlow<Boolean> = _refreshing.asStateFlow()

    init {
        viewModelScope.launch { WatchData.load(app) }
    }

    /** Rafraîchit, sauf si les données ont moins de [maxAgeMs] (0 : toujours). */
    fun refresh(maxAgeMs: Long = 0) {
        if (_refreshing.value) return
        _refreshing.value = true
        viewModelScope.launch {
            try {
                WatchData.refresh(getApplication(), WatchData.Surface.App, maxAgeMs)
            } finally {
                _refreshing.value = false
            }
        }
    }
}
