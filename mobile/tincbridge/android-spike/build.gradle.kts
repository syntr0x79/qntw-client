plugins { id("com.android.application") version "8.9.2" }

layout.buildDirectory.set(file(providers.gradleProperty("spikeBuildDir").get()))
android {
    namespace = "link.quantway.tincspike"
    compileSdk = 35
    defaultConfig {
        applicationId = "link.quantway.tincspike"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "0.0.1-spike"
        ndk { abiFilters += listOf("arm64-v8a") }
    }
    compileOptions { sourceCompatibility = JavaVersion.VERSION_17; targetCompatibility = JavaVersion.VERSION_17 }
    sourceSets.getByName("main").jniLibs.srcDir(providers.gradleProperty("spikeNativeDir").get())
    packaging { jniLibs { useLegacyPackaging = true; keepDebugSymbols += "**/*.so" } }
}
