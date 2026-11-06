package com.bluegrass.client

import android.content.Context
import android.content.ContextWrapper
import android.test.InstrumentationTestCase
import java.io.File
import java.security.KeyStore
import java.util.UUID

/** Runs against real Android files and AndroidKeyStore, without launching Flutter. */
@Suppress("DEPRECATION")
class SecureSessionStorageTest : InstrumentationTestCase() {
    private lateinit var directory: File
    private lateinit var isolatedContext: Context
    private lateinit var storage: SecureSessionStorage
    private lateinit var keyAlias: String

    override fun setUp() {
        super.setUp()
        val namespace = "com.bluegrass.client.storage_test.${UUID.randomUUID()}"
        directory = File(instrumentation.targetContext.noBackupFilesDir, namespace)
        check(directory.mkdir())
        isolatedContext = object : ContextWrapper(instrumentation.targetContext) {
            override fun getApplicationContext(): Context = this
            override fun getNoBackupFilesDir(): File = directory
            override fun getPackageName(): String = namespace
        }
        keyAlias = "$namespace.session.v1"
        storage = SecureSessionStorage(isolatedContext)
    }

    override fun tearDown() {
        try {
            KeyStore.getInstance("AndroidKeyStore").apply { load(null) }.deleteEntry(keyAlias)
        } finally {
            directory.deleteRecursively()
            super.tearDown()
        }
    }

    fun testMissingSessionAndDefaultTheme() {
        assertNull(storage.read())
        assertEquals("system", storage.themeRead())
    }

    fun testRoundTripSurvivesNewStorageInstanceWithoutPlaintextOnDisk() {
        val value = "{\"token\":\"fixture-session-only\",\"label\":\"蓝草🌱\"}"
        storage.replace(value)
        assertEquals(value, SecureSessionStorage(isolatedContext).read())
        assertFalse(sessionFile().readBytes().toString(Charsets.UTF_8).contains("fixture-session-only"))
        assertFalse(File(directory, "session.enc.tmp").exists())
    }

    fun testReplacementUsesFreshIvAndReplacesWholeValue() {
        storage.replace("same-session")
        val first = sessionFile().readBytes()
        storage.replace("same-session")
        assertFalse(first.contentEquals(sessionFile().readBytes()))
        storage.replace("short")
        assertEquals("short", SecureSessionStorage(isolatedContext).read())
    }

    fun testEmptyStringIsPresentUntilCleared() {
        storage.replace("")
        assertEquals("", storage.read())
        storage.clear()
        assertNull(storage.read())
    }

    fun testClearIsIdempotentAndPreservesInstallationPreferences() {
        val id = storage.deviceId()
        storage.themeWrite("dark")
        storage.replace("first-session")
        storage.clear()
        storage.clear()
        val reopened = SecureSessionStorage(isolatedContext)
        assertNull(reopened.read())
        assertEquals(id, reopened.deviceId())
        assertEquals("dark", reopened.themeRead())
        assertFalse(KeyStore.getInstance("AndroidKeyStore").apply { load(null) }.containsAlias(keyAlias))
        reopened.replace("new-session")
        assertEquals("new-session", reopened.read())
    }

    fun testCorruptCiphertextFailsClosedWithoutDestroyingEvidence() {
        storage.replace("authenticated-session")
        val corrupted = sessionFile().readBytes()
        corrupted[corrupted.lastIndex] = (corrupted.last().toInt() xor 1).toByte()
        sessionFile().writeBytes(corrupted)
        assertStorageError { storage.read() }
        assertTrue(corrupted.contentEquals(sessionFile().readBytes()))
        storage.clear()
        assertNull(storage.read())
    }

    fun testTruncatedOrUnknownEnvelopeIsNotTreatedAsMissing() {
        storage.replace("session")
        val original = sessionFile().readBytes()
        for (length in listOf(0, 1, 15, original.size - 1)) {
            sessionFile().writeBytes(original.copyOf(length))
            assertStorageError { storage.read() }
        }
        original[0] = (original[0].toInt() xor 1).toByte()
        sessionFile().writeBytes(original)
        assertStorageError { storage.read() }
    }

    fun testPlaintextLimitIsUtf8BytesAndRejectedWritePreservesPreviousValue() {
        val boundary = "a".repeat(262144)
        storage.replace(boundary)
        assertEquals(boundary, storage.read())
        assertInvalidArgument { storage.replace(boundary + "a") }
        assertEquals(boundary, storage.read())
        val unicodeBoundary = "界".repeat(87381) + "a"
        storage.replace(unicodeBoundary)
        assertEquals(unicodeBoundary, storage.read())
        assertInvalidArgument { storage.replace("界".repeat(87382)) }
        assertEquals(unicodeBoundary, storage.read())
    }

    fun testOversizedCiphertextFailsClosed() {
        sessionFile().writeBytes(ByteArray(262177))
        assertStorageError { storage.read() }
    }

    fun testMissingKeyDoesNotSilentlyGenerateReplacementOrForgetSession() {
        storage.replace("key-loss-session")
        val original = sessionFile().readBytes()
        KeyStore.getInstance("AndroidKeyStore").apply { load(null) }.deleteEntry(keyAlias)
        assertStorageError { storage.read() }
        assertStorageError { storage.replace("replacement") }
        assertTrue(original.contentEquals(sessionFile().readBytes()))
        storage.clear()
        storage.replace("after-explicit-clear")
        assertEquals("after-explicit-clear", storage.read())
    }

    fun testFailedReplacementPreservesCommittedSession() {
        storage.replace("committed-session")
        blockTemporaryFile("session.enc")
        assertStorageError { storage.replace("uncommitted-session") }
        assertEquals("committed-session", SecureSessionStorage(isolatedContext).read())
    }

    fun testFailedClearIsReportedAndCanBeRetried() {
        storage.replace("session")
        val blocker = blockTemporaryFile("session.enc")
        assertStorageError { storage.clear() }
        assertEquals("session", storage.read())
        assertTrue(blocker.deleteRecursively())
        storage.clear()
        assertNull(storage.read())
    }

    fun testInterruptedTemporaryWriteIsNeverRestoredAsASession() {
        File(directory, "session.enc.tmp").writeBytes(ByteArray(64) { 42 })
        assertNull(storage.read())
        storage.clear()
        assertFalse(File(directory, "session.enc.tmp").exists())
    }

    fun testInstallationIdIsCanonicalUuidV4AndSurvivesReopeningAndSessionClear() {
        val id = storage.deviceId()
        val uuid = UUID.fromString(id)
        assertEquals(4, uuid.version())
        assertEquals(2, uuid.variant())
        assertEquals(uuid.toString(), id)
        assertEquals(id, SecureSessionStorage(isolatedContext).deviceId())
        storage.clear()
        assertEquals(id, storage.deviceId())
    }

    fun testThemeValuesPersistAndInvalidValueDoesNotReplaceCurrentTheme() {
        for (theme in listOf("dark", "light", "system")) {
            storage.themeWrite(theme)
            assertEquals(theme, SecureSessionStorage(isolatedContext).themeRead())
        }
        storage.themeWrite("dark")
        assertInvalidArgument { storage.themeWrite("auto") }
        assertEquals("dark", storage.themeRead())
    }

    fun testFreshRoutingModePersistsBeforeInstallationIdIsCreated() {
        assertEquals("bypass_cn", storage.routingModeRead())
        assertFalse(File(directory, "device-id").exists())
        assertEquals("bypass_cn", File(directory, "routing-mode").readText())
        storage.deviceId()
        assertEquals("bypass_cn", SecureSessionStorage(isolatedContext).routingModeRead())
        assertFalse(File(directory, "routing-mode.tmp").exists())
    }

    fun testExistingInstallationWithoutRoutingModeMigratesToGlobal() {
        val id = storage.deviceId()
        assertEquals("global", storage.routingModeRead())
        assertEquals("global", File(directory, "routing-mode").readText())
        val reopened = SecureSessionStorage(isolatedContext)
        assertEquals("global", reopened.routingModeRead())
        assertEquals(id, reopened.deviceId())
    }

    fun testRoutingChoiceSurvivesReopeningAndLogout() {
        storage.deviceId()
        storage.replace("routing-session")
        for (mode in listOf("global", "bypass_cn")) {
            storage.routingModeWrite(mode)
            storage.clear()
            val reopened = SecureSessionStorage(isolatedContext)
            assertNull(reopened.read())
            assertEquals(mode, reopened.routingModeRead())
        }
    }

    fun testInvalidRoutingWritesPreserveCommittedChoice() {
        storage.routingModeWrite("bypass_cn")
        for (value in listOf("", "auto", "GLOBAL", "global ", "bypass-cn")) {
            assertInvalidArgument { storage.routingModeWrite(value) }
            assertEquals("bypass_cn", SecureSessionStorage(isolatedContext).routingModeRead())
        }
    }

    fun testCorruptRoutingFileFailsClosedWithoutMigratingOrOverwriting() {
        val file = File(directory, "routing-mode")
        val invalid = listOf("", "auto", "GLOBAL", "global ", "bypass-cn", "x".repeat(10))
            .map { it.toByteArray(Charsets.UTF_8) } + listOf(byteArrayOf(0xC3.toByte(), 0x28))
        for (bytes in invalid) {
            file.writeBytes(bytes)
            assertStorageError { SecureSessionStorage(isolatedContext).routingModeRead() }
            assertTrue(bytes.contentEquals(file.readBytes()))
        }
        assertFalse(File(directory, "device-id").exists())
    }

    fun testFreshRoutingMigrationFailureIsReportedAndCanBeRetried() {
        val blocker = blockTemporaryFile("routing-mode")
        assertStorageError { storage.routingModeRead() }
        assertFalse(File(directory, "routing-mode").exists())
        assertFalse(File(directory, "device-id").exists())
        assertTrue(blocker.deleteRecursively())
        assertEquals("bypass_cn", SecureSessionStorage(isolatedContext).routingModeRead())
    }

    fun testLegacyRoutingMigrationFailureDoesNotPublishOrLoseLegacyIdentity() {
        val id = storage.deviceId()
        val blocker = blockTemporaryFile("routing-mode")
        assertStorageError { storage.routingModeRead() }
        assertFalse(File(directory, "routing-mode").exists())
        assertEquals(id, storage.deviceId())
        assertTrue(blocker.deleteRecursively())
        assertEquals("global", SecureSessionStorage(isolatedContext).routingModeRead())
    }

    fun testFailedRoutingWritePreservesCommittedChoice() {
        storage.routingModeWrite("global")
        blockTemporaryFile("routing-mode")
        assertStorageError { storage.routingModeWrite("bypass_cn") }
        assertEquals("global", SecureSessionStorage(isolatedContext).routingModeRead())
    }

    fun testInterruptedRoutingWriteIsNotPromotedDuringMigration() {
        File(directory, "routing-mode.tmp").writeText("global")
        assertEquals("bypass_cn", storage.routingModeRead())
        assertFalse(File(directory, "routing-mode.tmp").exists())
    }

    fun testFailedPreferenceWritesDoNotPublishUnpersistedValues() {
        storage.themeWrite("light")
        blockTemporaryFile("theme")
        assertStorageError { storage.themeWrite("dark") }
        assertEquals("light", SecureSessionStorage(isolatedContext).themeRead())
        val blocker = blockTemporaryFile("device-id")
        assertStorageError { storage.deviceId() }
        assertFalse(File(directory, "device-id").exists())
        assertTrue(blocker.deleteRecursively())
        val id = storage.deviceId()
        assertEquals(id, SecureSessionStorage(isolatedContext).deviceId())
    }

    private fun sessionFile() = File(directory, "session.enc")

    private fun blockTemporaryFile(name: String): File {
        val blocker = File(directory, "$name.tmp")
        check(blocker.mkdir())
        File(blocker, "block-delete").writeText("fixture")
        return blocker
    }

    private fun assertStorageError(operation: () -> Any?) {
        try {
            operation()
            fail("Expected a fixed storage_error")
        } catch (error: SecureSessionStorage.StorageException) {
            assertEquals("storage_error", error.message)
            assertNull(error.cause)
        }
    }

    private fun assertInvalidArgument(operation: () -> Any?) {
        try {
            operation()
            fail("Expected invalid_argument")
        } catch (error: SecureSessionStorage.InvalidArgumentException) {
            assertEquals("invalid_argument", error.message)
            assertNull(error.cause)
        }
    }
}
