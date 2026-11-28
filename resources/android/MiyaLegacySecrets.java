package dev.oneo.nsgo;

import android.content.Context;
import android.content.SharedPreferences;
import android.view.View;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.DataInputStream;
import java.io.DataOutputStream;
import java.io.IOException;
import java.security.KeyStore;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Base64;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;
import javax.crypto.Cipher;
import javax.crypto.spec.GCMParameterSpec;

/** Imports Miya's own MyGo records while running under the same Android identity. */
public final class MiyaLegacySecrets extends View {
    private static final String OLD_STORE = "mygo.secrets";
    private static final String SERVICE = "Miya";
    private static final int MAX_SECRET_SIZE = 1 << 20;
    private boolean started;
    private Listener listener;

    public interface Listener {
        void onMigrationComplete(boolean success);
    }

    public MiyaLegacySecrets(Context context) {
        super(context);
        setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO);
    }

    public void setOnMigrationListener(Listener listener) {
        this.listener = listener;
    }

    public void setStart(boolean start) {
        if (!start || started) return;
        started = true;
        Context context = getContext().getApplicationContext();
        new Thread(() -> {
            boolean success;
            try {
                migrate(context);
                success = true;
            } catch (Exception | LinkageError error) {
                success = false;
            }
            final boolean complete = success;
            post(() -> {
                if (listener != null) listener.onMigrationComplete(complete);
            });
        }, "Miya-pairing-migration").start();
    }

    private static void migrate(Context context) throws Exception {
        SharedPreferences prefs = context.getSharedPreferences(OLD_STORE, Context.MODE_PRIVATE);
        String prefix = context.getPackageName() + "\0";
        ArrayList<String> imported = new ArrayList<>();
        for (Map.Entry<String, ?> old : prefs.getAll().entrySet()) {
            String name = old.getKey();
            if (!name.startsWith(prefix)) continue;
            String account = name.substring(prefix.length());
            if (!miyaAccount(account)) continue;
            if (!ask(context, Protocol.SECRET_READ, account, new byte[0])) {
                byte[] raw = decrypt((String)old.getValue());
                byte[] encoded = null;
                try {
                    encoded = Base64.getEncoder().encode(raw);
                    ask(context, Protocol.SECRET_WRITE, account, encoded);
                } finally {
                    Arrays.fill(raw, (byte)0);
                    if (encoded != null) Arrays.fill(encoded, (byte)0);
                }
            }
            imported.add(name);
        }
        // The native store acknowledges its AtomicFile before the old
        // records are retired. A restart can safely repeat partial work.
        if (!imported.isEmpty()) {
            SharedPreferences.Editor edit = prefs.edit();
            for (String name : imported) edit.remove(name);
            if (!edit.commit()) throw new IOException("Unable to retire the old secure records");
        }
    }

    private static boolean miyaAccount(String account) {
        return account.equals("desktops-v1") || account.equals("desktop-link")
                || account.equals("tunnel-secret") || account.matches("desktop-unlock-v1-[A-Za-z0-9_-]{1,200}");
    }

    private static byte[] decrypt(String saved) throws Exception {
        if (saved.length() > MAX_SECRET_SIZE * 2) throw new IOException("Legacy record exceeds the size limit");
        byte[] encrypted = Base64.getDecoder().decode(saved);
        if (encrypted.length < 28) throw new IOException("Invalid encrypted legacy record");
        KeyStore store = KeyStore.getInstance("AndroidKeyStore");
        store.load(null);
        KeyStore.Entry entry = store.getEntry(OLD_STORE, null);
        if (!(entry instanceof KeyStore.SecretKeyEntry)) throw new IOException("The old keystore key is unavailable");
        Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
        cipher.init(Cipher.DECRYPT_MODE, ((KeyStore.SecretKeyEntry)entry).getSecretKey(), new GCMParameterSpec(128, encrypted, 0, 12));
        return cipher.doFinal(encrypted, 12, encrypted.length - 12);
    }

    // Use the same worker and storage as the Go app. The view emits only success/failure.
    private static boolean ask(Context context, int op, String account, byte[] value) throws Exception {
        ByteArrayOutputStream bytes = new ByteArrayOutputStream();
        DataOutputStream command = new DataOutputStream(bytes);
        command.writeInt(0);
        command.writeByte(op);
        Protocol.writeString(command, SERVICE);
        Protocol.writeString(command, account);
        command.writeInt(value.length);
        command.write(value);
        byte[] payload = bytes.toByteArray();
        CompletableFuture<byte[]> result = new CompletableFuture<>();
        Secrets.answer(context, new DataInputStream(new ByteArrayInputStream(payload)), result::complete);
        byte[] reply = null;
        try {
            reply = result.get(10, TimeUnit.SECONDS);
            DataInputStream input = new DataInputStream(new ByteArrayInputStream(reply));
            if (input.readInt() != 0 || !Protocol.readString(input).isEmpty()) {
                throw new IOException("The native secure store rejected the request");
            }
            return input.readUnsignedByte() != 0;
        } finally {
            if (reply != null) Arrays.fill(reply, (byte)0);
            // On a timeout the native worker may still own the request.
            result.thenRun(() -> Arrays.fill(payload, (byte)0));
        }
    }
}
