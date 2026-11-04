package restoredrill

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chatisp/internal/platform/crypto"
)

func TestRecoveredRingDecryptsAfterSourceIsGone(t *testing.T) {
	src, err := privateTempDir("restoredrill-test-keys-")
	if err != nil {
		t.Fatal(err)
	}
	if err := createKeyMaterial(src); err != nil {
		t.Fatal(err)
	}
	metaDir := t.TempDir()
	if err := sealToFiles(src, metaDir); err != nil {
		t.Fatal(err)
	}
	dump := t.TempDir()
	recovery := filepath.Join(dump, "recovery")
	if err := mkdirExact(recovery, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := copyRegular(src, recovery, "material", 0o600, maxKeyFileBytes); err != nil {
		t.Fatal(err)
	}
	if err := copyRegular(src, recovery, "current", 0o600, 64); err != nil {
		t.Fatal(err)
	}
	if err := disableTree(src); err != nil {
		t.Fatal(err)
	}
	if err := absent(src); err != nil {
		t.Fatal(err)
	}
	dst, err := privateTempDir("restoredrill-test-restored-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = disableTree(dst) })
	if err := copyRegular(recovery, dst, "material", 0o600, maxKeyFileBytes); err != nil {
		t.Fatal(err)
	}
	if err := copyRegular(recovery, dst, "current", 0o600, 64); err != nil {
		t.Fatal(err)
	}
	ring, version, err := loadRecoveredRing(dst)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := os.ReadFile(filepath.Join(metaDir, "cipher"))
	if err != nil {
		t.Fatal(err)
	}
	hash, err := os.ReadFile(filepath.Join(metaDir, "hash"))
	if err != nil {
		t.Fatal(err)
	}
	aad, err := canonicalAAD(crypto.Context{
		TenantID: tenantID.String(),
		Entity:   secretEntity,
		EntityID: connectorID.String(),
		Field:    secretField,
	})
	if err != nil {
		t.Fatal(err)
	}
	row := secretRow{
		TenantID:   tenantID,
		Entity:     secretEntity,
		EntityID:   connectorID,
		Field:      secretField,
		Version:    version,
		Ciphertext: cipher,
		AAD:        aad,
	}
	if err := verifySecret(ring, version, row, secretMeta{ID: secretID, Version: version, SHA256: string(hash)}); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreObjectsAfterSourceIsGone(t *testing.T) {
	src, err := privateTempDir("restoredrill-test-objects-")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeSyntheticObjects(src); err != nil {
		t.Fatal(err)
	}
	dump := t.TempDir()
	objects := filepath.Join(dump, "objects")
	if err := mkdirExact(objects, 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := writeObjectManifest(src, objects)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeExact(filepath.Join(dump, "objects.manifest"), body, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := disableTree(src); err != nil {
		t.Fatal(err)
	}
	dst, err := privateTempDir("restoredrill-test-restored-objects-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = disableTree(dst) })
	if err := copyObjectTree(dump, dst); err != nil {
		t.Fatal(err)
	}
	metas, err := readObjectManifest(dump)
	if err != nil {
		t.Fatal(err)
	}
	for key, meta := range metas {
		name, err := objectFileName(key)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := readRegular(mustChild(dst, name), maxObjectBytes)
		if err != nil {
			t.Fatal(err)
		}
		got := shaHex(payload)
		if got != meta.SHA256 || int64(len(payload)) != meta.Size {
			t.Fatalf("bytes restaurados divergentes para %s", key)
		}
	}
}

func TestContentAndKeyRefusals(t *testing.T) {
	t.Run("artefato ausente", func(t *testing.T) {
		dir, err := privateTempDir("restoredrill-missing-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = disableTree(dir) })
		_, _, err = loadRecoveredRing(dir)
		if err == nil || !strings.Contains(err.Error(), "ausente") {
			t.Fatalf("ausente: %v", err)
		}
	})

	t.Run("artefato corrompido", func(t *testing.T) {
		dir, err := privateTempDir("restoredrill-corrupt-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = disableTree(dir) })
		marker := []byte("supersecreta-nao-pode-vazar")
		if err := writeExact(mustChild(dir, "material"), marker, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := writeExact(mustChild(dir, "current"), []byte(keyVersion+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err = loadRecoveredRing(dir)
		if err == nil || !strings.Contains(err.Error(), "corrompido") {
			t.Fatalf("corrompido: %v", err)
		}
		if strings.Contains(err.Error(), "supersecreta") {
			t.Fatalf("erro vazou material: %s", err)
		}
	})

	t.Run("permissão indevida", func(t *testing.T) {
		dir, err := privateTempDir("restoredrill-mode-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = disableTree(dir) })
		if err := createKeyMaterial(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(mustChild(dir, "material"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err = loadRecoveredRing(dir)
		if err == nil || !strings.Contains(err.Error(), "permissão") {
			t.Fatalf("permissão: %v", err)
		}
	})

	t.Run("versão e AAD e hash", func(t *testing.T) {
		dir, err := privateTempDir("restoredrill-aad-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = disableTree(dir) })
		if err := createKeyMaterial(dir); err != nil {
			t.Fatal(err)
		}
		ring, err := loadKeyDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		aad := crypto.Context{TenantID: tenantID.String(), Entity: secretEntity, EntityID: connectorID.String(), Field: secretField}
		sealed, err := ring.Encrypt([]byte("segredo-sintetico-de-ensaio"), aad)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := canonicalAAD(aad)
		if err != nil {
			t.Fatal(err)
		}
		row := secretRow{
			TenantID: tenantID, Entity: secretEntity, EntityID: connectorID, Field: secretField,
			Version: sealed.Version, Ciphertext: sealed.Ciphertext, AAD: stored,
		}
		meta := secretMeta{ID: secretID, Version: sealed.Version, SHA256: shaHex([]byte("segredo-sintetico-de-ensaio"))}
		if err := verifySecret(ring, sealed.Version, row, meta); err != nil {
			t.Fatal(err)
		}
		badVersion := row
		badVersion.Version = "outra-v1"
		if err := verifySecret(ring, sealed.Version, badVersion, meta); err == nil || !strings.Contains(err.Error(), "versão") {
			t.Fatalf("versão: %v", err)
		}
		badAAD := row
		badAAD.AAD = "aad-divergente"
		err = verifySecret(ring, sealed.Version, badAAD, meta)
		if err == nil || !strings.Contains(err.Error(), "AAD") {
			t.Fatalf("AAD: %v", err)
		}
		if strings.Contains(err.Error(), "segredo-sintetico") {
			t.Fatal("erro vazou segredo")
		}
		badHash := meta
		badHash.SHA256 = strings.Repeat("ab", 32)
		if err := verifySecret(ring, sealed.Version, row, badHash); err == nil || !strings.Contains(err.Error(), "hash") {
			t.Fatalf("hash: %v", err)
		}
	})

	t.Run("symlink e caminho", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "fora")
		if err := os.WriteFile(outside, []byte("conteudo-externo"), 0o600); err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		link := filepath.Join(dir, "link")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		if _, err := readRegular(link, maxObjectBytes); err == nil {
			t.Fatal("symlink foi seguido")
		}
		if _, err := os.ReadFile(outside); err != nil {
			t.Fatal(err)
		}
		if _, err := confinedChild(dir, "../passwd"); err == nil {
			t.Fatal("caminho fora do temporário foi aceito")
		}
		if _, err := lexicalUnderTemp("/var/lib/postgresql/18/main"); err == nil {
			t.Fatal("destino real foi aceito")
		}
	})

	t.Run("hash divergente no objeto", func(t *testing.T) {
		src, err := privateTempDir("restoredrill-hash-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = disableTree(src) })
		if err := writeSyntheticObjects(src); err != nil {
			t.Fatal(err)
		}
		dump := t.TempDir()
		objects := filepath.Join(dump, "objects")
		if err := mkdirExact(objects, 0o700); err != nil {
			t.Fatal(err)
		}
		body, err := writeObjectManifest(src, objects)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(body), "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, keptObjectKey+" ") {
				fields := strings.Split(line, " ")
				fields[2] = strings.Repeat("cd", 32)
				lines[i] = strings.Join(fields, " ")
			}
		}
		if err := writeExact(filepath.Join(dump, "objects.manifest"), []byte(strings.Join(lines, "\n")), 0o444); err != nil {
			t.Fatal(err)
		}
		dst, err := privateTempDir("restoredrill-hash-dst-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = disableTree(dst) })
		err = copyObjectTree(dump, dst)
		if err == nil || !strings.Contains(err.Error(), "hash") {
			t.Fatalf("hash de objeto: %v", err)
		}
	})

	t.Run("sucesso parcial", func(t *testing.T) {
		_, err := succeed("chatisp_restore_drill_"+strings.Repeat("ab", 16), time.Now(), outcome{content: true, keyRecovered: true})
		if err == nil || !strings.Contains(err.Error(), "incompleto") {
			t.Fatalf("parcial: %v", err)
		}
	})
}

func sealToFiles(keyDir, metaDir string) error {
	ring, err := loadKeyDir(keyDir)
	if err != nil {
		return err
	}
	plain := make([]byte, secretPlainSize)
	if _, err := rand.Read(plain); err != nil {
		return err
	}
	sum := shaHex(plain)
	aad := crypto.Context{TenantID: tenantID.String(), Entity: secretEntity, EntityID: connectorID.String(), Field: secretField}
	sealed, err := ring.Encrypt(plain, aad)
	for i := range plain {
		plain[i] = 0
	}
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(metaDir, "cipher"), sealed.Ciphertext, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(metaDir, "hash"), []byte(sum), 0o600)
}

func shaHex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
