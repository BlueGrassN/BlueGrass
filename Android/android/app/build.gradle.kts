plugins {
    id("com.android.application")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

// CI injects a persistent keystore; local test builds retain their debug key.
val releaseSigning = listOf(
    "BLUEGRASS_ANDROID_KEYSTORE",
    "BLUEGRASS_ANDROID_STORE_PASSWORD",
    "BLUEGRASS_ANDROID_KEY_ALIAS",
    "BLUEGRASS_ANDROID_KEY_PASSWORD",
).associateWith { System.getenv(it)?.takeIf(String::isNotBlank) }
val hasReleaseSigning = releaseSigning.values.all { it != null }
require(releaseSigning.values.none { it != null } || hasReleaseSigning) {
    "Android signing configuration is incomplete; provide all four BLUEGRASS_ANDROID_* signing variables."
}
require(System.getenv("BLUEGRASS_REQUIRE_RELEASE_SIGNING") != "true" || hasReleaseSigning) {
    "Publishing requires a persistent Android signing key; debug signing is only for local/PR tests."
}

android {
    namespace = "com.bluegrass.client"
    compileSdk = flutter.compileSdkVersion
    ndkVersion = flutter.ndkVersion

    // Platform JUnit3 instrumentation APIs; no external test dependency is needed.
    useLibrary("android.test.runner", false)
    useLibrary("android.test.base", false)

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    defaultConfig {
        // TODO: Specify your own unique Application ID (https://developer.android.com/studio/build/application-id.html).
        applicationId = "com.bluegrass.client"
        // You can update the following values to match your application needs.
        // For more information, see: https://flutter.dev/to/review-gradle-config.
        minSdk = 26
        targetSdk = flutter.targetSdkVersion
        // Uses the version code from pubspec.yaml. When using split APKs, 1000 * ABI_VERSION
        // is added automatically by Flutter. (https://developer.android.com/studio/build/configure-apk-splits#configure-APK-versions)
        // You can force using the value of versionCode by specifying the `-P force-version-code-ignoring-abi=true`
        // flag during build.
        versionCode = flutter.versionCode
        versionName = flutter.versionName
        testInstrumentationRunner = "android.test.InstrumentationTestRunner"
        // Match build-native.ps1: distribute only the ARM64 phone libraries.
        ndk {
            abiFilters.clear()
            abiFilters += setOf("arm64-v8a")
        }
    }

    signingConfigs {
        if (hasReleaseSigning) {
            create("distribution") {
                storeFile = file(releaseSigning.getValue("BLUEGRASS_ANDROID_KEYSTORE")!!)
                storePassword = releaseSigning.getValue("BLUEGRASS_ANDROID_STORE_PASSWORD")
                keyAlias = releaseSigning.getValue("BLUEGRASS_ANDROID_KEY_ALIAS")
                keyPassword = releaseSigning.getValue("BLUEGRASS_ANDROID_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            signingConfig = signingConfigs.getByName(if (hasReleaseSigning) "distribution" else "debug")
        }
    }

    packaging {
        jniLibs {
            // Direct APK distribution: compress .so files and let Android
            // extract them on install. This reduces the download, not disk use.
            useLegacyPackaging = true
        }
    }
}

kotlin {
    compilerOptions {
        jvmTarget = org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17
    }
}

flutter {
    source = "../.."
}

dependencies {
    // Built by Android/native/core; deliberately no stub/fallback implementation.
    implementation(files("libs/bluegrass.aar"))
}
