import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}

// google-services n'est appliqué que si le fichier est présent (fourni par la CI depuis un secret).
val hasFirebase = file("google-services.json").exists()
if (hasFirebase) {
    apply(plugin = "com.google.gms.google-services")
}

val keystoreFile = System.getenv("KEYSTORE_FILE")?.let { file(it) }?.takeIf { it.exists() }

android {
    namespace = "com.karelisio.vigie"
    compileSdk = 37

    defaultConfig {
        applicationId = "com.karelisio.vigie"
        minSdk = 26
        targetSdk = 36
        versionCode = System.getenv("VERSION_CODE")?.toIntOrNull() ?: 1
        versionName = System.getenv("VERSION_NAME") ?: "0.1.0-dev"
        buildConfigField("String", "GITHUB_REPO", "\"Karelisio/Vigie\"")
        buildConfigField("boolean", "FIREBASE_ENABLED", hasFirebase.toString())
    }

    signingConfigs {
        if (keystoreFile != null) {
            create("release") {
                storeFile = keystoreFile
                storePassword = System.getenv("KEYSTORE_PASSWORD")
                keyAlias = System.getenv("KEY_ALIAS")
                keyPassword = System.getenv("KEY_PASSWORD")
                enableV2Signing = true
                enableV3Signing = true
            }
        }
    }

    buildTypes {
        release {
            // Téléphones uniquement : les bibliothèques natives x86 (émulateurs) alourdiraient l'APK de 12 Mo.
            ndk { abiFilters += setOf("arm64-v8a", "armeabi-v7a") }
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            signingConfig = signingConfigs.findByName("release") ?: signingConfigs.getByName("debug")
        }
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    packaging {
        resources.excludes += setOf("/META-INF/{AL2.0,LGPL2.1}", "/META-INF/versions/9/OSGI-INF/MANIFEST.MF")
    }

    dependenciesInfo {
        includeInApk = false
        includeInBundle = false
    }
}

kotlin {
    compilerOptions {
        jvmTarget.set(JvmTarget.JVM_17)
        optIn.addAll(
            "androidx.compose.material3.ExperimentalMaterial3Api",
            "androidx.compose.material3.ExperimentalMaterial3ExpressiveApi",
            "androidx.compose.animation.ExperimentalSharedTransitionApi",
            "androidx.compose.foundation.ExperimentalFoundationApi",
            "kotlinx.serialization.ExperimentalSerializationApi",
        )
    }
}

dependencies {
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.splashscreen)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.process)
    implementation(libs.androidx.navigation.compose)

    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.ui.graphics)
    implementation(libs.compose.foundation)
    implementation(libs.compose.animation)
    implementation(libs.compose.material3)
    implementation(libs.compose.material.icons)
    implementation(libs.compose.ui.tooling.preview)
    debugImplementation(libs.compose.ui.tooling)

    implementation(libs.glance.appwidget)
    implementation(libs.glance.material3)
    implementation(libs.security.crypto)
    implementation(libs.work.runtime)
    implementation(libs.datastore.preferences)

    implementation(libs.camera.camera2)
    implementation(libs.camera.lifecycle)
    implementation(libs.camera.view)
    implementation(libs.mlkit.barcode)

    implementation(platform(libs.firebase.bom))
    implementation(libs.firebase.messaging)

    implementation(libs.okhttp)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.vico.compose.m3)

    testImplementation(libs.junit)
}
