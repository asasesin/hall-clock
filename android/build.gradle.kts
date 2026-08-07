plugins {
    // 8.13+, not 8.7: earlier lint dies with "IllegalArgumentException: 25.0.2"
    // under the JDK 25 that Android Studio now bundles, and that takes
    // bundleRelease down with it via lintVitalRelease.
    id("com.android.application") version "8.13.0" apply false
    id("org.jetbrains.kotlin.android") version "2.0.21" apply false
}
