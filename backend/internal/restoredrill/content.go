package restoredrill

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/crypto"
)

// rower is the subset of pgx used to read restored rows.
type rower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const (
	objectsFormat = "format restoredrill-objects-1"
	secretsFormat = "format restoredrill-secrets-1"
	secretEntity  = "channel_connector"
	secretField   = "access_token"

	maxObjectBytes   = 64 * 1024
	maxObjects       = 8
	maxSecrets       = 8
	maxKeyFileBytes  = 4096
	maxDumpFileBytes = 8 << 20
	maxManifestBytes = 64 * 1024

	keptObjectSize    = 128
	removedObjectSize = 96
	secretPlainSize   = 24
)

type objectMeta struct {
	Key     string
	Version string
	SHA256  string
	Size    int64
}

type secretMeta struct {
	ID      uuid.UUID
	Version string
	SHA256  string
}

type secretRow struct {
	TenantID   uuid.UUID
	Entity     string
	EntityID   uuid.UUID
	Field      string
	Version    string
	Ciphertext []byte
	AAD        string
}

type scratch struct {
	objects string
	keys    string
}

func newScratch() (*scratch, error) {
	objects, err := privateTempDir("restoredrill-objects-")
	if err != nil {
		return nil, err
	}
	keys, err := privateTempDir("restoredrill-keys-")
	if err != nil {
		_ = disableTree(objects)
		return nil, err
	}
	return &scratch{objects: objects, keys: keys}, nil
}

func (s *scratch) release() {
	if s == nil {
		return
	}
	if s.objects != "" {
		_ = disableTree(s.objects)
		s.objects = ""
	}
	if s.keys != "" {
		_ = disableTree(s.keys)
		s.keys = ""
	}
}

func privateTempDir(prefix string) (string, error) {
	parent, err := tempRoot()
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(parent, prefix)
	if err != nil {
		return "", errors.New("falha ao criar armazenamento temporário")
	}
	pinned, err := pinTempDir(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	if err := os.Chmod(pinned, 0o700); err != nil {
		_ = os.RemoveAll(pinned)
		return "", errors.New("falha ao criar armazenamento temporário")
	}
	if err := requirePrivateDir(pinned); err != nil {
		_ = os.RemoveAll(pinned)
		return "", err
	}
	return pinned, nil
}

func writeSyntheticObjects(dir string) error {
	plans := []struct {
		key  string
		size int
	}{
		{keptObjectKey, keptObjectSize},
		{removedObjectKey, removedObjectSize},
	}
	if len(plans) > maxObjects {
		return errors.New("falha ao gravar o conteúdo sintético do anexo")
	}
	for _, plan := range plans {
		payload := make([]byte, plan.size)
		if _, err := rand.Read(payload); err != nil {
			return errors.New("falha ao gravar o conteúdo sintético do anexo")
		}
		name, err := objectFileName(plan.key)
		if err != nil {
			return err
		}
		path, err := confinedChild(dir, name)
		if err != nil {
			return err
		}
		if err := writeExact(path, payload, 0o600); err != nil {
			return errors.New("falha ao gravar o conteúdo sintético do anexo")
		}
		for i := range payload {
			payload[i] = 0
		}
	}
	return nil
}

func createKeyMaterial(dir string) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return errors.New("falha ao gravar o material sintético da chave")
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	for i := range raw {
		raw[i] = 0
	}
	body := []byte(keyVersion + ":" + encoded + "\n")
	material, err := confinedChild(dir, "material")
	if err != nil {
		return err
	}
	if err := writeExact(material, body, 0o600); err != nil {
		return errors.New("falha ao gravar o material sintético da chave")
	}
	for i := range body {
		body[i] = 0
	}
	current, err := confinedChild(dir, "current")
	if err != nil {
		return err
	}
	if err := writeExact(current, []byte(keyVersion+"\n"), 0o600); err != nil {
		return errors.New("falha ao gravar o material sintético da chave")
	}
	return requireKeyArtifact(dir)
}

func loadKeyDir(dir string) (*crypto.Ring, error) {
	if err := requireKeyArtifact(dir); err != nil {
		return nil, err
	}
	ring, err := crypto.Load("", mustChild(dir, "material"), keyVersion)
	if err != nil || ring == nil || ring.Current() != keyVersion {
		return nil, errors.New("artefato de chave corrompido")
	}
	return ring, nil
}

func encryptFixtureSecrets(ctx context.Context, tx pgx.Tx, keyDir string) error {
	ring, err := loadKeyDir(keyDir)
	if err != nil {
		return err
	}
	version := ring.Current()
	if version != keyVersion {
		return errors.New("versão inconsistente")
	}
	lines := []string{secretsFormat}
	plans := []struct {
		id       uuid.UUID
		entityID uuid.UUID
	}{
		{secretID, connectorID},
		{removedSecretID, removedConnectorID},
	}
	if len(plans) > maxSecrets {
		return errors.New("falha ao gravar o material sintético da chave")
	}
	for _, plan := range plans {
		plain := make([]byte, secretPlainSize)
		if _, err := rand.Read(plain); err != nil {
			return errors.New("falha ao gravar o material sintético da chave")
		}
		sum := sha256.Sum256(plain)
		hash := hex.EncodeToString(sum[:])
		aad := crypto.Context{
			TenantID: tenantID.String(),
			Entity:   secretEntity,
			EntityID: plan.entityID.String(),
			Field:    secretField,
		}
		sealed, err := ring.Encrypt(plain, aad)
		for i := range plain {
			plain[i] = 0
		}
		if err != nil || sealed.Version != version || len(sealed.Ciphertext) == 0 {
			return errors.New("falha ao gravar o material sintético da chave")
		}
		stored, err := canonicalAAD(aad)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO credential_secrets (
				tenant_id, id, entity, entity_id, field, key_version, ciphertext, aad
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, tenantID, plan.id, secretEntity, plan.entityID, secretField, sealed.Version, sealed.Ciphertext, stored); err != nil {
			return fmt.Errorf("falha ao gravar a referência de ensaio: %s", pgMessage(err))
		}
		for i := range sealed.Ciphertext {
			sealed.Ciphertext[i] = 0
		}
		lines = append(lines, plan.id.String()+" "+sealed.Version+" "+hash)
	}
	hashes, err := confinedChild(keyDir, "hashes")
	if err != nil {
		return err
	}
	if err := writeExact(hashes, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return errors.New("falha ao gravar o material sintético da chave")
	}
	return nil
}

func insertAttachments(ctx context.Context, tx pgx.Tx, objects string) error {
	plans := []struct {
		id      uuid.UUID
		request uuid.UUID
		name    string
		key     string
		version string
	}{
		{keptAttachmentID, keptRequestID, "mantido.txt", keptObjectKey, keptObjectVersion},
		{removedAttachmentID, removedRequestID, "removido.txt", removedObjectKey, removedObjectVersion},
	}
	for _, plan := range plans {
		file, err := objectFileName(plan.key)
		if err != nil {
			return err
		}
		payload, err := readRegular(mustChild(objects, file), maxObjectBytes)
		if err != nil {
			return errors.New("falha ao gravar o conteúdo sintético do anexo")
		}
		sum := sha256.Sum256(payload)
		size := len(payload)
		hash := hex.EncodeToString(sum[:])
		for i := range payload {
			payload[i] = 0
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO attachments (
				tenant_id, id, conversation_id, request_id, direction, name,
				expected_bytes, object_key, object_version, expires_at, sha256
			) VALUES ($1, $2, $3, $4, 'incoming', $5, $6, $7, $8, $9, $10)
		`, tenantID, plan.id, conversationID, plan.request, plan.name, size, plan.key, plan.version, fixtureExpiry, hash); err != nil {
			return fmt.Errorf("falha ao gravar a referência de ensaio: %s", pgMessage(err))
		}
	}
	return nil
}

func snapshotBackup(src *scratch, dump string) error {
	if src == nil {
		return errors.New("falha ao gravar o backup dos objetos")
	}
	objectsDir, err := confinedChild(dump, "objects")
	if err != nil {
		return err
	}
	if err := mkdirExact(objectsDir, 0o700); err != nil {
		return errors.New("falha ao gravar o backup dos objetos")
	}
	manifest, err := writeObjectManifest(src.objects, objectsDir)
	if err != nil {
		return err
	}
	manifestPath, err := confinedChild(dump, "objects.manifest")
	if err != nil {
		return err
	}
	if err := writeExact(manifestPath, manifest, 0o444); err != nil {
		return errors.New("falha ao gravar o backup dos objetos")
	}
	recovery, err := confinedChild(dump, "recovery")
	if err != nil {
		return err
	}
	if err := mkdirExact(recovery, 0o700); err != nil {
		return errors.New("falha ao gravar o material sintético da chave")
	}
	if err := copyRegular(src.keys, recovery, "material", 0o600, maxKeyFileBytes); err != nil {
		return errors.New("falha ao gravar o material sintético da chave")
	}
	if err := copyRegular(src.keys, recovery, "current", 0o600, 64); err != nil {
		return errors.New("falha ao gravar o material sintético da chave")
	}
	hashes, err := readRegular(mustChild(src.keys, "hashes"), maxManifestBytes)
	if err != nil {
		return errors.New("falha ao gravar o material sintético da chave")
	}
	secretsPath, err := confinedChild(dump, "secrets.manifest")
	if err != nil {
		return err
	}
	if err := writeExact(secretsPath, hashes, 0o444); err != nil {
		return errors.New("falha ao gravar o material sintético da chave")
	}
	if err := requireKeyArtifact(recovery); err != nil {
		return err
	}
	return ensureMaterialNotLeaked(dump)
}

func writeObjectManifest(srcObjects, dstObjects string) ([]byte, error) {
	plans := []struct {
		key     string
		version string
	}{
		{keptObjectKey, keptObjectVersion},
		{removedObjectKey, removedObjectVersion},
	}
	lines := []string{objectsFormat}
	for _, plan := range plans {
		name, err := objectFileName(plan.key)
		if err != nil {
			return nil, err
		}
		payload, err := readRegular(mustChild(srcObjects, name), maxObjectBytes)
		if err != nil || len(payload) == 0 {
			return nil, errors.New("falha ao gravar o backup dos objetos")
		}
		sum := sha256.Sum256(payload)
		size := len(payload)
		dst, err := confinedChild(dstObjects, name)
		if err != nil {
			return nil, err
		}
		if err := writeExact(dst, payload, 0o444); err != nil {
			return nil, errors.New("falha ao gravar o backup dos objetos")
		}
		for i := range payload {
			payload[i] = 0
		}
		lines = append(lines, strings.Join([]string{
			plan.key,
			plan.version,
			hex.EncodeToString(sum[:]),
			strconv.Itoa(size),
		}, " "))
	}
	body := []byte(strings.Join(lines, "\n") + "\n")
	if len(body) > maxManifestBytes {
		return nil, errors.New("falha ao gravar o backup dos objetos")
	}
	return body, nil
}

func restoreTrees(dump, objectsDir, keyDir string) error {
	if err := copyObjectTree(dump, objectsDir); err != nil {
		return err
	}
	recovery, err := confinedChild(dump, "recovery")
	if err != nil {
		return err
	}
	if err := copyRegular(recovery, keyDir, "material", 0o600, maxKeyFileBytes); err != nil {
		return errors.New("artefato de chave ausente")
	}
	if err := copyRegular(recovery, keyDir, "current", 0o600, 64); err != nil {
		return errors.New("artefato de chave ausente")
	}
	backupMaterial, err := readRegular(mustChild(recovery, "material"), maxKeyFileBytes)
	if err != nil {
		return errors.New("artefato de chave ausente")
	}
	restoredMaterial, err := readRegular(mustChild(keyDir, "material"), maxKeyFileBytes)
	if err != nil {
		return errors.New("artefato de chave ausente")
	}
	same := subtle.ConstantTimeCompare(backupMaterial, restoredMaterial) == 1
	for i := range backupMaterial {
		backupMaterial[i] = 0
	}
	for i := range restoredMaterial {
		restoredMaterial[i] = 0
	}
	if !same {
		return errors.New("artefato de chave corrompido")
	}
	return requireKeyArtifact(keyDir)
}

func copyObjectTree(dump, dst string) error {
	metas, err := readObjectManifest(dump)
	if err != nil {
		return err
	}
	src, err := confinedChild(dump, "objects")
	if err != nil {
		return err
	}
	if err := requireDirNoSymlink(src); err != nil {
		return errors.New("falha ao restaurar os objetos do anexo")
	}
	for _, meta := range metas {
		name, err := objectFileName(meta.Key)
		if err != nil {
			return err
		}
		if err := copyRegular(src, dst, name, 0o400, maxObjectBytes); err != nil {
			return errors.New("falha ao restaurar os objetos do anexo")
		}
		payload, err := readRegular(mustChild(dst, name), maxObjectBytes)
		if err != nil {
			return errors.New("falha ao restaurar os objetos do anexo")
		}
		sum := sha256.Sum256(payload)
		size := int64(len(payload))
		for i := range payload {
			payload[i] = 0
		}
		if hex.EncodeToString(sum[:]) != meta.SHA256 || size != meta.Size {
			return errors.New("hash de conteúdo divergente")
		}
	}
	return nil
}

func loadRecoveredRing(keyDir string) (*crypto.Ring, string, error) {
	if err := requireKeyArtifact(keyDir); err != nil {
		return nil, "", err
	}
	currentBody, err := readRegular(mustChild(keyDir, "current"), 64)
	if err != nil {
		return nil, "", errors.New("artefato de chave ausente")
	}
	version := strings.TrimSpace(string(currentBody))
	if version != keyVersion || strings.ContainsAny(version, "\n\r") {
		return nil, "", errors.New("versão inconsistente")
	}
	ring, err := crypto.Load("", mustChild(keyDir, "material"), version)
	if err != nil || ring == nil || ring.Current() != version {
		return nil, "", errors.New("artefato de chave corrompido")
	}
	return ring, version, nil
}

func verifyAttachmentBytes(ctx context.Context, conn rower, objects string, metas map[string]objectMeta, id uuid.UUID) error {
	var key, version, sum string
	var expected int64
	var conversation uuid.UUID
	err := conn.QueryRow(ctx, `
		SELECT object_key, object_version, sha256, expected_bytes, conversation_id
		FROM attachments
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, id).Scan(&key, &version, &sum, &expected, &conversation)
	if err != nil || conversation != conversationID {
		return errors.New("referência de anexo divergente depois do restore")
	}
	meta, ok := metas[key]
	if !ok || meta.Version != version || meta.SHA256 != sum || meta.Size != expected {
		return errors.New("hash de conteúdo divergente")
	}
	name, err := objectFileName(key)
	if err != nil {
		return err
	}
	payload, err := readRegular(mustChild(objects, name), maxObjectBytes)
	if err != nil {
		return errors.New("falha ao ler o anexo restaurado")
	}
	got := sha256.Sum256(payload)
	size := int64(len(payload))
	for i := range payload {
		payload[i] = 0
	}
	if hex.EncodeToString(got[:]) != sum || size != expected {
		return errors.New("hash de conteúdo divergente")
	}
	return nil
}

func verifySecret(ring *crypto.Ring, version string, row secretRow, meta secretMeta) error {
	if ring == nil || version == "" || meta.ID == uuid.Nil || row.Version != version || meta.Version != version {
		return errors.New("versão inconsistente")
	}
	if meta.Version != row.Version || meta.SHA256 == "" {
		return errors.New("versão inconsistente")
	}
	if len(row.Ciphertext) < 12+16 {
		return errors.New("artefato de chave corrompido")
	}
	aad := crypto.Context{
		TenantID: row.TenantID.String(),
		Entity:   row.Entity,
		EntityID: row.EntityID.String(),
		Field:    row.Field,
	}
	stored, err := canonicalAAD(aad)
	if err != nil || subtle.ConstantTimeCompare([]byte(stored), []byte(row.AAD)) != 1 {
		return errors.New("AAD inconsistente")
	}
	plain, err := ring.Decrypt(row.Version, row.Ciphertext, aad)
	if err != nil {
		return errors.New("falha ao decifrar o segredo recuperado")
	}
	sum := sha256.Sum256(plain)
	for i := range plain {
		plain[i] = 0
	}
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(meta.SHA256)) != 1 {
		return errors.New("hash do segredo divergente")
	}
	return nil
}

func loadSecretRow(ctx context.Context, conn rower, id uuid.UUID) (secretRow, error) {
	var row secretRow
	err := conn.QueryRow(ctx, `
		SELECT tenant_id, entity, entity_id, field, key_version, ciphertext, aad
		FROM credential_secrets
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, id).Scan(&row.TenantID, &row.Entity, &row.EntityID, &row.Field, &row.Version, &row.Ciphertext, &row.AAD)
	if err != nil {
		return secretRow{}, errors.New("referência de chave divergente depois do restore")
	}
	return row, nil
}

func readObjectManifest(dump string) (map[string]objectMeta, error) {
	path, err := confinedChild(dump, "objects.manifest")
	if err != nil {
		return nil, err
	}
	body, err := readRegular(path, maxManifestBytes)
	if err != nil {
		return nil, errors.New("falha ao ler o backup dos objetos")
	}
	return parseObjectManifest(body)
}

func parseObjectManifest(body []byte) (map[string]objectMeta, error) {
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	if len(lines) < 2 || lines[0] != objectsFormat {
		return nil, errors.New("falha ao ler o backup dos objetos")
	}
	if len(lines)-1 > maxObjects {
		return nil, errors.New("falha ao ler o backup dos objetos")
	}
	out := make(map[string]objectMeta, len(lines)-1)
	for _, line := range lines[1:] {
		fields := strings.Split(line, " ")
		if len(fields) != 4 || !validObjectKey(fields[0]) || fields[1] == "" || len(fields[1]) > 64 {
			return nil, errors.New("falha ao ler o backup dos objetos")
		}
		if len(fields[2]) != 64 || !onlyHex(fields[2]) {
			return nil, errors.New("falha ao ler o backup dos objetos")
		}
		size, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil || size <= 0 || size > maxObjectBytes {
			return nil, errors.New("falha ao ler o backup dos objetos")
		}
		if _, exists := out[fields[0]]; exists {
			return nil, errors.New("falha ao ler o backup dos objetos")
		}
		out[fields[0]] = objectMeta{Key: fields[0], Version: fields[1], SHA256: fields[2], Size: size}
	}
	if _, ok := out[keptObjectKey]; !ok {
		return nil, errors.New("falha ao ler o backup dos objetos")
	}
	if _, ok := out[removedObjectKey]; !ok {
		return nil, errors.New("falha ao ler o backup dos objetos")
	}
	return out, nil
}

func readSecretManifest(dump string) (map[uuid.UUID]secretMeta, error) {
	path, err := confinedChild(dump, "secrets.manifest")
	if err != nil {
		return nil, err
	}
	body, err := readRegular(path, maxManifestBytes)
	if err != nil {
		return nil, errors.New("falha ao ler o material da chave")
	}
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	if len(lines) < 2 || lines[0] != secretsFormat || len(lines)-1 > maxSecrets {
		return nil, errors.New("falha ao ler o material da chave")
	}
	out := make(map[uuid.UUID]secretMeta, len(lines)-1)
	for _, line := range lines[1:] {
		fields := strings.Split(line, " ")
		if len(fields) != 3 {
			return nil, errors.New("falha ao ler o material da chave")
		}
		id, err := uuid.Parse(fields[0])
		if err != nil || fields[1] == "" || len(fields[2]) != 64 || !onlyHex(fields[2]) {
			return nil, errors.New("falha ao ler o material da chave")
		}
		if _, exists := out[id]; exists {
			return nil, errors.New("falha ao ler o material da chave")
		}
		out[id] = secretMeta{ID: id, Version: fields[1], SHA256: fields[2]}
	}
	if _, ok := out[secretID]; !ok {
		return nil, errors.New("falha ao ler o material da chave")
	}
	if _, ok := out[removedSecretID]; !ok {
		return nil, errors.New("falha ao ler o material da chave")
	}
	return out, nil
}

func removeEligibleObject(root, objectKey string) error {
	if strings.TrimSpace(root) == "" {
		return errors.New("objeto elegível ausente")
	}
	if err := requireDirNoSymlink(root); err != nil {
		return errors.New("objeto elegível ausente")
	}
	name, err := objectFileName(objectKey)
	if err != nil {
		return err
	}
	path, err := confinedChild(root, name)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("objeto elegível ausente")
	}
	if err := os.Remove(path); err != nil {
		return errors.New("falha ao remover o objeto elegível")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return errors.New("objeto excluído permaneceu após reaplicar deletion_records")
	}
	return nil
}

func objectAbsent(root, objectKey string) error {
	name, err := objectFileName(objectKey)
	if err != nil {
		return err
	}
	path, err := confinedChild(root, name)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return errors.New("objeto excluído permaneceu após reaplicar deletion_records")
	}
	return nil
}

func backupObjectIntact(dump, objectKey, version string) error {
	metas, err := readObjectManifest(dump)
	if err != nil {
		return err
	}
	meta, ok := metas[objectKey]
	if !ok || meta.Version != version {
		return errors.New("o backup não conservou o objeto excluído")
	}
	name, err := objectFileName(objectKey)
	if err != nil {
		return err
	}
	payload, err := readRegular(mustChild(mustChild(dump, "objects"), name), maxObjectBytes)
	if err != nil {
		return errors.New("o backup não conservou o objeto excluído")
	}
	sum := sha256.Sum256(payload)
	size := int64(len(payload))
	for i := range payload {
		payload[i] = 0
	}
	if hex.EncodeToString(sum[:]) != meta.SHA256 || size != meta.Size {
		return errors.New("o backup não conservou o objeto excluído")
	}
	return nil
}

func ensureMaterialNotLeaked(dump string) error {
	material, err := readRegular(mustChild(mustChild(dump, "recovery"), "material"), maxKeyFileBytes)
	if err != nil || len(material) == 0 {
		return errors.New("falha ao gravar o material sintético da chave")
	}
	defer func() {
		for i := range material {
			material[i] = 0
		}
	}()
	entries, err := os.ReadDir(dump)
	if err != nil {
		return errors.New("falha ao ler o dump")
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		body, err := readRegular(mustChild(dump, entry.Name()), maxDumpFileBytes)
		if err != nil {
			return errors.New("falha ao ler o dump")
		}
		if bytesContains(body, material) {
			return errors.New("material de chave vazou para o manifesto")
		}
	}
	return nil
}

func bytesContains(haystack, needle []byte) bool {
	return len(needle) > 0 && bytes.Contains(haystack, needle)
}

func requireKeyArtifact(dir string) error {
	if err := requirePrivateDir(dir); err != nil {
		return errors.New("artefato de chave com permissão indevida")
	}
	for _, name := range []string{"material", "current"} {
		path, err := confinedChild(dir, name)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("artefato de chave ausente")
		}
		if info.Mode().Perm() != 0o600 {
			return errors.New("artefato de chave com permissão indevida")
		}
		if info.Size() <= 0 || info.Size() > maxKeyFileBytes {
			return errors.New("artefato de chave corrompido")
		}
	}
	return nil
}

func requirePrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("artefato de chave ausente")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("artefato de chave com permissão indevida")
	}
	return nil
}

func requireDirNoSymlink(dir string) error {
	pinned, err := pinTempDir(dir)
	if err != nil {
		return err
	}
	if pinned != filepath.Clean(dir) {
		return errors.New("caminho recusado no armazenamento temporário")
	}
	info, err := os.Lstat(pinned)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("caminho recusado no armazenamento temporário")
	}
	return nil
}

func objectFileName(key string) (string, error) {
	if !validObjectKey(key) {
		return "", errors.New("caminho recusado no armazenamento temporário")
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:]), nil
}

func validObjectKey(key string) bool {
	if key == "" || len(key) > 120 || strings.Contains(key, "..") {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '/', r == '-', r == '_':
		default:
			return false
		}
	}
	return !strings.HasPrefix(key, "/") && !strings.HasSuffix(key, "/")
}

func onlyHex(value string) bool {
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

func canonicalAAD(ctx crypto.Context) (string, error) {
	parts := []string{ctx.TenantID, ctx.Entity, ctx.EntityID, ctx.Field}
	for _, part := range parts {
		if part == "" || len(part) > 200 || strings.ContainsAny(part, "\x00\x1f") {
			return "", errors.New("AAD inconsistente")
		}
	}
	return strings.Join(parts, "\x1f"), nil
}

func copyRegular(srcDir, dstDir, name string, mode os.FileMode, max int64) error {
	payload, err := readRegular(mustChild(srcDir, name), max)
	if err != nil {
		return err
	}
	dst, err := confinedChild(dstDir, name)
	if err != nil {
		return err
	}
	return writeExact(dst, payload, mode)
}

func readRegular(path string, max int64) ([]byte, error) {
	if path == "" {
		return nil, errors.New("caminho recusado no armazenamento temporário")
	}
	if _, err := lexicalUnderTemp(path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("caminho recusado no armazenamento temporário")
	}
	if info.Size() <= 0 || info.Size() > max {
		return nil, errors.New("caminho recusado no armazenamento temporário")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("caminho recusado no armazenamento temporário")
	}
	defer file.Close()
	buf := make([]byte, info.Size())
	if _, err := io.ReadFull(file, buf); err != nil {
		return nil, errors.New("caminho recusado no armazenamento temporário")
	}
	return buf, nil
}

func writeExact(path string, payload []byte, mode os.FileMode) error {
	if len(payload) == 0 {
		return errors.New("falha ao gravar o armazenamento temporário")
	}
	if _, err := lexicalUnderTemp(path); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return errors.New("falha ao gravar o armazenamento temporário")
	}
	defer file.Close()
	if _, err := file.Write(payload); err != nil {
		return errors.New("falha ao gravar o armazenamento temporário")
	}
	if err := file.Sync(); err != nil {
		return errors.New("falha ao gravar o armazenamento temporário")
	}
	if err := file.Chmod(mode); err != nil {
		return errors.New("falha ao gravar o armazenamento temporário")
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != mode {
		return errors.New("falha ao gravar o armazenamento temporário")
	}
	return nil
}

func mkdirExact(path string, mode os.FileMode) error {
	if _, err := lexicalUnderTemp(path); err != nil {
		return err
	}
	if err := os.Mkdir(path, mode); err != nil {
		return errors.New("falha ao gravar o armazenamento temporário")
	}
	if err := os.Chmod(path, mode); err != nil {
		return errors.New("falha ao gravar o armazenamento temporário")
	}
	return nil
}

func mustChild(root, name string) string {
	path, err := confinedChild(root, name)
	if err != nil {
		return ""
	}
	return path
}

func confinedChild(root, name string) (string, error) {
	if name == "" || name == "." || name == ".." || name != filepath.Base(name) {
		return "", errors.New("caminho recusado no armazenamento temporário")
	}
	if strings.ContainsAny(name, `/\`) {
		return "", errors.New("caminho recusado no armazenamento temporário")
	}
	root = filepath.Clean(root)
	full := filepath.Join(root, name)
	rel, err := filepath.Rel(root, full)
	if err != nil || rel != name {
		return "", errors.New("caminho recusado no armazenamento temporário")
	}
	if _, err := lexicalUnderTemp(full); err != nil {
		return "", err
	}
	return full, nil
}

func disableTree(path string) error {
	pinned, err := pinTempDir(path)
	if err != nil {
		return err
	}
	root, err := tempRoot()
	if err != nil {
		return err
	}
	if pinned == root {
		return errors.New("caminho recusado no armazenamento temporário")
	}
	if err := os.RemoveAll(pinned); err != nil {
		return errors.New("a fonte permaneceu disponível depois do backup")
	}
	if _, err := os.Lstat(pinned); !os.IsNotExist(err) {
		return errors.New("a fonte permaneceu disponível depois do backup")
	}
	return nil
}

func absent(path string) error {
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return errors.New("a fonte permaneceu disponível depois do backup")
	}
	return nil
}

func tempRoot() (string, error) {
	tmp, err := filepath.Abs(os.TempDir())
	if err != nil {
		return "", errors.New("ensaio recusado: diretório temporário indisponível")
	}
	resolved, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		return "", errors.New("ensaio recusado: diretório temporário indisponível")
	}
	return filepath.Clean(resolved), nil
}

func pinTempDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("caminho recusado no armazenamento temporário")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", errors.New("caminho recusado no armazenamento temporário")
	}
	resolved = filepath.Clean(resolved)
	if pathInRealCheckout(abs) || pathInRealCheckout(resolved) {
		return "", errors.New("caminho recusado no armazenamento temporário")
	}
	root, err := tempRoot()
	if err != nil {
		return "", err
	}
	if resolved != root && !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
		return "", errors.New("caminho recusado no armazenamento temporário")
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("caminho recusado no armazenamento temporário")
	}
	return resolved, nil
}

func lexicalUnderTemp(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("caminho recusado no armazenamento temporário")
	}
	abs = filepath.Clean(abs)
	if pathInRealCheckout(abs) {
		return "", errors.New("caminho recusado no armazenamento temporário")
	}
	root, err := tempRoot()
	if err != nil {
		return "", err
	}
	if abs != root && !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		return "", errors.New("caminho recusado no armazenamento temporário")
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == "." {
		return "", errors.New("caminho recusado no armazenamento temporário")
	}
	cur := root
	parts := strings.Split(rel, string(os.PathSeparator))
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("caminho recusado no armazenamento temporário")
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		last := i == len(parts)-1
		if err != nil {
			if last && os.IsNotExist(err) {
				return abs, nil
			}
			return "", errors.New("caminho recusado no armazenamento temporário")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("caminho recusado no armazenamento temporário")
		}
		if !last && !info.IsDir() {
			return "", errors.New("caminho recusado no armazenamento temporário")
		}
	}
	return abs, nil
}
