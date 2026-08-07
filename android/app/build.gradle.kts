import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// Upload-key credentials come from keystore.properties, which is gitignored: a
// key committed to the repo is a key in the hands of everyone who clones it, and
// for a Play upload key that is unrecoverable — losing control of it means the
// app can never be updated under this listing again.
val keystoreProperties = Properties().apply {
    val file = rootProject.file("keystore.properties")
    if (file.exists()) file.inputStream().use { load(it) }
}
val hasUploadKey = keystoreProperties.containsKey("storeFile")

android {
    namespace = "com.nuxcor.hallclock"
    // minSdk 26 keeps the launcher icon adaptive-only (no legacy PNG set). Older
    // phones are also the ones that cannot resolve .local names, so they gain
    // little from this app anyway — see the README.
    compileSdk = 36

    defaultConfig {
        applicationId = "com.nuxcor.hallclock"
        minSdk = 26
        // targetSdk stays a release behind compileSdk: 36 makes Android 16's
        // edge-to-edge non-optional, and the inset handling in MainActivity has
        // only been checked on hardware up to 35. Bump it with a phone in hand.
        targetSdk = 35
        versionCode = 1
        versionName = "1.0"
    }

    signingConfigs {
        if (hasUploadKey) {
            create("release") {
                storeFile = rootProject.file(keystoreProperties.getProperty("storeFile"))
                storePassword = keystoreProperties.getProperty("storePassword")
                keyAlias = keystoreProperties.getProperty("keyAlias")
                keyPassword = keystoreProperties.getProperty("keyPassword")
            }
        }
    }

    buildTypes {
        release {
            // No keystore.properties leaves the release unsigned, and Play
            // rejects it. That is the right failure: loud at upload, rather
            // than a build that quietly falls back to the debug key and
            // produces an artifact that can never be published.
            if (hasUploadKey) signingConfig = signingConfigs.getByName("release")
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
}

dependencies {
    // Held at the newest versions that still build against compileSdk 36. Lint
    // will keep offering 1.19.0/1.13.0; those need compileSdk 37, which is past
    // AGP 8.13's ceiling, so taking them means moving the whole toolchain.
    implementation("androidx.core:core-ktx:1.18.0")
    implementation("androidx.activity:activity-ktx:1.10.1")
}
