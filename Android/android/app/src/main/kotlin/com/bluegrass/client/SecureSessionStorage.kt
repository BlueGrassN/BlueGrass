package com.bluegrass.client

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.system.Os
import android.system.OsConstants
import java.io.ByteArrayOutputStream
import java.io.File
import java.nio.ByteBuffer
import java.nio.CharBuffer
import java.nio.charset.CodingErrorAction
import java.nio.file.Files
import java.nio.file.NoSuchFileException
import java.security.KeyStore
import java.util.UUID
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Blocking, single-process storage. All callers must use MainActivity's shared serial executor.
 * No method caches a successful write before it reaches disk, including local preferences.
 */
class SecureSessionStorage(context: Context) {
    private val context = context.applicationContext
    private val directory: File get() = context.noBackupFilesDir
    private val keyAlias: String get() = "${context.packageName}.session.v1"

    class StorageException : Exception("storage_error")
    class InvalidArgumentException : IllegalArgumentException("invalid_argument")

    fun read(): String? = storageOperation {
        val envelope = readBounded(File(directory, SESSION_FILE), MAX_ENVELOPE_BYTES)
            ?: return@storageOperation null
        if (envelope.size < ENVELOPE_OVERHEAD ||
            !envelope.copyOfRange(0, HEADER.size).contentEquals(HEADER)
        ) {
            throw StorageException()
        }
        val iv = envelope.copyOfRange(HEADER.size, HEADER.size + IV_BYTES)
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.DECRYPT_MODE, sessionKey(create = false), GCMParameterSpec(TAG_BITS, iv))
        cipher.updateAAD(HEADER)
        val plaintext = cipher.doFinal(envelope, HEADER.size + IV_BYTES,
            envelope.size - HEADER.size - IV_BYTES)
        try {
            decodeUtf8(plaintext)
        } finally {
            plaintext.fill(0)
        }
    }

    fun replace(value: String) {
        // Check UTF-16 length first to bound the UTF-8 encoder's allocation.
        if (value.length > MAX_SESSION_BYTES) throw InvalidArgumentException()
        val plaintext = try {
            val encoded = Charsets.UTF_8.newEncoder()
                .onMalformedInput(CodingErrorAction.REPORT)
                .onUnmappableCharacter(CodingErrorAction.REPORT)
                .encode(CharBuffer.wrap(value))
            if (encoded.remaining() > MAX_SESSION_BYTES) throw InvalidArgumentException()
            ByteArray(encoded.remaining()).also { encoded.get(it) }
        } catch (_: Exception) {
            throw InvalidArgumentException()
        }
        try {
            storageOperation {
                // Never replace a lost key while its ciphertext still exists. Explicit clear
                // is the recovery path, so key loss cannot silently look like a signed-out user.
                val sessionFile = File(directory, SESSION_FILE)
                val hasSession = readBounded(sessionFile, MAX_ENVELOPE_BYTES) != null
                val cipher = Cipher.getInstance(TRANSFORMATION)
                cipher.init(Cipher.ENCRYPT_MODE, sessionKey(create = !hasSession))
                val iv = cipher.iv
                if (iv.size != IV_BYTES) throw StorageException()
                cipher.updateAAD(HEADER)
                val encrypted = cipher.doFinal(plaintext)
                writeDurably(sessionFile, HEADER + iv + encrypted)
            }
        } finally {
            plaintext.fill(0)
        }
    }

    fun clear(): Unit = storageOperation {
        val sessionFile = File(directory, SESSION_FILE)
        // Remove staging first. A failed clear must be reported, and no staging file is
        // ever promoted during reads. Retrying clear also retries the directory fsync.
        Files.deleteIfExists(temporaryFile(sessionFile).toPath())
        Files.deleteIfExists(sessionFile.toPath())
        syncStorageDirectory()
        val keyStore = openKeyStore()
        keyStore.deleteEntry(keyAlias)
    }

    fun deviceId(): String = storageOperation {
        val file = File(directory, DEVICE_ID_FILE)
        val stored = readBounded(file, 36)
        if (stored != null) {
            val value = decodeUtf8(stored)
            val uuid = UUID.fromString(value)
            if (uuid.version() != 4 || uuid.variant() != 2 || uuid.toString() != value) {
                throw StorageException()
            }
            value
        } else {
            val value = UUID.randomUUID().toString()
            writeDurably(file, value.toByteArray(Charsets.UTF_8))
            value
        }
    }

    fun themeRead(): String = storageOperation {
        val stored = readBounded(File(directory, THEME_FILE), 6)
            ?: return@storageOperation "system"
        val value = decodeUtf8(stored)
        if (value !in THEMES) throw StorageException()
        value
    }

    fun themeWrite(value: String) {
        if (value !in THEMES) throw InvalidArgumentException()
        storageOperation {
            writeDurably(File(directory, THEME_FILE), value.toByteArray(Charsets.UTF_8))
        }
    }

    /** Read before deviceId(): the existing installation record distinguishes legacy devices. */
    fun routingModeRead(): String = storageOperation {
        val file = File(directory, ROUTING_MODE_FILE)
        val stored = readBounded(file, 9)
        if (stored != null) {
            val value = decodeUtf8(stored)
            if (value !in ROUTING_MODES) throw StorageException()
            value
        } else {
            val legacy = readBounded(File(directory, DEVICE_ID_FILE), 36) != null
            val value = if (legacy) "global" else "bypass_cn"
            // Persist the migration before returning it, including the fresh-install default.
            // Do not generate a device record here or silently recover corrupt preferences.
            writeDurably(file, value.toByteArray(Charsets.UTF_8))
            value
        }
    }

    fun routingModeWrite(value: String) {
        if (value !in ROUTING_MODES) throw InvalidArgumentException()
        storageOperation {
            writeDurably(File(directory, ROUTING_MODE_FILE), value.toByteArray(Charsets.UTF_8))
        }
    }

    private fun sessionKey(create: Boolean): SecretKey {
        val keyStore = openKeyStore()
        if (keyStore.containsAlias(keyAlias)) {
            return keyStore.getKey(keyAlias, null) as? SecretKey ?: throw StorageException()
        }
        if (!create) throw StorageException()
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE).apply {
            init(KeyGenParameterSpec.Builder(keyAlias,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setKeySize(256)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setRandomizedEncryptionRequired(true)
                .build())
        }.generateKey()
    }

    private fun openKeyStore(): KeyStore = KeyStore.getInstance(KEYSTORE).apply { load(null) }

    private fun readBounded(file: File, maximum: Int): ByteArray? {
        val input = try {
            Files.newInputStream(file.toPath())
        } catch (_: NoSuchFileException) {
            return null
        }
        return input.use {
            val output = ByteArrayOutputStream(minOf(maximum, 4096))
            val buffer = ByteArray(4096)
            while (true) {
                val count = it.read(buffer)
                if (count < 0) break
                if (output.size() + count > maximum) throw StorageException()
                output.write(buffer, 0, count)
            }
            output.toByteArray()
        }
    }

    private fun writeDurably(file: File, bytes: ByteArray) {
        val temporary = temporaryFile(file)
        try {
            // The staging file is in the same directory/filesystem as the destination.
            // Explicit fsync/rename calls propagate errors; AtomicFile.finishWrite() can
            // log sync/rename failures without reporting them to the caller.
            val descriptor = Os.open(temporary.path,
                OsConstants.O_WRONLY or OsConstants.O_CREAT or OsConstants.O_TRUNC or
                    CLOSE_ON_EXEC or OsConstants.O_NOFOLLOW, 0x180) // 0600
            try {
                var offset = 0
                while (offset < bytes.size) {
                    val written = Os.write(descriptor, bytes, offset, bytes.size - offset)
                    if (written <= 0) throw StorageException()
                    offset += written
                }
                Os.fsync(descriptor)
            } finally {
                Os.close(descriptor)
            }
            Os.rename(temporary.path, file.path)
            syncStorageDirectory()
        } finally {
            // A failed write can leave ciphertext staging data, never plaintext sessions.
            // Ignore cleanup failure here so the original write error is retained.
            try {
                Files.deleteIfExists(temporary.toPath())
            } catch (_: Exception) {
                // No secrets, paths, or exception messages are logged.
            }
        }
    }

    private fun syncStorageDirectory() {
        val storageDirectory = directory
        syncDirectory(storageDirectory)
        // Also persist the no-backup directory entry when Android has just created it.
        syncDirectory(storageDirectory.parentFile ?: throw StorageException())
    }

    private fun syncDirectory(file: File) {
        val descriptor = Os.open(file.path,
            OsConstants.O_RDONLY or CLOSE_ON_EXEC, 0)
        try {
            if (!OsConstants.S_ISDIR(Os.fstat(descriptor).st_mode)) throw StorageException()
            Os.fsync(descriptor)
        } finally {
            Os.close(descriptor)
        }
    }

    private fun temporaryFile(file: File) = File(file.parentFile, "${file.name}.tmp")

    private fun decodeUtf8(value: ByteArray): String = Charsets.UTF_8.newDecoder()
        .onMalformedInput(CodingErrorAction.REPORT)
        .onUnmappableCharacter(CodingErrorAction.REPORT)
        .decode(ByteBuffer.wrap(value)).toString()

    private inline fun <T> storageOperation(operation: () -> T): T = try {
        operation()
    } catch (_: Exception) {
        // Never expose provider exceptions, file paths, payloads, or nested causes.
        throw StorageException()
    }

    companion object {
        // Linux/Android kernel flag exists on API 26; the Java field was added in 27.
        private const val CLOSE_ON_EXEC = 0x80000
        const val MAX_SESSION_BYTES = 256 * 1024
        private const val SESSION_FILE = "session.enc"
        private const val DEVICE_ID_FILE = "device-id"
        private const val THEME_FILE = "theme"
        private const val ROUTING_MODE_FILE = "routing-mode"
        private const val KEYSTORE = "AndroidKeyStore"
        private const val TRANSFORMATION = "AES/GCM/NoPadding"
        private const val IV_BYTES = 12
        private const val TAG_BITS = 128
        private const val ENVELOPE_OVERHEAD = 4 + IV_BYTES + TAG_BITS / 8
        private const val MAX_ENVELOPE_BYTES = MAX_SESSION_BYTES + ENVELOPE_OVERHEAD
        private val HEADER = byteArrayOf(0x42, 0x47, 0x53, 0x01)
        private val THEMES = setOf("system", "light", "dark")
        private val ROUTING_MODES = setOf("global", "bypass_cn")
    }
}
