package com.karelisio.vigie

import android.app.Application
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.captureRoboImage
import com.karelisio.vigie.data.CheckHistory
import com.karelisio.vigie.data.CheckResult
import com.karelisio.vigie.data.CheckStatus
import com.karelisio.vigie.data.Connection
import com.karelisio.vigie.data.Container
import com.karelisio.vigie.data.Counts
import com.karelisio.vigie.data.CheckCounts
import com.karelisio.vigie.data.History
import com.karelisio.vigie.data.Issue
import com.karelisio.vigie.data.LiveState
import com.karelisio.vigie.data.Overview
import com.karelisio.vigie.data.SysPoint
import com.karelisio.vigie.data.SystemSnapshot
import com.karelisio.vigie.data.UptimeBucket
import com.karelisio.vigie.ui.checks.CheckCard
import com.karelisio.vigie.ui.checks.Ranges
import com.karelisio.vigie.ui.containers.ContainerCard
import com.karelisio.vigie.ui.containers.StatsCard
import com.karelisio.vigie.ui.home.HomeContent
import com.karelisio.vigie.ui.logs.LogRow
import com.karelisio.vigie.ui.logs.UiLine
import com.karelisio.vigie.ui.logs.levelOf
import com.karelisio.vigie.ui.onboarding.FeaturesPage
import com.karelisio.vigie.ui.onboarding.PermissionPage
import com.karelisio.vigie.ui.onboarding.WelcomePage
import com.karelisio.vigie.ui.settings.Perm
import com.karelisio.vigie.ui.theme.VigieTheme
import org.junit.Assume.assumeTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import kotlin.math.sin

/**
 * Captures d'écran des écrans clés (Robolectric, rendu natif).
 * Lancement : ./gradlew :app:recordRoborazziDebug → app/build/outputs/roborazzi/
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], application = Application::class, qualifiers = "w411dp-h1500dp-xxhdpi")
class ScreenshotTest {
    @get:Rule val compose = createComposeRule()

    @Before
    fun onlyWhenRecording() {
        assumeTrue(System.getProperty("roborazzi.test.record") == "true")
    }

    private val now = System.currentTimeMillis()

    private val system = SystemSnapshot(
        ts = now, cpu = 23.4, cores = listOf(20.0, 31.0, 18.0, 25.0), numCpu = 4,
        memTotal = 24L shl 30, memUsed = (11.2 * (1L shl 30)).toLong(), mem = 46.7,
        diskTotal = 200L shl 30, diskUsed = 97L shl 30, disk = 48.5,
        netRx = 1_450_000.0, netTx = 380_000.0, netRxTotal = 812L shl 30, netTxTotal = 214L shl 30,
        load1 = 0.82, load5 = 0.64, load15 = 0.51, uptime = 23 * 86_400L + 4 * 3600, temp = 47.0, cpuSteal = 1.2,
    )

    private val containers = listOf(
        ctr("aiostreams", "ghcr.io/viren070/aiostreams:latest", cpu = 2.4, mem = 210),
        ctr("comet", "g0ldyy/comet:latest", cpu = 0.8, mem = 145).copy(updateAvailable = true),
        ctr("aiometadata", "ghcr.io/cedya77/aiometadata:latest", cpu = 0.3, mem = 98),
        ctr("prowlarr", "lscr.io/linuxserver/prowlarr", cpu = 0.1, mem = 180),
        ctr("redis", "redis:7-alpine", cpu = 0.2, mem = 12),
        ctr("vigie-agent", "ghcr.io/karelisio/vigie-agent:latest", cpu = 0.4, mem = 9, self = true),
        Container(id = "x1", name = "flaresolverr", image = "flaresolverr/flaresolverr", state = "exited", exitCode = 1, problem = "down"),
        Container(id = "x2", name = "old-backup", image = "alpine", state = "exited", exitCode = 0),
    )

    private fun ctr(name: String, image: String, cpu: Double, mem: Long, self: Boolean = false) = Container(
        id = name, name = name, image = image, state = "running", status = "Up 3 days", health = "healthy",
        cpu = cpu, memUsed = mem shl 20, memLimit = 24L shl 30, startedAt = now - 3 * 86_400_000L - 7_200_000L,
        restartPolicy = "unless-stopped", project = "stremio", self = self, pids = 12, netRx = 12_000.0, netTx = 3_000.0,
    )

    private val checks = listOf(
        check(1, "AIOStreams", "https://aiostreams.15122000.xyz/stremio/manifest.json", true, 182),
        check(2, "Comet", "https://comet.15122000.xyz/manifest.json", true, 96),
        check(3, "AIOMetadata", "https://aiometadata.15122000.xyz/manifest.json", false, 0),
    )

    private fun check(id: Long, name: String, url: String, up: Boolean, lat: Long) = CheckStatus(
        id = id, name = name, url = url, state = if (up) "up" else "down",
        last = CheckResult(now, up, if (up) 200 else 502, lat, if (up) null else "code 502 (attendu 200)"),
        certExpires = now + (if (id == 2L) 9 else 61) * 86_400_000L,
        uptime24h = if (up) 100.0 else 93.75, consecutiveFailures = if (up) 0 else 3,
        recent = List(30) { i -> CheckResult(now - (30 - i) * 60_000L, up || i < 26, 200, lat + (sin(i / 3.0) * 30).toLong()) },
    )

    private fun history(up: Boolean) = CheckHistory(
        buckets = List(48) { i ->
            when {
                i < 4 -> UptimeBucket(0, 0, 0)
                !up && i > 44 -> UptimeBucket(0, 30, 6, 210.0)
                i == 20 -> UptimeBucket(0, 30, 29, 140.0)
                else -> UptimeBucket(0, 30, 30, 120.0)
            }
        },
        uptime = if (up) 99.9 else 93.8,
    )

    private fun overview(status: String, issues: List<Issue>) = Overview(
        status = status, issues = issues, system = system,
        containers = Counts(total = 8, running = 6, stopped = 2, problems = issues.count { it.kind == "container" }),
        checks = CheckCounts(3, if (status == "ok") 3 else 2, if (status == "ok") 0 else 1),
        server = "VPS Oracle", updatedAt = now,
    )

    private fun live(status: String): LiveState {
        val issues = if (status == "ok") emptyList() else listOf(
            Issue("container", "critical", "flaresolverr", "x1", "arrêté (code 1)"),
            Issue("check", "critical", "AIOMetadata", "3", "code 502 (attendu 200)"),
        )
        return LiveState(
            connection = Connection.Live, overview = overview(status, issues), system = system,
            containers = if (status == "ok") containers.filter { !it.hasProblem } else containers,
            checks = if (status == "ok") checks.map { it.copy(state = "up") } else checks,
            updatedAt = now, loaded = true,
            recent = List(60) { i -> system.copy(netRx = 900_000 + sin(i / 5.0) * 600_000, netTx = 300_000 + sin(i / 4.0) * 150_000) },
        )
    }

    private val sysHistory = History(
        bucketMs = 300_000,
        points = List(288) { i ->
            val t = now - (288 - i) * 300_000L
            SysPoint(t, cpu = 18 + 10 * sin(i / 20.0) + if (i in 150..160) 35.0 else 0.0, mem = 44 + 3 * sin(i / 40.0), disk = 48.0, rx = 1e6, tx = 3e5)
        },
    )

    private fun shot(name: String, dark: Boolean = false, content: @Composable () -> Unit) {
        if (dark) org.robolectric.RuntimeEnvironment.setQualifiers("+night")
        compose.mainClock.autoAdvance = false
        compose.setContent {
            VigieTheme(dynamicColor = false) {
                androidx.compose.material3.Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.surface) { Column { content() } }
            }
        }
        compose.mainClock.advanceTimeBy(2_400)
        compose.onRoot().captureRoboImage("build/outputs/roborazzi/$name.png")
    }

    private fun home(status: String) = @Composable {
        HomeContent(
            state = live(status), history = sysHistory, range = "24h", serverName = "VPS Oracle",
            servers = listOf("1" to "VPS Oracle"), onSwitch = {}, onAdd = {}, onRange = {}, onIssue = {}, onContainers = {},
        )
    }

    @Test fun homeOk() = shot("home_ok", content = home("ok"))

    @Test fun homeCritical() = shot("home_critical", content = home("critical"))

    @Test fun homeDark() = shot("home_dark", dark = true, content = home("critical"))

    @Test
    fun containersList() = shot("containers") {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            containers.sortedBy { if (it.hasProblem) 0 else if (it.running) 1 else 2 }.forEach { ContainerCard(it, busy = it.name == "prowlarr", onOpen = {}, favorite = it.name == "aiostreams") }
            StatsCard(containers[0], Modifier.fillMaxWidth())
        }
    }

    @Test
    fun checksCards() = shot("checks") {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            checks.forEachIndexed { i, c ->
                CheckCard(
                    c = c, history = history(c.state == "up"), range = Ranges[0], expanded = i == 2,
                    onToggle = {}, onRun = {}, onEdit = {}, onDelete = {}, modifier = Modifier,
                )
            }
        }
    }

    @Test
    fun logs() = shot("logs", dark = true) {
        val sample = listOf(
            "2026-10-28 10:41:02 INFO  Server listening on :3000",
            "[aiostreams] info: Fetched 128 streams from 6 addons in 842ms",
            "WARN slow upstream response from torrentio (2310 ms)",
            "level=error msg=\"failed to resolve debrid link\" err=\"timeout\"",
            "GET /stremio/manifest.json 200 12ms",
            "DEBUG cache hit key=tt0903747:1:1",
            "Traceback (most recent call last): ValueError: invalid config",
        )
        Column(Modifier.padding(8.dp)) {
            sample.forEachIndexed { i, t ->
                LogRow(UiLine(i.toLong(), now - (7 - i) * 1000L, if (i == 6) "stderr" else "stdout", t, levelOf(t)), query = if (i == 4) "manifest" else "", animate = false, modifier = Modifier, onCopy = {})
            }
        }
    }

    @Test
    fun extras() = shot("extras") {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            com.karelisio.vigie.ui.home.OracleCard(
                com.karelisio.vigie.data.OracleReport(
                    applicable = true, risk = "medium", days = 7.0,
                    cpu = com.karelisio.vigie.data.OracleMetric(8.4, below = true, applies = true),
                    mem = com.karelisio.vigie.data.OracleMetric(46.0, below = false, applies = true),
                    net = com.karelisio.vigie.data.OracleMetric(3.1, below = true, applies = true),
                    netMbps = 4000.0, summary = "Un seul critère maintient l'instance au-dessus du seuil d'inactivité.",
                ),
            )
            fun e(ts: Long, a: String, d: String = "", s: String = "docker") = com.karelisio.vigie.data.TimelineEvent(ts, a, d, s)
            com.karelisio.vigie.ui.containers.TimelineCard(
                listOf(
                    e(now - 600_000, "health", "healthy"),
                    e(now - 640_000, "start"), e(now - 650_000, "die", "1"),
                    e(now - 86_400_000, "restart"), e(now - 86_401_000, "start"), e(now - 86_402_000, "stop"),
                    e(now - 86_402_500, "die", "143"), e(now - 86_403_000, "restart", "ok", "vigie"),
                    e(now - 3 * 86_400_000L, "start"), e(now - 3 * 86_400_000L - 2000, "create"), e(now - 3 * 86_400_000L - 4000, "destroy"),
                ),
            )
        }
    }

    @Test
    fun updates() = shot("updates") {
        val job = com.karelisio.vigie.data.AgentJob(
            id = "1", kind = "stack", action = "update", target = "stremio", phase = "pulling", progress = 0.46,
            message = "comet : téléchargement 62 %",
        )
        val done = com.karelisio.vigie.data.AgentJob(
            id = "2", kind = "stack", action = "update", target = "stremio", phase = "failed", done = true, ok = false, progress = 1.0,
            message = "Échec : 2 mis à jour, 1 déjà à jour, 1 échec",
            results = listOf(
                com.karelisio.vigie.data.JobResult("aiostreams", true, true, "aiostreams mis à jour"),
                com.karelisio.vigie.data.JobResult("comet", true, true, "comet mis à jour"),
                com.karelisio.vigie.data.JobResult("redis", true, false, "redis : déjà à jour"),
                com.karelisio.vigie.data.JobResult("aiometadata", false, false, "le nouveau aiometadata s'est arrêté (code 1) (ancienne version rétablie)"),
            ),
        )
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            com.karelisio.vigie.ui.containers.UpdateCard(
                c = containers[1].copy(imageRef = "g0ldyy/comet:latest"), enabled = true, busy = true, job = job.copy(kind = "update", target = "comet"),
                onUpdate = {}, onPull = {}, onStack = {},
            )
            com.karelisio.vigie.ui.containers.StackHeader("stremio", containers.take(5), updatesEnabled = true, actions = true, busy = false, onAction = {})
            ContainerCard(containers[1], busy = false, onOpen = {})
            com.karelisio.vigie.ui.components.JobBanner(job)
            com.karelisio.vigie.ui.components.SectionCard(title = "Résultat") { com.karelisio.vigie.ui.components.JobProgress(done) }
        }
    }

    @Test
    fun maintenance() = shot("maintenance") {
        Column(Modifier.padding(16.dp)) {
            com.karelisio.vigie.ui.home.MaintenanceBanner(now + 42 * 60_000L, onStop = {})
        }
    }

    @Test fun lockScreen() = shot("lock", dark = true) { com.karelisio.vigie.security.LockScreen(visible = true) {} }

    @Test fun onboardingWelcome() = shot("onboarding_welcome") { WelcomePage(active = true) }

    @Test fun onboardingFeatures() = shot("onboarding_features", dark = true) { FeaturesPage(active = true) }

    @Test fun onboardingPermission() = shot("onboarding_permission") { PermissionPage(Perm.Notifications, granted = false) {} }

    @Test
    fun insights() = shot("insights") {
        val tb = 1_000_000_000_000L
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            com.karelisio.vigie.ui.home.TrafficCard(
                com.karelisio.vigie.data.TrafficReport(
                    month = "2026-10", tx = 7_840 * tb / 1000, rx = 1_210 * tb / 1000, quota = 10 * tb, pct = 78.4,
                    projectedTx = 9_920 * tb / 1000, daysLeft = 3.4,
                    history = listOf(5_100, 6_300, 10_400, 7_200, 6_900, 7_840).mapIndexed { i, tx ->
                        com.karelisio.vigie.data.TrafficMonth("2026-%02d".format(5 + i), tx * tb / 1000, tx * tb / 6000)
                    },
                ),
            )
            com.karelisio.vigie.ui.home.TopCard(
                consumers = listOf(
                    com.karelisio.vigie.data.Consumer("aiostreams", 2.4, 18.0, 210L shl 20, 4L shl 30, 9L shl 30),
                    com.karelisio.vigie.data.Consumer("comet", 0.8, 31.0, 145L shl 20, 2L shl 30, 3L shl 30),
                    com.karelisio.vigie.data.Consumer("prowlarr", 0.4, 6.0, 180L shl 20, 300L shl 20, 120L shl 20),
                    com.karelisio.vigie.data.Consumer("redis", 0.2, 1.0, 12L shl 20, 900L shl 20, 900L shl 20),
                    com.karelisio.vigie.data.Consumer("vigie-agent", 0.4, 2.0, 9L shl 20, 50L shl 20, 80L shl 20),
                    com.karelisio.vigie.data.Consumer("aiometadata", 0.3, 4.0, 98L shl 20, 1L shl 30, 2L shl 30),
                ),
                containers = containers, range = "24h", onRange = {}, onOpen = {},
            )
        }
    }

    @Test
    fun report() = shot("report", dark = true) {
        fun alert(h: Long, kind: String, title: String) = com.karelisio.vigie.data.Alert(h, now - h * 3_600_000, kind, "critical", "", title)
        com.karelisio.vigie.ui.report.ReportContent(
            report = com.karelisio.vigie.data.WeeklyReport(
                from = now - 7 * 86_400_000L, to = now,
                services = com.karelisio.vigie.data.ReportServices(
                    uptime = 99.42, worst = com.karelisio.vigie.data.ServiceUptime("AIOMetadata", 98.37),
                    list = listOf(
                        com.karelisio.vigie.data.ServiceUptime("AIOMetadata", 98.37),
                        com.karelisio.vigie.data.ServiceUptime("Comet", 99.9),
                        com.karelisio.vigie.data.ServiceUptime("AIOStreams", 100.0),
                    ),
                ),
                incidents = com.karelisio.vigie.data.ReportIncidents(
                    total = 3,
                    byKind = listOf(com.karelisio.vigie.data.IncidentKind("container", "conteneurs", 2), com.karelisio.vigie.data.IncidentKind("check", "services", 1)),
                    recent = listOf(alert(5, "container", "flaresolverr est tombé"), alert(30, "check", "AIOMetadata ne répond plus"), alert(52, "container", "comet manque de mémoire")),
                ),
                containers = com.karelisio.vigie.data.ReportContainers(
                    crashes = 2, restarts = 1,
                    list = listOf(com.karelisio.vigie.data.ContainerIncidents("flaresolverr", 1, 0), com.karelisio.vigie.data.ContainerIncidents("comet", 1, 1)),
                ),
                updates = listOf("comet", "redis"),
                disk = com.karelisio.vigie.data.ReportDisk(48.5, 97L shl 30, 200L shl 30, 1.3),
                traffic = com.karelisio.vigie.data.ReportTraffic(310_000_000_000, 1_840_000_000_000, 7_840_000_000_000, 10_000_000_000_000, 78.4),
            ),
            error = null, containers = containers, diskThreshold = 90.0, onBack = {}, onRetry = {}, onContainer = {}, onUpdates = {},
        )
    }
}
